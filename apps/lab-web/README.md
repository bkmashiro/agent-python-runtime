# Pysolate Lab Web

Static viewer with three build-pinned surfaces:

- **Mechanisms** — eight compact runtime-mechanism examples backed by accepted evidence. Mechanisms 01 and 02 use the day-trip travel capabilities and their own matched measurements.
- **Inspector** — the reviewed `pysolate.public-day-trip.v1` projection of two candidate Agents, two fresh Guest attempts, six delayed Host API observations, the main Agent selection, and final itinerary. Public task/system/skill/tool/workspace inputs are separated from explicitly withheld private provider fields.
- **Timeline** — the historical `dev-release-readiness` Host/Guest recording on an independent shared clock, clearly labelled as legacy rather than the day-trip trace.

Generate the mechanism projection with `npm run data:latest`. Re-project a reviewed private day-trip result with `scripts/project-lab-day-trip.py`; the script validates the expected candidate, Guest observation, branch, and final-output closure before writing `public/lab-data/day-trip.json`. `npm run data:task` remains available for the legacy timeline fixture and verifies its `experiment_full` private trajectory before emitting the public projection.

Verify with `npm test`, `npm run build`, and `npm run test:e2e`.

The browser accepts `pysolate.lab-latest.v2`, `pysolate.public-day-trip.v1`, and the legacy `pysolate.lab-task.v2` timeline schema through separate strict decoders. Evidence identities are verified but not presented as accomplishments.
