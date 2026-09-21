#!/usr/bin/env bash

set -euo pipefail
SCRIPT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
# shellcheck source=demos/common.sh
source "$SCRIPT_DIR/common.sh"

banner "Fresh isolated Python: inputs + NumPy + YAML + one Host tool"

cat <<'PY' | go run ./cmd/pysolate \
  -wasm "$GUEST" \
  -prepared copy \
  -inputs '{"values":[3,5,8,13],"workflow":"name: demo\nretries: 2\n"}'
import numpy as np
import yaml

values = np.array(inputs["values"], dtype=np.float64)
workflow = yaml.safe_load(inputs["workflow"])
host = echo(event="demo", count=len(values))
result = {
    "mean": float(values.mean()),
    "maximum": float(values.max()),
    "workflow": workflow["name"],
    "retries": workflow["retries"],
    "host_echo": host,
}
PY
