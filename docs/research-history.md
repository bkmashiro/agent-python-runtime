# Historical research

Pysolate began as a bounded CPython/WASI execution experiment and accumulated several independent research systems. They were useful for answering specific questions, but are not part of the active proof of concept.

The full implementations remain available in Git history at or before `03f05b80073228000fe573e3ee95df574e54b714`.

## Archived lines of work

- **Prepared and COW execution:** demonstrated that initialized guests can be prepared ahead of demand and measured Linux memory sharing, refill pressure and density. The active PoC uses a fresh guest for every run because lifecycle simplicity matters more than startup optimization.
- **Benchmark and scheduler campaigns:** exercised open-loop admission, memory pressure, lifecycle density and paired NumPy cells. These were measurement systems, not runtime requirements.
- **Transactional effect plane:** explored durable SQLite journals, approval, compensation, reconciliation and evidence export. The active PoC permits only bounded Host-owned calls and records them in memory.
- **Evaluation platform:** compared Direct, Python and Hybrid model treatments with datasets, scorers, routing and trace evidence. The active PoC proves only that ordinary Agent-authored Python can execute through Pysolate.
- **Hermes/MCP/trace integrations:** demonstrated daemon, MCP and trace correlation paths. The active integration boundary is the `apyrun` JSON stdin/stdout CLI.
- **Workspace verifier and evidence products:** produced release-style reports, replay fixtures and evidence bundles. Core isolation is now covered directly by focused unit and real-Guest integration tests.
- **NumPy/WASI qualification:** explored a larger scientific artifact profile. It remains historical; the active PoC targets a small safe standard-library profile.

## When to restore something

Restore an archived mechanism only when a real PoC workload is blocked without it. Start from the smallest implementation that resolves the observed blocker; do not revive an entire benchmark, schema or orchestration stack by default.
