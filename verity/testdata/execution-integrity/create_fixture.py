#!/usr/bin/env python3
"""Create an isolated, multi-commit execution-integrity Git fixture."""

import argparse
import shutil
import subprocess
from pathlib import Path


def run(root: Path, *args: str) -> None:
    subprocess.run(["git", *args], cwd=root, check=True, stdout=subprocess.DEVNULL)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("destination", type=Path)
    destination = parser.parse_args().destination.resolve()
    if destination.exists():
        raise SystemExit(f"refusing to overwrite {destination}")
    source = Path(__file__).parent / "repository"
    shutil.copytree(source, destination)
    run(destination, "init", "-q")
    run(destination, "config", "user.name", "Verity Fixture")
    run(destination, "config", "user.email", "verity-fixture@example.invalid")
    run(destination, "add", "README.md", "src", "tests", "playwright", "malicious")
    run(destination, "commit", "-qm", "fixture baseline")
    (destination / "history.txt").write_text("second commit\n", encoding="utf-8")
    run(destination, "add", "history.txt")
    run(destination, "commit", "-qm", "fixture history")
    (destination / "staged.txt").write_text("staged content\n", encoding="utf-8")
    run(destination, "add", "staged.txt")
    (destination / "README.md").write_text(
        (destination / "README.md").read_text(encoding="utf-8") + "\nunstaged source change\n",
        encoding="utf-8")
    (destination / "unstaged.txt").write_text("unstaged content\n", encoding="utf-8")
    (destination / "untracked.txt").write_text("untracked content\n", encoding="utf-8")
    print(destination)


if __name__ == "__main__":
    main()
