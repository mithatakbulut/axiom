import os
import time
import unittest
from pathlib import Path

from src.app import answer


class FixtureTests(unittest.TestCase):
    def test_baseline_passes(self):
        self.assertEqual(answer(), 42)

    @unittest.skipUnless(os.getenv("VERITY_FIXTURE_INCLUDE_FAIL"), "opt-in failure scenario")
    def test_deterministic_failure(self):
        self.fail("deterministic fixture failure")

    @unittest.skipUnless(os.getenv("VERITY_FIXTURE_INCLUDE_SLOW"), "opt-in slow scenario")
    def test_bounded_slow_case(self):
        time.sleep(2)
        self.assertEqual(answer(), 42)

    @unittest.skipUnless(os.getenv("VERITY_FIXTURE_ARTIFACT_DIR"), "opt-in artifact scenario")
    def test_bounded_artifact(self):
        root = Path(os.environ["VERITY_FIXTURE_ARTIFACT_DIR"])
        root.mkdir(parents=True, exist_ok=True)
        artifact = root / "fixture.txt"
        artifact.write_text("fixture artifact\n", encoding="utf-8")
        self.assertLessEqual(artifact.stat().st_size, 1024)


if __name__ == "__main__":
    unittest.main()
