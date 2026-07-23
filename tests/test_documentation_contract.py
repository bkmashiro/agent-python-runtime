import pathlib
import re
import unittest
from urllib.parse import unquote

ROOT = pathlib.Path(__file__).resolve().parents[1]
LINK_PATTERN = re.compile(r"(?<!!)\[[^\]]*\]\(([^)]+)\)")


class PublicDocumentationContractTests(unittest.TestCase):
    def read(self, relative_path: str) -> str:
        return (ROOT / relative_path).read_text()

    def test_public_status_does_not_restore_retired_future_claims(self):
        documents = "\n".join(
            self.read(path)
            for path in (
                "README.md",
                "NOTICE.md",
                "docs/architecture.md",
                "docs/threat-model.md",
                "docs/prepared-state-audit.md",
                "docs/supply-chain.md",
                "docs/adr/0001-runtime-boundaries.md",
                "docs/adr/0002-guest-abi-v1.md",
                "docs/adr/0003-artifact-provenance.md",
                "docs/adr/0006-execution-session-lifecycle.md",
            )
        )
        for stale in (
            "NumPy, prepared snapshots",
            "ready/first/steady benchmark is still pending",
            "eventual single CLI entry",
            "runtime/snapshot/        prepared-state capture/reset",
            "return healthy instance to pool",
            "including NumPy and the Host-side wazero dependency",
            "Accepted direction; identifiers freeze when fixtures and real artifact tests pass",
            "Snapshot capture occurs only after",
            "active NumPy artifact-profile and final-hardening tracks",
            "active frontier is truthful closeout",
            "inactive until the current truthful-closeout Track G finishes",
            "active final-hardening/truthful-closeout Track G",
        ):
            with self.subTest(stale=stale):
                self.assertNotIn(stale, documents)

    def test_public_docs_preserve_profile_and_release_boundaries(self):
        readme = self.read("README.md")
        supply_chain = self.read("docs/supply-chain.md")
        threat_model = self.read("docs/threat-model.md")
        provenance = self.read("docs/adr/0003-artifact-provenance.md")

        self.assertIn("manual-only `numpy-core`", readme)
        self.assertIn("not released or deployed", readme)
        self.assertIn("No served instance is reset, restored, or returned to the pool", threat_model)
        self.assertIn("`numpy-core` deliberately does not write that production-safe index", supply_chain)
        self.assertIn("lock-derived bundled-package versions", provenance)

    def test_local_markdown_links_resolve(self):
        failures = []
        for document in ROOT.rglob("*.md"):
            if ".git" in document.parts:
                continue
            for raw_target in LINK_PATTERN.findall(document.read_text()):
                target = raw_target.split()[0].strip("<>")
                if target.startswith(("#", "http://", "https://", "mailto:")):
                    continue
                relative = unquote(target.split("#", 1)[0])
                if relative and not (document.parent / relative).resolve().exists():
                    failures.append(f"{document.relative_to(ROOT)} -> {target}")
        self.assertEqual([], failures)

    def test_closed_execution_roadmaps_have_no_unchecked_items(self):
        closed_roadmaps = {
            "docs/plans/2026-07-22-agent-python-runtime-autonomous-megagoal.md": "No unchecked executable item remains",
            "docs/plans/2026-07-23-agent-python-session-lifecycle-autonomous-megagoal.md": "**No active implementation pointer.**",
        }
        for path, closeout_marker in closed_roadmaps.items():
            with self.subTest(path=path):
                roadmap = self.read(path)
                self.assertIsNone(re.search(r"^- \[ \]", roadmap, flags=re.MULTILINE))
                self.assertIn(closeout_marker, roadmap)

        session_roadmap = self.read(
            "docs/plans/2026-07-23-agent-python-session-lifecycle-autonomous-megagoal.md"
        )
        self.assertIn("- [deferred]", session_roadmap)
        self.assertNotIn("This is the active `/goal`", session_roadmap)
        self.assertNotIn("session-lifecycle successor is now active", self.read("README.md"))


if __name__ == "__main__":
    unittest.main()
