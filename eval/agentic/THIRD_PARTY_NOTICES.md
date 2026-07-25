# Third-party notices for `eval/agentic/v1`

The normalized tasks under `eval/agentic/v1/tasks/` are adapted from the
**Berkeley Function Calling Leaderboard (BFCL) v4** in the Gorilla repository:

- Upstream: <https://github.com/ShishirPatil/gorilla>
- Pinned revision: `6ea57973c7a6097fd7c5915698c54c17c5b1b6c8`
- License: Apache License 2.0
- Bundled license copy: `eval/agentic/LICENSE-BFCL.txt`
- Upstream license: <https://github.com/ShishirPatil/gorilla/blob/6ea57973c7a6097fd7c5915698c54c17c5b1b6c8/LICENSE>

Changes made by this repository:

1. selected a deterministic subset before any model execution;
2. excluded tasks with credentials, network access, destructive deletion, or real-world effects;
3. normalized non-standard schema type aliases (`dict` to `object`, `float` to `number`);
4. wrapped source questions, tool definitions, initial state, and expected call traces in the repository's `external-agentic-task/v1` envelope;
5. assigned a local development/evaluation split. This split is not an official BFCL split and must not be reported as an official BFCL leaderboard result.

The manifest records SHA-256 digests of every upstream source file and every
normalized task. The import script refuses a source checkout whose Git revision
differs from the pinned revision.
