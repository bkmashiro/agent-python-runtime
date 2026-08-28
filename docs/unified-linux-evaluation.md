# Unified Linux evaluation suite

The dissertation evaluation lanes run from one clean `agent-python-runtime`
commit on one Linux/x86-64 host. The suite does not consume Shimmy benchmark
results.

## Lanes

1. **PLM economics** runs the one-read control and four-read treatment with the
   same freshly built base Guest.
2. **Prepared Family economics** compares private copy with Linux private COW
   for the same 8 MiB NumPy input and four single-use consumers. Every treatment
   and repetition runs in a fresh test subprocess. The report records family
   preparation, consumer creation and execution, process proportional set size,
   and private dirty memory.
3. **Producer sharing** runs the fixed 20-program authority campaign with the
   same base Guest used by PLM.

Prepared Family is not a reset benchmark. It does not support the historical
Shimmy reset-time or dirty-rate-crossover claims.

## ICL gpu31

From a clean worktree:

```bash
scripts/test-host-workstation.sh \
  --suite evaluation \
  --output /absolute/local/evidence-directory
```

The wrapper archives the exact clean HEAD, stages it through `shell2`, runs the
allowlisted worker on `gpu31.doc.ic.ac.uk`, retrieves the bounded evidence, and
removes only that run's remote stage and output directories. The worker reuses
the approved shared Go toolchain and Guest build cache under
`/vol/bitbucket/ys25/pysolate`.

## Any Linux/x86-64 host

The inner suite can run directly when its source identity is supplied:

```bash
scripts/run-linux-evaluation-suite.sh \
  --output /absolute/evidence-directory \
  --source-commit "$COMMIT" \
  --source-tree "$TREE" \
  --source-epoch "$EPOCH" \
  --runs 5 \
  --fanout 4 \
  --build-cache-root /absolute/build-cache
```

The output directory must be absent or empty. Existing evidence is never
overwritten.

## Output

The suite retains:

- `platform.json`
- `plm/one-read.json`
- `plm/four-read.json`
- `prepared-family/economics.json`
- `producer/private/` and `producer/public.json`
- `suite-manifest.json`

The manifest binds the platform, source commit and tree, both Guest artifact
hashes, lane parameters, evidence hashes, and headline metrics. It is written
only after every lane passes its own schema, source, artifact, and semantic
checks.
