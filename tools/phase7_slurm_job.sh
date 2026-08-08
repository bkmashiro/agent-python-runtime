#!/bin/bash
#SBATCH --partition=t4
#SBATCH --gres=gpu:tesla_t4:1
#SBATCH --nodes=1
#SBATCH --ntasks=1
#SBATCH --cpus-per-task=4
#SBATCH --mem=16G
#SBATCH --time=04:00:00
#SBATCH --job-name=pysolate-p7
#SBATCH --export=NIL
#SBATCH --output=/vol/bitbucket/ys25/pysolate-p7-slurm-%j.out

set -Eeuo pipefail
umask 077
export PATH=/usr/local/bin:/usr/bin:/bin

if [[ $# -ne 4 ]]; then
  printf 'usage: %s {canary|formal} /vol/bitbucket/ys25/STAGE SOURCE_COMMIT {cow-first|non-cow-first}\n' "$0" >&2
  exit 64
fi
TIER="$1"
STAGE="$2"
SOURCE_COMMIT="$3"
ORDER="$4"
case "$TIER" in canary|formal) ;; *) printf 'invalid tier\n' >&2; exit 64 ;; esac
case "$ORDER" in cow-first|non-cow-first) ;; *) printf 'invalid arm order\n' >&2; exit 64 ;; esac
[[ "$STAGE" =~ ^/vol/bitbucket/ys25/[A-Za-z0-9._-]+$ ]] || { printf 'unsafe stage path\n' >&2; exit 64; }
[[ "$SOURCE_COMMIT" =~ ^[0-9a-f]{40}$ ]] || { printf 'invalid source commit\n' >&2; exit 64; }
test -d "$STAGE" && test ! -L "$STAGE"
test -d "$STAGE/input" && test ! -L "$STAGE/input"

NODE_ROOT="${SLURM_TMPDIR:-/tmp}/pysolate-p7-${SLURM_JOB_ID}"
INPUT="$NODE_ROOT/input"
REPO="$NODE_ROOT/repo"
RESULT="$NODE_ROOT/result"
OUTBOX="$STAGE/outbox"
ACK="$STAGE/ACK-${SLURM_JOB_ID}"
FAILED="$OUTBOX/FAILED-${SLURM_JOB_ID}"
FAILED_TMP="$OUTBOX/FAILED-${SLURM_JOB_ID}.partial"
OWNER_MARKER="$NODE_ROOT/.phase7-owner"
OWNER_MARKER_TMP="$NODE_ROOT/.phase7-owner.partial"
owner_token="${SLURM_JOB_ID}:${SOURCE_COMMIT}:$$"
failure_line=0
node_root_created=false

if [[ -e "$OUTBOX" ]] || [[ -L "$OUTBOX" ]]; then
  test -d "$OUTBOX" && test ! -L "$OUTBOX"
else
  mkdir "$OUTBOX"
fi
chmod 700 "$OUTBOX"
# shellcheck disable=SC2329
cleanup_node_root() {
  marker_value=""
  if [[ "$node_root_created" = true ]] && [[ -d "$NODE_ROOT" ]] && [[ ! -L "$NODE_ROOT" ]]; then
    if [[ -f "$OWNER_MARKER" ]] && [[ ! -L "$OWNER_MARKER" ]] &&
       IFS= read -r marker_value < "$OWNER_MARKER" && [[ "$marker_value" == "$owner_token" ]]; then
      rm -rf -- "$NODE_ROOT"
    else
      if [[ ! -e "$OWNER_MARKER" ]] && [[ ! -L "$OWNER_MARKER" ]] &&
         { [[ ! -e "$OWNER_MARKER_TMP" ]] || { [[ -f "$OWNER_MARKER_TMP" ]] && [[ ! -L "$OWNER_MARKER_TMP" ]]; }; }; then
        rm -f -- "$OWNER_MARKER_TMP"
        rmdir -- "$NODE_ROOT" 2>/dev/null || true
      fi
    fi
  fi
}
# shellcheck disable=SC2329
record_failure_and_cleanup() {
  status=$?
  trap - ERR EXIT
  set +e
  if [[ "$status" -ne 0 ]] && [[ -d "$OUTBOX" ]] && [[ ! -L "$OUTBOX" ]] &&
     [[ ! -e "$FAILED" ]] && [[ ! -L "$FAILED" ]] && [[ ! -e "$FAILED_TMP" ]] && [[ ! -L "$FAILED_TMP" ]]; then
    {
      printf 'job_id=%s\n' "$SLURM_JOB_ID"
      printf 'source_commit=%s\n' "$SOURCE_COMMIT"
      printf 'exit_code=%s\n' "$status"
      printf 'failure_line=%s\n' "$failure_line"
    } > "$FAILED_TMP"
    chmod 400 "$FAILED_TMP"
    if ln -- "$FAILED_TMP" "$FAILED"; then
      rm -- "$FAILED_TMP"
    fi
  fi
  cleanup_node_root
  exit "$status"
}
abort_job() {
  failure_line="$1"
  code="$2"
  shift 2
  printf '%s\n' "$*" >&2
  exit "$code"
}
trap 'failure_line=$LINENO' ERR
trap record_failure_and_cleanup EXIT
trap 'abort_job "$LINENO" 129 "received SIGHUP"' HUP
trap 'abort_job "$LINENO" 130 "received SIGINT"' INT
trap 'abort_job "$LINENO" 143 "received SIGTERM"' TERM

test ! -e "$ACK" && test ! -L "$ACK"
test ! -e "$FAILED" && test ! -L "$FAILED"
test ! -e "$FAILED_TMP" && test ! -L "$FAILED_TMP"
test "${SLURM_JOB_PARTITION:-}" = t4
test "${SLURM_CPUS_PER_TASK:-}" = 4
test "${SLURM_JOB_NUM_NODES:-}" = 1
test "${SLURM_NTASKS:-}" = 1
case "${SLURM_MEM_PER_NODE:-}" in 16384|16G) ;; *) abort_job "$LINENO" 65 'unexpected memory allocation' ;; esac
test "${SLURM_GPUS_ON_NODE:-}" = 1
test -n "${CUDA_VISIBLE_DEVICES:-}"
[[ "${CUDA_VISIBLE_DEVICES}" != *,* ]]

if find "$STAGE/input" -type l -print -quit | grep -q .; then
  abort_job "$LINENO" 65 'symlinked staged input is forbidden'
fi
test -d "$STAGE/input/artifacts" && test ! -L "$STAGE/input/artifacts"
test -d "$STAGE/input/bin" && test ! -L "$STAGE/input/bin"
test "$(find "$STAGE/input" -type d -print | wc -l | tr -d '[:space:]')" -eq 3
if find "$STAGE/input" -mindepth 1 ! -type f ! -type d -print -quit | grep -q .; then
  abort_job "$LINENO" 65 'special staged input is forbidden'
fi

expected_payload_files=(
  SOURCE_COMMIT
  artifacts/agent-python-runtime-numpy-core.wasm
  artifacts/manifest.json
  bin/apyrun-benchmark-linux-amd64
  phase7_slurm_job.sh
  source.bundle
)
expected_payload_max_bytes=(128 268435456 1048576 134217728 131072 67108864)
test "$(find "$STAGE/input" -type f -print | wc -l | tr -d '[:space:]')" -eq "$((${#expected_payload_files[@]} + 1))"
for index in "${!expected_payload_files[@]}"; do
  candidate="$STAGE/input/${expected_payload_files[$index]}"
  test -f "$candidate"
  test ! -L "$candidate"
  candidate_size="$(stat -c '%s' "$candidate")"
  test "$candidate_size" -gt 0
  test "$candidate_size" -le "${expected_payload_max_bytes[$index]}"
done
test -f "$STAGE/input/payload.SHA256"
test ! -L "$STAGE/input/payload.SHA256"
payload_manifest_size="$(stat -c '%s' "$STAGE/input/payload.SHA256")"
test "$payload_manifest_size" -gt 0
test "$payload_manifest_size" -le 8192

if ! mkdir -m 700 "$NODE_ROOT"; then
  abort_job "$LINENO" 66 'compute root already exists'
fi
node_root_created=true
printf '%s\n' "$owner_token" > "$OWNER_MARKER_TMP"
chmod 400 "$OWNER_MARKER_TMP"
ln -- "$OWNER_MARKER_TMP" "$OWNER_MARKER"
rm -- "$OWNER_MARKER_TMP"
mkdir -m 700 "$INPUT"
mkdir -m 700 "$INPUT/artifacts" "$INPUT/bin"
chmod 700 "$NODE_ROOT" "$INPUT"
copy_bounded_regular() {
  python3 - "$1" "$2" "$3" <<'PY'
import hashlib
import os
import stat
import sys

source, destination, maximum_text = sys.argv[1:]
maximum = int(maximum_text)
source_fd = os.open(source, os.O_RDONLY | os.O_NONBLOCK | os.O_NOFOLLOW)
try:
    before = os.fstat(source_fd)
    if not stat.S_ISREG(before.st_mode) or before.st_size <= 0 or before.st_size > maximum:
        raise SystemExit("staged source is not a bounded regular file")
    destination_fd = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    digest = hashlib.sha256()
    try:
        remaining = before.st_size
        while remaining:
            chunk = os.read(source_fd, min(1024 * 1024, remaining))
            if not chunk:
                raise SystemExit("staged source shrank while copying")
            digest.update(chunk)
            view = memoryview(chunk)
            while view:
                written = os.write(destination_fd, view)
                view = view[written:]
            remaining -= len(chunk)
        if os.read(source_fd, 1):
            raise SystemExit("staged source grew while copying")
        after = os.fstat(source_fd)
        if (before.st_dev, before.st_ino, before.st_size) != (after.st_dev, after.st_ino, after.st_size):
            raise SystemExit("staged source identity changed while copying")
        os.fsync(destination_fd)
    finally:
        os.close(destination_fd)
finally:
    os.close(source_fd)
print(digest.hexdigest())
PY
}

expected_payload_manifest="$NODE_ROOT/payload.expected"
: > "$expected_payload_manifest"
artifact_digest=''
manifest_digest=''
for index in "${!expected_payload_files[@]}"; do
  relative_path="${expected_payload_files[$index]}"
  digest="$(copy_bounded_regular "$STAGE/input/$relative_path" "$INPUT/$relative_path" "${expected_payload_max_bytes[$index]}")"
  printf '%s  %s\n' "$digest" "$relative_path" >> "$expected_payload_manifest"
  case "$relative_path" in
    artifacts/agent-python-runtime-numpy-core.wasm) artifact_digest="$digest" ;;
    artifacts/manifest.json) manifest_digest="$digest" ;;
  esac
done
test "$artifact_digest" = f00f22ac94a66f2f2e67573da11ef879f8b5e46622eb9379300cc1e6a5b40a30
test "$manifest_digest" = 458a4e4bbec1ad225f0f3c38357738f1937b1e16d5388f76cdf4c460ce6839fa
copy_bounded_regular "$STAGE/input/payload.SHA256" "$INPUT/payload.SHA256" 8192 >/dev/null
cmp -- "$expected_payload_manifest" "$INPUT/payload.SHA256"
test "$(find "$INPUT" -type f -print | wc -l | tr -d '[:space:]')" -eq "$((${#expected_payload_files[@]} + 1))"
(cd "$INPUT" && sha256sum --check payload.SHA256)
test "$(tr -d '\r\n' < "$INPUT/SOURCE_COMMIT")" = "$SOURCE_COMMIT"

git clone "$INPUT/source.bundle" "$REPO" >/dev/null 2>&1
test "$(git -C "$REPO" rev-parse HEAD)" = "$SOURCE_COMMIT"
test -z "$(git -C "$REPO" status --porcelain=v1 --untracked-files=all)"
test -f "$0" && test ! -L "$0"
cmp -- "$0" "$INPUT/phase7_slurm_job.sh"
cmp -- "$INPUT/phase7_slurm_job.sh" "$REPO/tools/phase7_slurm_job.sh"
expected_binary_identity="$NODE_ROOT/binary-source.expected.json"
actual_binary_identity="$NODE_ROOT/binary-source.actual.json"
printf '{"revision":"%s","modified":false}\n' "$SOURCE_COMMIT" > "$expected_binary_identity"
"$INPUT/bin/apyrun-benchmark-linux-amd64" -kind binary-source-identity > "$actual_binary_identity"
cmp -- "$expected_binary_identity" "$actual_binary_identity"

SAMPLES=1
if [[ "$TIER" = formal ]]; then SAMPLES=3; fi
mkdir -m 700 "$RESULT"
{
  printf 'job_id=%s\n' "$SLURM_JOB_ID"
  printf 'tier=%s\n' "$TIER"
  printf 'arm_order=%s\n' "$ORDER"
  printf 'source_commit=%s\n' "$SOURCE_COMMIT"
  printf 'hostname=%s\n' "$(hostname)"
  printf 'kernel=%s\n' "$(uname -srmo)"
  printf 'python=%s\n' "$(python3 --version 2>&1)"
  printf 'cpus_per_task=%s\n' "${SLURM_CPUS_PER_TASK:-unknown}"
  printf 'memory_per_node=%s\n' "${SLURM_MEM_PER_NODE:-unknown}"
  printf 'gpus_on_node=%s\n' "${SLURM_GPUS_ON_NODE:-unknown}"
  printf 'job_partition=%s\n' "${SLURM_JOB_PARTITION:-unknown}"
  printf 'cuda_visible_devices=%s\n' "${CUDA_VISIBLE_DEVICES:-unknown}"
  printf 'started_utc=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
} > "$NODE_ROOT/ENVIRONMENT.txt"

if [[ "$ORDER" = cow-first ]]; then
  arms='cow-ready-single-use single-use-preinitialized'
else
  arms='single-use-preinitialized cow-ready-single-use'
fi
for strategy in $arms; do
  case "$strategy" in
    cow-ready-single-use) output="$RESULT/cow.json" ;;
    single-use-preinitialized) output="$RESULT/non-cow.json" ;;
    *) abort_job "$LINENO" 70 'unexpected strategy' ;;
  esac
  "$INPUT/bin/apyrun-benchmark-linux-amd64" \
    -kind lifecycle-density \
    -class profile-candidate \
    -strategy "$strategy" \
    -prepared-warmup-profile numpy-ready-v1 \
    -artifact "$INPUT/artifacts/agent-python-runtime-numpy-core.wasm" \
    -manifest "$INPUT/artifacts/manifest.json" \
    -samples "$SAMPLES" \
    -max-rss-bytes 8589934592 \
    -child-timeout 4m \
    -output "$output"
  "$INPUT/bin/apyrun-benchmark-linux-amd64" \
    -kind validate-lifecycle-density \
    -input "$output" \
    -schema "$REPO/benchmark/v1/lifecycle-density.schema.json" \
    -artifact "$INPUT/artifacts/agent-python-runtime-numpy-core.wasm" \
    -manifest "$INPUT/artifacts/manifest.json" \
    > "$output.validation.json"
done

python3 "$REPO/tools/phase7_density.py" \
  --benchmark "$INPUT/bin/apyrun-benchmark-linux-amd64" \
  --schema "$REPO/benchmark/v1/lifecycle-density.schema.json" \
  --artifact "$INPUT/artifacts/agent-python-runtime-numpy-core.wasm" \
  --manifest "$INPUT/artifacts/manifest.json" \
  --cow "$RESULT/cow.json" \
  --non-cow "$RESULT/non-cow.json" \
  --output "$RESULT/paired-summary.json"

(
  cd "$RESULT"
  sha256sum cow.json cow.json.validation.json non-cow.json non-cow.json.validation.json paired-summary.json > SHA256SUMS
  sha256sum --check SHA256SUMS
)
printf 'completed_utc=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$NODE_ROOT/ENVIRONMENT.txt"
printf '%s\n' "$SOURCE_COMMIT" > "$NODE_ROOT/RUN_COMPLETE"

archive_tmp="$OUTBOX/result-${SLURM_JOB_ID}.tar.gz.partial"
archive="$OUTBOX/result-${SLURM_JOB_ID}.tar.gz"
checksum_tmp="$OUTBOX/result-${SLURM_JOB_ID}.tar.gz.sha256.partial"
checksum="$OUTBOX/result-${SLURM_JOB_ID}.tar.gz.sha256"
ready_tmp="$OUTBOX/READY-${SLURM_JOB_ID}.partial"
ready="$OUTBOX/READY-${SLURM_JOB_ID}"
acked_tmp="$OUTBOX/ACKED-${SLURM_JOB_ID}.partial"
acked="$OUTBOX/ACKED-${SLURM_JOB_ID}"
for path in "$archive_tmp" "$archive" "$checksum_tmp" "$checksum" "$ready_tmp" "$ready" "$acked_tmp" "$acked"; do
  test ! -e "$path" && test ! -L "$path"
done

publish_exclusive() {
  local source_path="$1"
  local destination_path="$2"
  test -f "$source_path" && test ! -L "$source_path"
  ln -- "$source_path" "$destination_path"
  rm -f -- "$source_path" || true
}

ack_is_exact() {
  python3 - "$ACK" "$archive_sha" <<'PY'
import os
import stat
import sys

path, digest = sys.argv[1:]
expected = (digest + "\n").encode("ascii")
try:
    descriptor = os.open(path, os.O_RDONLY | os.O_NONBLOCK | os.O_NOFOLLOW)
except OSError:
    raise SystemExit(1)
try:
    metadata = os.fstat(descriptor)
    if not stat.S_ISREG(metadata.st_mode) or metadata.st_size != len(expected):
        raise SystemExit(1)
    if os.read(descriptor, len(expected) + 1) != expected:
        raise SystemExit(1)
finally:
    os.close(descriptor)
PY
}

tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner \
  -C "$NODE_ROOT" -czf "$archive_tmp" result ENVIRONMENT.txt RUN_COMPLETE
archive_sha="$(sha256sum "$archive_tmp" | cut -d' ' -f1)"
printf '%s  %s\n' "$archive_sha" "$(basename "$archive")" > "$checksum_tmp"
publish_exclusive "$archive_tmp" "$archive"
publish_exclusive "$checksum_tmp" "$checksum"
printf '%s  %s\n' "$archive_sha" "$(basename "$archive")" > "$ready_tmp"
publish_exclusive "$ready_tmp" "$ready"

for _ in $(seq 1 180); do
  if ack_is_exact; then
    printf '%s  %s\n' "$archive_sha" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$acked_tmp"
    publish_exclusive "$acked_tmp" "$acked"
    exit 0
  fi
  sleep 10
done
abort_job "$LINENO" 72 "ACK timeout for $archive_sha"
