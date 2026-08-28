import os
import pathlib
import subprocess
import tarfile
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
VERIFIER = ROOT / "scripts" / "verify-source-identity.sh"


class SourceIdentityVerifierTests(unittest.TestCase):
    def make_repository(self, root: pathlib.Path) -> tuple[str, str, str]:
        subprocess.run(["git", "init", "-q", root], check=True)
        subprocess.run(["git", "-C", root, "config", "user.email", "test@example.com"], check=True)
        subprocess.run(["git", "-C", root, "config", "user.name", "Test"], check=True)
        (root / "source.txt").write_text("original source\n")
        ignored = root / ".hermes/plans/tracked.md"
        ignored.parent.mkdir(parents=True)
        ignored.write_text("tracked despite global ignore\n")
        subprocess.run(["git", "-C", root, "add", "-Af", "source.txt", ".hermes/plans/tracked.md"], check=True)
        subprocess.run(["git", "-C", root, "commit", "-q", "-m", "fixture"], check=True)
        commit = subprocess.check_output(["git", "-C", root, "rev-parse", "HEAD"], text=True).strip()
        tree = subprocess.check_output(["git", "-C", root, "rev-parse", "HEAD^{tree}"], text=True).strip()
        epoch = subprocess.check_output(["git", "-C", root, "show", "-s", "--format=%ct", "HEAD"], text=True).strip()
        return commit, tree, epoch

    def test_archive_tree_is_bound_and_mutation_is_rejected(self) -> None:
        self.assertTrue(VERIFIER.exists(), "source identity verifier is missing")
        with tempfile.TemporaryDirectory() as raw:
            base = pathlib.Path(raw)
            repository = base / "repository"
            archive = base / "source.tar"
            extracted = base / "extracted"
            extracted.mkdir()
            commit, tree, epoch = self.make_repository(repository)
            with archive.open("wb") as output:
                subprocess.run(["git", "-C", repository, "archive", "--format=tar", "HEAD"], check=True, stdout=output)
            with tarfile.open(archive) as source:
                source.extractall(extracted)
            excludes = pathlib.Path(raw) / "global-excludes"
            excludes.write_text(".hermes/\n")
            gitconfig = pathlib.Path(raw) / "global-gitconfig"
            subprocess.run(["git", "config", "--file", gitconfig, "core.excludesfile", excludes], check=True)
            environment = os.environ | {"GIT_CONFIG_GLOBAL": str(gitconfig)}
            accepted = subprocess.run([VERIFIER, commit, tree, epoch], cwd=extracted, text=True, capture_output=True, env=environment)
            self.assertEqual(accepted.returncode, 0, accepted.stderr)
            (extracted / "source.txt").write_text("mutated source\n")
            rejected = subprocess.run([VERIFIER, commit, tree, epoch], cwd=extracted, text=True, capture_output=True, env=environment)
            self.assertNotEqual(rejected.returncode, 0)
            self.assertIn("source tree mismatch", rejected.stderr)

    def test_git_worktree_metadata_must_match(self) -> None:
        self.assertTrue(VERIFIER.exists(), "source identity verifier is missing")
        with tempfile.TemporaryDirectory() as raw:
            repository = pathlib.Path(raw) / "repository"
            commit, tree, epoch = self.make_repository(repository)
            accepted = subprocess.run([VERIFIER, commit, tree, epoch], cwd=repository, text=True, capture_output=True)
            self.assertEqual(accepted.returncode, 0, accepted.stderr)
            rejected = subprocess.run([VERIFIER, "0" * 40, tree, epoch], cwd=repository, text=True, capture_output=True)
            self.assertNotEqual(rejected.returncode, 0)
            self.assertIn("source commit mismatch", rejected.stderr)


if __name__ == "__main__":
    unittest.main()
