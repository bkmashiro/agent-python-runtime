#!/usr/bin/env bash

set -euo pipefail
SCRIPT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
# shellcheck source=demos/common.sh
source "$SCRIPT_DIR/common.sh"
require_command curl

banner "Long-running hot service: repeated runs and persistent workspace"
printf '%s\n' 'Observe: the local service reuses prepared execution for repeated HTTP Runs, then keeps workspace state across one edit/read lifecycle.'
PORT="${PYSOLATE_DEMO_PORT:-$(python3 - <<'PY'
import socket
with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
)}"
BASE_URL="http://127.0.0.1:$PORT"
LOG_FILE="$(mktemp -t pysolate-demo.XXXXXX)"
SERVER_DIR="$(mktemp -d -t pysolate-server.XXXXXX)"
SERVER_BIN="$SERVER_DIR/pysolate-server"
SERVER_PID=""
WORKSPACE=""

cleanup() {
  if [[ -n "$WORKSPACE" ]]; then
    curl -fsS -X DELETE "$BASE_URL/v1/workspaces/$WORKSPACE" >/dev/null 2>&1 || true
  fi
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" >/dev/null 2>&1; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" >/dev/null 2>&1 || true
  fi
  rm -f "$LOG_FILE"
  rm -rf "$SERVER_DIR"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

go build -o "$SERVER_BIN" ./cmd/pysolate-server
"$SERVER_BIN" \
  -guest "$GUEST" \
  -listen "127.0.0.1:$PORT" \
  -max-active 2 >"$LOG_FILE" 2>&1 &
SERVER_PID=$!

ready=0
for _ in $(seq 1 240); do
  if curl -fsS "$BASE_URL/healthz" >/dev/null 2>&1; then
    ready=1
    break
  fi
  if ! kill -0 "$SERVER_PID" >/dev/null 2>&1; then
    printf 'service exited before becoming ready:\n' >&2
    sed -n '1,160p' "$LOG_FILE" >&2
    exit 1
  fi
  sleep 0.25
done
if [[ "$ready" -ne 1 ]]; then
  printf 'service did not become ready:\n' >&2
  sed -n '1,160p' "$LOG_FILE" >&2
  exit 1
fi

printf 'Service: %s\n' "$BASE_URL"

for value in 41 99; do
  printf '\nHot request with value=%s\n' "$value"
  curl -fsS \
    -H 'Content-Type: application/json' \
    -d "{\"source\":\"result=inputs['value']+1\",\"inputs\":{\"value\":$value}}" \
    "$BASE_URL/v1/run" | pretty_json
done

CREATE_RESPONSE="$(curl -fsS \
  -H 'Content-Type: application/json' \
  -d '{"files":[{"path":"config.json","data":"eyJlbmFibGVkIjpmYWxzZX0K"},{"path":"numbers.csv","data":"bmFtZSx2YWx1ZQphLDEwCmIsMjAK"}]}' \
  "$BASE_URL/v1/workspaces")"
WORKSPACE="$(printf '%s' "$CREATE_RESPONSE" | python3 -c 'import json,sys; print(json.load(sys.stdin)["workspace"])')"
printf '\nCreated workspace: %s\n' "$WORKSPACE"

RUN_BODY="$(python3 - <<'PY'
import json
source = '''import csv
import json
from pathlib import Path

rows = list(csv.DictReader(Path("numbers.csv").read_text().splitlines()))
total = sum(int(row["value"]) for row in rows)
config = json.loads(Path("config.json").read_text())
config.update(enabled=True, total=total)
Path("config.json").write_text(json.dumps(config, sort_keys=True) + "\\n")
Path("REPORT.md").write_text(f"# Demo report\\n\\nTotal: {total}\\n")
result = {"cwd_file_count": len(list(Path(".").iterdir())), "total": total}
'''
print(json.dumps({"source": source, "inputs": {}}))
PY
)"

printf '\nWorkspace run and deterministic diff\n'
curl -fsS \
  -H 'Content-Type: application/json' \
  -d "$RUN_BODY" \
  "$BASE_URL/v1/workspaces/$WORKSPACE/run" | pretty_json

printf '\nRead REPORT.md back through the bounded file API\n'
curl -fsS "$BASE_URL/v1/workspaces/$WORKSPACE/files?path=REPORT.md" |
  python3 -c 'import base64,json,sys; body=json.load(sys.stdin); print(base64.b64decode(body["data"]).decode(), end="")'

printf '\nDestroy workspace\n'
curl -fsS -X DELETE "$BASE_URL/v1/workspaces/$WORKSPACE" | pretty_json
WORKSPACE=""
