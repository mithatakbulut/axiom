# Publication preparation

This workspace contains local state and experimental material. The ignore files
are safeguards for a new Git repository; they do not remove tracked files,
rewrite history, protect archives or prevent `git add -f`. Do not publish this
workspace wholesale. Keep the original records and work on a separate disposable
copy when preparing a release.

1. Decide the repository layout, license and copyright holder first. Obtain
   permission for third-party material where required.
2. Populate a fresh staging directory with only reviewed source, tests,
   configuration and documentation. Exclude `.verity`, build outputs, caches,
   OS metadata and `vigil/docs/evidence` by default. Review all other files,
   especially `vigil/docs/evaluation.md`, `vigil/docs/WORKLOG.md` and
   `vigil/docs/status.md`, for identifying details, session data, credentials,
   paths and third-party excerpts. Excluding evidence can leave links unresolved;
   remove or replace those links in the staged copy, preserving the originals.
3. Review every staged file and any generated archive, including hidden files.
   Run a secret scanner as an aid, manually assess matches and inspect for
   personal information and third-party rights; a clean scan is not proof.
   Test fixtures may contain intentionally fake credentials: verify each match
   in context before narrowly suppressing it, never disable whole categories.
4. If importing existing Git history, review all reachable history and artifacts
   separately; ignore rules only affect untracked files. Do not publish history
   until reviewed. Check the staged file list and diffs again before release.

The root CI workflow runs both modules in the chosen single-repository layout.
The existing Vigil workflow is retained for standalone module use; GitHub only
discovers workflows at the repository root. The workspace is currently supplied
without Git metadata, so its history and tracked-file status cannot be audited here.
