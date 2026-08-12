import { canonicalDocuments } from "./data.mjs";

const digests = (index) => Object.fromEntries([
  ["index", index.source_sha256],
  ...index.links.map(({ rel, kind, sha256 }) => [({
    study: "study",
    run: "run",
    timeline: "timeline",
    branch: "branch",
    workspace: "workspace",
    comparison: "comparison",
    problem: "problem",
    reference: "objectRef",
  })[rel] ?? kind, sha256]),
]);

const makeFixture = (documents) => Object.freeze({
  ...documents,
  __sha256: digests(documents.index),
});

export const canonicalByState = Object.freeze(Object.fromEntries(
  Object.entries(canonicalDocuments).map(([state, documents]) => [state, makeFixture(documents)]),
));
export const CANONICAL_STATES = Object.freeze(Object.keys(canonicalByState));
