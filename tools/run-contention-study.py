#!/usr/bin/env python3
"""Small alternating-process pilot. Full traces stay in an explicit private folder."""
import argparse
import collections
import hashlib
import json
import os
from pathlib import Path
import subprocess


def validate_trace(path):
    events = [json.loads(line) for line in Path(path).read_text().splitlines()]
    if not events or events[0].get("kind") != "header" or events[-1].get("kind") != "summary":
        raise ValueError("partial trace: missing header or summary")
    header, summary = events[0], events[-1]
    if not summary.get("complete"):
        raise ValueError("incomplete campaign")
    rows = [e for e in events if e.get("kind") == "request_end" and e["id"] >= 0]
    n = header["config"]["requests"]
    if len(rows) != n or sorted(r["id"] for r in rows) != list(range(n)):
        raise ValueError("missing or duplicate request outcomes")
    allowed = {"ok", "timeout", "rejected", "cancelled", "failed", "incorrect"}
    counts = collections.Counter(r["status"] for r in rows)
    if not set(counts) <= allowed or any(summary["counts"].get(k) != counts[k] for k in allowed):
        raise ValueError("status accounting mismatch")
    if counts["failed"] or counts["incorrect"] or counts["cancelled"]:
        raise ValueError("unexpected execution/correctness/cancellation failure")
    keyed = {}
    for kind in ("tool_queued", "tool_started", "tool_end"):
        items = [e for e in events if e.get("kind") == kind]
        mapping = {(e["request_id"], e["call_id"]): e for e in items}
        if len(mapping) != len(items):
            raise ValueError("duplicate tool event")
        keyed[kind] = mapping
    queued, started, ended = (keyed[k] for k in ("tool_queued", "tool_started", "tool_end"))
    if set(queued) != set(ended) or set(started) != {k for k, v in ended.items() if v["acquired"]}:
        raise ValueError("unresolved tool call")
    if any(queued[k]["args"] != v["args"] for k, v in ended.items()):
        raise ValueError("tool arguments changed in recording")
    measured = [e for e in ended.values() if e["request_id"] >= 0]
    calls = collections.Counter(e["request_id"] for e in measured)
    programs = {w["name"]: w for w in header["programs"]}
    for r in rows:
        if r["calls"] != calls[r["id"]]:
            raise ValueError("per-request call accounting mismatch")
        if r["status"] == "ok":
            w = programs[r["work"]]
            if r["output"]["Value"] != json.loads(w["expected_json"]) or r["calls"] != w["expected_calls_on_success"]:
                raise ValueError("incorrect successful result")
    if summary["scheduled"] != n or summary["calls"] != len(measured) or summary["backend"]["attempts"] != len(measured):
        raise ValueError("summary accounting mismatch")
    if summary["backend"]["end_inflight"] != 0 or summary["backend"]["peak_inflight"] > header["config"]["tool_capacity"]:
        raise ValueError("backend capacity/cleanup violated")
    if header.get("decision_events"):
        decisions = [e for e in events if e.get("kind") == "mode_decision" and e["request_id"] >= 0]
        if sorted(e["request_id"] for e in decisions) != sorted(r["id"] for r in rows if r["admitted"]):
            raise ValueError("missing or duplicate mode decision")
        for e in decisions:
            arm = header["config"]["arm"]
            expected = arm == "early" or (arm == "spare-capacity" and e["occupied_tool_slots"] < header["config"]["tool_capacity"])
            if e["early_reads"] != expected:
                raise ValueError("mode decision violates declared policy")
        if any(r["status"] == "ok" and r["latency_ns"] > header["config"]["timeout_ns"] for r in rows):
            raise ValueError("late request counted as on-time")
    return summary


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--binary", required=True, type=Path)
    p.add_argument("--guest", required=True, type=Path)
    p.add_argument("--out", required=True, type=Path, help="new private directory, never overwritten")
    p.add_argument("--requests", type=int, default=60)
    p.add_argument("--repeats", type=int, default=3)
    p.add_argument("--interval-ms", default="100,40,20")

    p.add_argument("--arms", default="sequential,early", help="sequential,early,spare-capacity")
    p.add_argument("--timeout-ms", type=int, default=400)
    p.add_argument("--prepare", choices=("copy", "cow"), default="copy")
    p.add_argument("--cow-data-image", action="store_true")
    args = p.parse_args()
    intervals = [int(x) for x in args.interval_ms.split(",")]
    arms = args.arms.split(",")
    if len(set(arms)) != len(arms) or not set(arms) <= {"sequential", "early", "spare-capacity"}:
        p.error("arms must be distinct supported names")
    if args.repeats < 1 or args.requests < 1 or not intervals or any(x < 0 for x in intervals):
        p.error("positive repeats/requests and nonnegative intervals required")
    args.out.mkdir(parents=True, mode=0o700, exist_ok=False)
    binary, guest = args.binary.resolve(), args.guest.resolve()
    def save(path, data):
        with open(path, "x", opener=lambda name, flags: os.open(name, flags, 0o600)) as f:
            json.dump(data, f, indent=2)
            f.write("\n")
    save(args.out / "plan.json", {"requests":args.requests, "repeats":args.repeats, "interval_ms":intervals,
        "timeout_ms":args.timeout_ms, "prepare":args.prepare, "cow_data_image":args.cow_data_image, "arms":arms,
        "binary_sha256":hashlib.sha256(binary.read_bytes()).hexdigest(),
        "guest_sha256":hashlib.sha256(guest.read_bytes()).hexdigest(), "gomaxprocs":2})
    env = os.environ.copy()
    env["GOMAXPROCS"] = "2"
    # Each arm is a new process. Rotate order across repetitions and load levels.
    with open(args.out / "summaries.jsonl", "x", opener=lambda name, flags: os.open(name, flags, 0o600)) as summary_file:
        for index, interval in enumerate(intervals):
            for repeat in range(args.repeats):
                offset = (index + repeat) % len(arms)
                for arm in arms[offset:] + arms[:offset]:
                    name = f"i{interval}-r{repeat}-{arm}"
                    trace = args.out / f"{name}.jsonl"
                    command = [str(binary), "-guest", str(guest), "-out", str(trace), "-arm", arm,
                        "-requests", str(args.requests), "-interval", f"{interval}ms", "-timeout", f"{args.timeout_ms}ms",
                        "-prepare", args.prepare]
                    if args.cow_data_image:
                        command += ["-cow-data-image"]
                    result = subprocess.run(command, env=env, text=True, capture_output=True,
                        timeout=240 + interval * args.requests / 1000)
                    if result.returncode:
                        save(args.out / f"{name}.failure.json", {"returncode":result.returncode,"stdout":result.stdout,"stderr":result.stderr})
                        raise RuntimeError(f"failed {name}; partial trace retained")
                    summary = validate_trace(trace)
                    if json.loads(result.stdout) != summary:
                        raise ValueError("stdout and saved summary differ")
                    summary["repeat"] = repeat
                    summary_file.write(json.dumps(summary) + "\n")
                    summary_file.flush()
                    print(name, summary["counts"], "p95_ms", None if summary["ok_p95_ns"] is None else round(summary["ok_p95_ns"]/1e6, 2), flush=True)
    print("verified processes:", len(intervals)*args.repeats*len(arms))


if __name__ == "__main__":
    main()
