# Common agent Python use cases

The `agent-core` profile targets frequent, bounded scripting work rather than a general Python distribution. Its supported high-value workflows are qualified against the real Wasm artifact.

## Repository and configuration maintenance

A workspace Run starts with `/workspace` as both the current directory and the first Python import root. Scripts can use relative paths and import local modules from the private copy without learning a Host backing path.

Qualified operations include:

- recursive file selection with `pathlib`, `glob` and `fnmatch`;
- Python inspection with `ast` and `tokenize`;
- review output with `difflib` and SHA-256;
- TOML and INI reads through `tomllib` and `configparser`;
- safe YAML reads and writes through pinned pure-Python PyYAML;
- deterministic workspace snapshots and added/modified/deleted diffs.

File publication back to a source repository remains an explicit Host operation. A Guest error preserves private changes for inspection but does not imply rollback.

## Structured-data transformation

The profile qualifies:

- JSON and JSONL;
- CSV;
- TOML, INI and safe YAML;
- URL query parsing and Base64;
- `collections`, `itertools`, `statistics`, `decimal` and `fractions`;
- NumPy arrays, aggregation, sorting, filtering and core linear algebra;
- JSON, CSV, text and Markdown workspace output.

This covers small and medium data-cleaning, benchmark-result processing, report generation and numerical preprocessing without adding Pandas or SciPy.

## Host-enriched scripts

External I/O stays behind named Host tools. A script can call a generated function such as `market.get_prices(...)`, normalize the returned JSON with Python/NumPy, and write reviewed results to its private workspace. Tool catalogs can change without rebuilding the Guest artifact.

The executable acceptance backs the market-data tool with a real local HTTP request and verifies exactly one Host request. The older workspace acceptance also covers Host HTTP, parameterized read-only SQLite and an idempotent external write.

```sh
go run ./examples/agent-core-usecases -guest dist/pysolate.wasm
python3 tools/probe-guest-modules.py --guest dist/pysolate.wasm --output /tmp/agent-core-qualification.json
```

## Deliberate exclusions

The current profile does not claim:

- Guest networking or `requests`;
- subprocesses, `pip` or runtime package installation;
- Guest SQLite;
- Pandas, SciPy, Pillow or other heavy native packages;
- compressed gzip/bzip2/lzma or ZIP-deflate support;
- a complete IANA timezone database.

Those capabilities either belong on the Host boundary or require native/runtime weight disproportionate to the frequent use cases above. Uncompressed TAR and stored ZIP may work through the standard library, but they are not part of the qualified contract.
