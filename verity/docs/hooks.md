# Local Git verification hooks

Verity's Git hooks are opt-in developer feedback. They do not replace
required CI, branch protection, or repository rules because a developer can
skip them with `git commit --no-verify` or disable local hooks.

## Setup

Configure `.verity/verification.yaml` with a non-empty `pre-commit` profile.
The profile uses the same argv-only checks, bounded timeouts, disposable
staged-tree snapshots, and evidence recording as `verity verify index`.
Then install the hooks from the initialized project:

```sh
verity hooks install
verity hooks status
```

Installation respects the repository's `core.hooksPath` and supports linked
worktrees. It does not modify Git configuration. Existing hooks cause an error;
use `verity hooks install --chain` to preserve and run them first. The wrapper
propagates a prior hook's nonzero exit status. Verity refuses symbolic-link
hooks and refuses removal after the managed hook has drifted.

The pre-commit hook captures the current index tree and runs the configured
`pre-commit` profile. A missing profile, unavailable check, failed check, stale
index, or any aggregate state other than `PASS` blocks the commit. No files are
staged, unstaged, reset, or edited by the hook.

The post-commit hook compares the actual `HEAD^{tree}` and repository identity
with the most recent passing pre-commit result. It appends a proof audit event
only for an exact match. A mismatch is reported without rejecting or changing
the commit; the index-candidate proof remains available without a commit
binding.

Remove the hooks with:

```sh
verity hooks uninstall
```

The previous chained hook is restored with its original bytes and mode.
`verity hooks status` reports each managed hook and whether its path remains
active. Hook state is stored under `.verity`; do not copy that state to a
different repository.

## Trust and isolation

The hook trusts the local Verity config and verification manifest. Candidate
Git trees containing those control files are rejected, and the active config
bytes are bound into verification approval. Checks currently run as the local
Verity process with a reduced environment and local-process isolation; they
are not OS sandboxed. Do not use this mode to execute untrusted pull request
code unattended. Phase 3 of the execution integrity plan covers runtime
isolation.
