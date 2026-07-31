#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

mkdir -p "$tmp/bin" "$tmp/state" "$tmp/home" "$tmp/codex-home"
chmod 700 "$tmp/state"
printf 'wasm' > "$tmp/guest.wasm"
printf '{}' > "$tmp/manifest.json"
cat > "$tmp/bin/codex" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$@" > "$CODEX_ARGV_OUT"
SH
printf '#!/usr/bin/env bash\nexit 99\n' > "$tmp/bin/go"
printf '#!/usr/bin/env bash\nexit 0\n' > "$tmp/apyrun-mcp"
chmod 700 "$tmp/bin/codex" "$tmp/bin/go" "$tmp/apyrun-mcp"

export PATH="$tmp/bin:$PATH"
export HOME="$tmp/home"
export CODEX_HOME="$tmp/codex-home"
export CODEX_ARGV_OUT="$tmp/codex.argv"
export APYRUN_MCP_BIN="$tmp/apyrun-mcp"
export APYRUN_ARTIFACT="$tmp/guest.wasm"
export APYRUN_MANIFEST="$tmp/manifest.json"
export APYRUN_STATE_DIR="$tmp/state"

"$repo_root/scripts/codex-runtime" -C "$tmp/worktree"

python3 - "$tmp/codex.argv" "$tmp/state" "$tmp" <<'PY'
from pathlib import Path
import sys

argv = Path(sys.argv[1]).read_text().splitlines()
joined = "\n".join(argv)
required = [
    'mcp_servers.agent_python_runtime.command=',
    'mcp_servers.agent_python_runtime.args=',
    'mcp_servers.agent_python_runtime.enabled=true',
    'mcp_servers.agent_python_runtime.required=true',
    'mcp_servers.agent_python_runtime.enabled_tools=["python_runtime"]',
    'mcp_servers.agent_python_runtime.default_tools_approval_mode="prompt"',
]
for value in required:
    if value not in joined:
        raise SystemExit(f"missing Codex override: {value}\n{joined}")
if argv[-2:] != ["-C", str(Path(sys.argv[2]).parent / "worktree")]:
    raise SystemExit(f"Codex arguments not preserved: {argv}")
if any(path.name == "config.toml" for path in Path(sys.argv[3]).rglob("config.toml")):
    raise SystemExit("wrapper wrote a persistent Codex config")
args_override = next(value for value in argv if value.startswith("mcp_servers.agent_python_runtime.args="))
if str(Path(sys.argv[2])) not in args_override or "trace-" not in args_override:
    raise SystemExit(f"private per-session trace missing: {args_override}")
PY

export APYRUN_MCP_APPROVAL_MODE=approve
export CODEX_ARGV_OUT="$tmp/codex-approve.argv"
"$repo_root/scripts/codex-runtime" exec --ephemeral test
grep -F 'mcp_servers.agent_python_runtime.default_tools_approval_mode="approve"' "$CODEX_ARGV_OUT"
