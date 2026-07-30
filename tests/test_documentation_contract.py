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

    def test_public_docs_preserve_profile_and_license_boundaries(self):
        readme = self.read("README.md")
        license_text = self.read("LICENSE")
        supply_chain = self.read("docs/supply-chain.md")
        threat_model = self.read("docs/threat-model.md")
        provenance = self.read("docs/adr/0003-artifact-provenance.md")

        self.assertIn("[MIT](LICENSE)", readme)
        self.assertTrue(license_text.startswith("MIT License\n"))
        self.assertIn("Copyright (c) 2026 Yuzhe Shi", license_text)
        self.assertIn("No served instance is reset, restored, or returned to the pool", threat_model)
        self.assertIn("`numpy-core` deliberately does not write that production-safe index", supply_chain)
        self.assertIn("lock-derived bundled-package versions", provenance)

    def test_local_markdown_links_resolve(self):
        failures = []
        for document in ROOT.rglob("*.md"):
            if any(part in {".git", ".artifacts-private", ".hermes"} for part in document.parts):
                continue
            for raw_target in LINK_PATTERN.findall(document.read_text()):
                target = raw_target.split()[0].strip("<>")
                if target.startswith(("#", "http://", "https://", "mailto:")):
                    continue
                relative = unquote(target.split("#", 1)[0])
                if relative and not (document.parent / relative).resolve().exists():
                    failures.append(f"{document.relative_to(ROOT)} -> {target}")
        self.assertEqual([], failures)

    def test_internal_agent_plans_are_not_public_docs(self):
        self.assertFalse((ROOT / "docs/plans").exists())
        public_documents = [ROOT / "README.md", *sorted((ROOT / "docs").rglob("*.md"))]
        forbidden = (
            "/Users/" + "yuzhe",
            "For Hermes",
            "autonomous-megagoal",
            "Autonomous Mega-Goal",
        )
        for document in public_documents:
            text = document.read_text()
            for marker in forbidden:
                with self.subTest(document=document.relative_to(ROOT), marker=marker):
                    self.assertNotIn(marker, text)

    def test_transactional_workflow_docs_share_authority_vocabulary(self):
        effect_plane = self.read("docs/effect-plane.md")
        adr = self.read("docs/adr/0007-mcp-transactional-tool-workflows.md")

        for policy in (
            "DENY",
            "AUTO_COMMIT",
            "AGENT_COMMIT_REQUIRED",
            "USER_APPROVAL_REQUIRED",
        ):
            with self.subTest(policy=policy):
                self.assertIn(policy, effect_plane)
                self.assertIn(policy, adr)

        for effect_class in (
            "read_only",
            "reversible",
            "compensatable",
            "irreversible",
        ):
            with self.subTest(effect_class=effect_class):
                self.assertIn(effect_class, adr)

        self.assertIn("same Python run", effect_plane)


if __name__ == "__main__":
    unittest.main()
