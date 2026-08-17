# Pysolate Lab Web

Static viewer with three build-pinned surfaces:

- **Mechanisms** — eight compact runtime-mechanism examples backed by accepted evidence.
- **Inspector** — the restored execution-list/selected-operation debugger layout over one real `dev-release-readiness` Guest run. It exposes source, event metadata, full verified public agent outputs, workspace artifacts, and task context.
- **Timeline** — the same Host and Guest events on an independent shared clock.

Generate projections with `npm run data:latest` and `npm run data:task`. `data:task` verifies an `experiment_full` private trajectory in `labstore` before emitting the explicit public fixture projection. Pass `--recording-root` to `project-task` to retain the private recording rather than using its temporary default.

Verify with `npm test -- --run`, `npm run build`, and `npm run test:e2e`.

The browser accepts only `pysolate.lab-latest.v2` and `pysolate.lab-task.v2`. Evidence identities are verified but not presented as accomplishments.
