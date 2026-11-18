# Execution integrity fixture

Run `python3 create_fixture.py /tmp/verity-integrity-fixture` to materialize a
small disposable Git repository. The generator creates multiple commits, then
leaves staged, unstaged, and untracked changes so index and candidate tests can
exercise the distinctions without mutating this source tree.

Inside the generated repository:

- `python3 -m unittest discover -s tests` runs the passing baseline.
- `VERITY_FIXTURE_INCLUDE_FAIL=1 python3 -m unittest discover -s tests` includes
  a deterministic failure.
- `VERITY_FIXTURE_INCLUDE_SLOW=1 python3 -m unittest discover -s tests` includes
  a bounded slow test.
- `VERITY_FIXTURE_ARTIFACT_DIR=/tmp/artifacts python3 -m unittest discover -s tests`
  creates a deterministic small artifact.
- `playwright/smoke.spec.js` is an optional browser smoke test for environments
  that provide Playwright and its browser dependencies.
- `malicious/probe.py` probes protected-path writes, loopback and external
  network access, credential environment variables, and the Docker socket. It
  exits unsuccessfully if protected access is available. Run it only through
  the opt-in rootless Docker integration test on a disposable Linux host; never
  run it directly on the host.

The fixture contains source scenarios only. It does not imply that a real
container runtime, GitHub installation, or browser environment has passed its
own integration gates.
