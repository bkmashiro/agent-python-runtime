import assert from "node:assert/strict";
import test from "node:test";
import { canonicalByState, CANONICAL_STATES } from "../src/canonical/index.mjs";
import { validateCanonicalFixture } from "../src/lib/canonical-adapter.mjs";

for (const state of CANONICAL_STATES) {
  test(`${state} is the Go-produced canonical Lab v1 fixture`, () => {
    assert.strictEqual(validateCanonicalFixture(canonicalByState[state], state), canonicalByState[state]);
    assert.equal(canonicalByState[state].index.schema_version, "pysolate.lab-index.v1");
    assert.equal(canonicalByState[state].run.schema_version, "pysolate.run-detail.v1");
  });
}

test("adapter fails closed on draft-shaped or canonical-looking impostors", () => {
  const fixture = structuredClone(canonicalByState.ordinary);
  fixture.run = { schema_version: "pysolate.lab-draft.v0", fixture_only: true };
  assert.throws(() => validateCanonicalFixture(fixture, "draft"), TypeError);
});

test("adapter fails closed on digest/link drift", () => {
  const fixture = structuredClone(canonicalByState.branched);
  fixture.__sha256.branch = "sha256:0000000000000000000000000000000000000000000000000000000000000000";
  assert.throws(() => validateCanonicalFixture(fixture, "drift"), /link does not match/);
});

test("adapter rejects protected/private body-shaped fields", () => {
  const fixture = structuredClone(canonicalByState.private);
  fixture.run.protected_body = "must never appear";
  assert.throws(() => validateCanonicalFixture(fixture, "protected"), TypeError);
});
