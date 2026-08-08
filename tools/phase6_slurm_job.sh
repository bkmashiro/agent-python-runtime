#!/bin/bash
#SBATCH --partition=t4
#SBATCH --gres=gpu:tesla_t4:1
#SBATCH --cpus-per-task=4
#SBATCH --mem=16G
#SBATCH --time=04:00:00
#SBATCH --job-name=pysolate-p6
#SBATCH --export=NIL

set -euo pipefail
umask 077
export PATH=/usr/local/bin:/usr/bin:/bin

if [[ $# -ne 3 ]]; then
  printf 'usage: %s {canary|small|formal} /vol/bitbucket/ys25/STAGE SOURCE_COMMIT\n' "$0" >&2
  exit 64
fi
TIER="$1"
STAGE="$2"
SOURCE_COMMIT="$3"
case "$TIER" in
  canary|small|formal) ;;
  *) printf 'invalid tier: %s\n' "$TIER" >&2; exit 64 ;;
esac
if [[ ! "$STAGE" =~ ^/vol/bitbucket/ys25/[A-Za-z0-9._-]+$ ]]; then
  printf 'unsafe stage path\n' >&2
  exit 64
fi
if [[ ! "$SOURCE_COMMIT" =~ ^[0-9a-f]{40}$ ]]; then
  printf 'invalid source commit\n' >&2
  exit 64
fi
test "${SLURM_JOB_PARTITION:-}" = "t4"
test -d "$STAGE"
test ! -L "$STAGE"
test -d "$STAGE/input"
test ! -L "$STAGE/input"
if find "$STAGE/input" -type l -print -quit | grep -q .; then
  printf 'symlinked staged input is forbidden\n' >&2
  exit 65
fi

NODE_ROOT="${SLURM_TMPDIR:-/tmp}/pysolate-p6-${SLURM_JOB_ID}"
INPUT="$NODE_ROOT/input"
REPO="$NODE_ROOT/repo"
RESULT="$NODE_ROOT/result"
OUTBOX="$STAGE/outbox"
ACK="$STAGE/ACK-${SLURM_JOB_ID}"

trap 'rm -rf -- "$NODE_ROOT"' EXIT

mkdir -p "$INPUT"
if [[ -e "$OUTBOX" ]] || [[ -L "$OUTBOX" ]]; then
  test -d "$OUTBOX"
  test ! -L "$OUTBOX"
else
  mkdir "$OUTBOX"
fi
chmod 700 "$NODE_ROOT" "$INPUT" "$OUTBOX"
test ! -e "$ACK" && test ! -L "$ACK"
cp -a "$STAGE/input/." "$INPUT/"
if find "$INPUT" -type l -print -quit | grep -q .; then
  printf 'copied input contains a symlink\n' >&2
  exit 65
fi
(
  cd "$INPUT"
  sha256sum --check payload.SHA256
)
test "$(tr -d '\r\n' < "$INPUT/SOURCE_COMMIT")" = "$SOURCE_COMMIT"

git clone "$INPUT/source.bundle" "$REPO" >/dev/null 2>&1
test "$(git -C "$REPO" rev-parse HEAD)" = "$SOURCE_COMMIT"
test -z "$(git -C "$REPO" status --porcelain=v1 --untracked-files=all)"
cmp -- "$0" "$REPO/tools/phase6_slurm_job.sh"

extra_args=()
formal_selection_sha256=none
if [[ "$TIER" = "formal" ]]; then
  test -f "$INPUT/formal-selection.json"
  test ! -L "$INPUT/formal-selection.json"
  formal_selection_sha256="$(sha256sum "$INPUT/formal-selection.json" | cut -d' ' -f1)"
  extra_args=(--formal-selection "$INPUT/formal-selection.json")
fi

{
  printf 'job_id=%s\n' "$SLURM_JOB_ID"
  printf 'tier=%s\n' "$TIER"
  printf 'source_commit=%s\n' "$SOURCE_COMMIT"
  printf 'hostname=%s\n' "$(hostname)"
  printf 'kernel=%s\n' "$(uname -srmo)"
  printf 'cpus_per_task=%s\n' "${SLURM_CPUS_PER_TASK:-unknown}"
  printf 'job_partition=%s\n' "${SLURM_JOB_PARTITION:-unknown}"
  printf 'job_gpus=%s\n' "${SLURM_JOB_GPUS:-unknown}"
  printf 'formal_selection_sha256=%s\n' "$formal_selection_sha256"
  printf 'started_utc=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
} > "$NODE_ROOT/ENVIRONMENT.txt"

python3 "$REPO/tools/phase6_matrix.py" run \
  --tier "$TIER" \
  --repo "$REPO" \
  --binary "$INPUT/bin/apyrun-benchmark-linux-amd64" \
  --artifact "$INPUT/artifacts/agent-python-runtime-numpy-core.wasm" \
  --artifact-manifest "$INPUT/artifacts/manifest.json" \
  --output-dir "$RESULT" \
  --memory-budget-bytes 8589934592 \
  --memory-reserve-bytes 2147483648 \
  --max-cpu 4 \
  --greed 50 \
  "${extra_args[@]}"

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
