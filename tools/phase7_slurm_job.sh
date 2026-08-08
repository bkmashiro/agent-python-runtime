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
test "${SLURM_JOB_PARTITION:-}" = t4
test "${SLURM_CPUS_PER_TASK:-}" = 4
test "${SLURM_JOB_NUM_NODES:-}" = 1
test "${SLURM_NTASKS:-}" = 1
case "${SLURM_MEM_PER_NODE:-}" in 16384|16G) ;; *) printf 'unexpected memory allocation\n' >&2; exit 65 ;; esac
test "${SLURM_GPUS_ON_NODE:-}" = 1
test -n "${CUDA_VISIBLE_DEVICES:-}"
[[ "${CUDA_VISIBLE_DEVICES}" != *,* ]]

test -d "$STAGE" && test ! -L "$STAGE"
test -d "$STAGE/input" && test ! -L "$STAGE/input"
if find "$STAGE/input" -type l -print -quit | grep -q .; then
  printf 'symlinked staged input is forbidden\n' >&2
  exit 65
fi

NODE_ROOT="${SLURM_TMPDIR:-/tmp}/pysolate-p7-${SLURM_JOB_ID}"
INPUT="$NODE_ROOT/input"
REPO="$NODE_ROOT/repo"
RESULT="$NODE_ROOT/result"
OUTBOX="$STAGE/outbox"
ACK="$STAGE/ACK-${SLURM_JOB_ID}"
FAILED="$OUTBOX/FAILED-${SLURM_JOB_ID}"
FAILED_TMP="$OUTBOX/FAILED-${SLURM_JOB_ID}.partial"
if ! mkdir -m 700 "$NODE_ROOT"; then
  printf 'compute root already exists\n' >&2
  exit 66
fi
OWNER_MARKER="$NODE_ROOT/.phase7-owner"
owner_token="${SLURM_JOB_ID}:${SOURCE_COMMIT}:$$"
failure_line=0
printf '%s\n' "$owner_token" > "$OWNER_MARKER"
chmod 400 "$OWNER_MARKER"
# shellcheck disable=SC2329
cleanup_node_root() {
  marker_value=""
  if [[ -d "$NODE_ROOT" ]] && [[ ! -L "$NODE_ROOT" ]] &&
     [[ -f "$OWNER_MARKER" ]] && [[ ! -L "$OWNER_MARKER" ]] &&
     IFS= read -r marker_value < "$OWNER_MARKER" && [[ "$marker_value" == "$owner_token" ]]; then
    rm -rf -- "$NODE_ROOT"
  fi
}
# shellcheck disable=SC2329
record_failure_and_cleanup() {
  status=$?
  trap - EXIT
  if [[ "$status" -ne 0 ]] && [[ -d "$OUTBOX" ]] && [[ ! -L "$OUTBOX" ]] &&
     [[ ! -e "$FAILED" ]] && [[ ! -L "$FAILED" ]] && [[ ! -e "$FAILED_TMP" ]] && [[ ! -L "$FAILED_TMP" ]]; then
    {
      printf 'job_id=%s\n' "$SLURM_JOB_ID"
      printf 'source_commit=%s\n' "$SOURCE_COMMIT"
      printf 'exit_code=%s\n' "$status"
      printf 'failure_line=%s\n' "$failure_line"
    } > "$FAILED_TMP"
    chmod 400 "$FAILED_TMP"
    mv -- "$FAILED_TMP" "$FAILED"
  fi
  cleanup_node_root
  exit "$status"
}
trap 'failure_line=$LINENO' ERR
trap record_failure_and_cleanup EXIT

mkdir -m 700 "$INPUT"
if [[ -e "$OUTBOX" ]] || [[ -L "$OUTBOX" ]]; then
  test -d "$OUTBOX" && test ! -L "$OUTBOX"
else
  mkdir "$OUTBOX"
fi
chmod 700 "$NODE_ROOT" "$INPUT" "$OUTBOX"
test ! -e "$ACK" && test ! -L "$ACK"
test ! -e "$FAILED" && test ! -L "$FAILED" && test ! -e "$FAILED_TMP" && test ! -L "$FAILED_TMP"
cp -a "$STAGE/input/." "$INPUT/"
if find "$INPUT" -type l -print -quit | grep -q .; then
  printf 'copied input contains a symlink\n' >&2
  exit 65
fi

expected_payload_files=(
  SOURCE_COMMIT
  BINARY_METADATA.txt
  artifacts/agent-python-runtime-numpy-core.wasm
  artifacts/manifest.json
  bin/apyrun-benchmark-linux-amd64
  phase7_slurm_job.sh
  source.bundle
)
expected_payload_manifest="$NODE_ROOT/payload.expected"
: > "$expected_payload_manifest"
for relative_path in "${expected_payload_files[@]}"; do
  candidate="$INPUT/$relative_path"
  test -f "$candidate" && test ! -L "$candidate"
  printf '%s  %s\n' "$(sha256sum "$candidate" | cut -d' ' -f1)" "$relative_path" >> "$expected_payload_manifest"
done
actual_file_count="$(find "$INPUT" -type f -print | wc -l | tr -d '[:space:]')"
test "$actual_file_count" -eq "$((${#expected_payload_files[@]} + 1))"
cmp -- "$expected_payload_manifest" "$INPUT/payload.SHA256"
(cd "$INPUT" && sha256sum --check payload.SHA256)
test "$(tr -d '\r\n' < "$INPUT/SOURCE_COMMIT")" = "$SOURCE_COMMIT"
grep -Fx $'\tbuild\tvcs.revision='"$SOURCE_COMMIT" "$INPUT/BINARY_METADATA.txt"
grep -Fx $'\tbuild\tvcs.modified=false' "$INPUT/BINARY_METADATA.txt"

git clone "$INPUT/source.bundle" "$REPO" >/dev/null 2>&1
test "$(git -C "$REPO" rev-parse HEAD)" = "$SOURCE_COMMIT"
test -z "$(git -C "$REPO" status --porcelain=v1 --untracked-files=all)"
cmp -- "$INPUT/phase7_slurm_job.sh" "$REPO/tools/phase7_slurm_job.sh"

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
    *) exit 70 ;;
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
    -child-timeout 12m \
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

tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner \
  -C "$NODE_ROOT" -czf "$archive_tmp" result ENVIRONMENT.txt RUN_COMPLETE
archive_sha="$(sha256sum "$archive_tmp" | cut -d' ' -f1)"
printf '%s  %s\n' "$archive_sha" "$(basename "$archive")" > "$checksum_tmp"
mv "$archive_tmp" "$archive"
mv "$checksum_tmp" "$checksum"
printf '%s  %s\n' "$archive_sha" "$(basename "$archive")" > "$ready_tmp"
mv "$ready_tmp" "$ready"

for _ in $(seq 1 180); do
  ack_value=""
  if [[ -f "$ACK" ]] && [[ ! -L "$ACK" ]] &&
     IFS= read -r ack_value < "$ACK" && [[ "$(wc -l < "$ACK")" -eq 1 ]] && [[ "$ack_value" == "$archive_sha" ]]; then
    printf '%s  %s\n' "$archive_sha" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$acked_tmp"
    ln "$acked_tmp" "$acked"
    rm "$acked_tmp"
    exit 0
  fi
  sleep 10
done
printf 'ACK timeout for %s\n' "$archive_sha" >&2
exit 72
