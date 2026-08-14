import pathlib
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]


class GuestBuildWorkingDirectoryTests(unittest.TestCase):
    def test_guest_build_is_independent_of_calling_working_directory(self):
        script = (ROOT / "guest" / "build" / "build-guest.sh").read_text(encoding="utf-8")
        self.assertIn('source_date_epoch.py" --repository "${ROOT_DIR}" HEAD', script)
        self.assertIn('GITHUB_SHA=$(git -C "${ROOT_DIR}" rev-parse HEAD)', script)
        self.assertIn("export GITHUB_SHA", script)


if __name__ == "__main__":
    unittest.main()
