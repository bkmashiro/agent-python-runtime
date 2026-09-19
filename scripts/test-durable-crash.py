#!/usr/bin/env python3
"""Crash the demo at persisted waiting and external-commit boundaries."""
import argparse
import json
import select
import sqlite3
import subprocess
import time
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--binary', required=True)
parser.add_argument('--guest', required=True)
parser.add_argument('--output-dir', type=Path, required=True)
args = parser.parse_args()
args.output_dir = args.output_dir.resolve()
args.output_dir.mkdir(parents=True, exist_ok=True)
db = args.output_dir / 'runs.db'
provider = args.output_dir / 'provider.db'
if db.exists() or provider.exists():
    raise SystemExit('Use a new output directory for each crash test')
base = [str(Path(args.binary).resolve())]
common = ['-db', str(db), '-provider-db', str(provider), '-run-id', 'crash-run']
guest = ['-guest', str(Path(args.guest).resolve())]


def command(name, *flags):
    result = subprocess.run(base + [name] + common + list(flags), capture_output=True, text=True, timeout=90)
    if result.returncode:
        raise RuntimeError(result.stderr)
    return json.loads(result.stdout)


def crash_at(point, *flags):
    process = subprocess.Popen(base + ['resume'] + common + guest + ['-hold-point', point] + list(flags), stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    assert process.stdout is not None and process.stderr is not None
    try:
        ready, _, _ = select.select([process.stdout], [], [], 90)
        if not ready:
            raise TimeoutError('No checkpoint event received')
        line = process.stdout.readline()
        if not line:
            raise RuntimeError(process.stderr.read())
        event = json.loads(line)
        assert event['event'] == point, event
        process.kill()
        process.wait(timeout=10)
        assert process.returncode < 0
        return event
    finally:
        if process.poll() is None:
            process.kill()
            process.wait(timeout=10)


def calls():
    connection = sqlite3.connect(f'file:{db}?mode=ro', uri=True)
    try:
        return connection.execute('SELECT sequence,state,operation_key FROM calls ORDER BY sequence').fetchall()
    finally:
        connection.close()


command('create', *guest, '-read-value', '7')
park = crash_at('waiting')
after_wait = calls()
assert [row[1] for row in after_wait] == ['completed', 'waiting'], after_wait
command('decide', '-wait-id', park['wait_id'], '-decision', 'true')
write_event = crash_at('provider-committed', '-read-value', '999')
after_write = calls()
assert [row[1] for row in after_write] == ['completed', 'completed', 'pending'], after_write
first_provider = command('provider-status')
assert first_provider['read_dispatches'] == 1, first_provider
assert first_provider['write_requests'] == first_provider['effect_count'] == 1, first_provider
started = time.monotonic()
result = command('resume', *guest, '-read-value', '999')
resume_seconds = time.monotonic() - started
assert result['status'] == 'ok', result
assert result['result']['read_value'] == 7, result
final_provider = command('provider-status')
assert final_provider['read_dispatches'] == 1, final_provider
assert final_provider['write_requests'] == 2, final_provider
assert final_provider['effect_count'] == 1, final_provider
assert command('resume', *guest) == result
assert command('provider-status') == final_provider
report = {'process_kills': 2, 'waiting': park, 'provider_committed': write_event,
          'after_wait': after_wait, 'after_write': after_write,
          'result': result, 'provider': final_provider, 'resume_seconds': resume_seconds}
(args.output_dir / 'result.json').write_text(json.dumps(report, indent=2) + '\n')
print(json.dumps(report, indent=2))
