# Compatibility and supported providers

## v0.1.0 support

- Go module: Go 1.23 language floor. SQLite is provided by
  `github.com/mattn/go-sqlite3`; builds require CGO and a C toolchain.
- Local persistence: SQLite in WAL mode, one process/connection at a time.
- GitHub: selected pull request, push, and Actions webhook normalization;
  exact-SHA Check Runs and PR summaries; paginated Actions run/artifact
  metadata and bounded artifact download. A scoped signed PR receiver and
  SQLite worker can verify exact PR heads when a trusted rootless-container
  policy and pinned image are configured. GitHub App scoped token lifecycle,
  Enterprise remote fetching, hosted proof URLs, push/Actions workflows and live-hosted
  validation remain unsupported.
- Preview: Netlify deployment adapter with source-SHA matching and HTTPS URL
  policy.
- Browser verification: optional Playwright executable and project/browser
  dependencies must be installed in the check environment. Missing tools
  produce BLOCKED.
- Runtime: `work run` and persistent plans can launch a bounded Codex CLI
  turn; fake and manual handoff adapters remain available for deterministic
  workflows. Codex CLI availability and account/model access are required for
  real provider execution.
- Deployment: preview lifecycle contracts exist; production deployment is
  intentionally unavailable.

Only local single-node use is in scope. Concurrent multi-user/team service,
Windows/Linux provider validation, signed release archives, managed artifact
retention, and real Linux container validation are not yet supported in
v0.1.0. The GitHub worker is an initial self-hosted integration, not a
production deployment claim.

## Local environment observed

- Go 1.26.5, darwin/arm64, `CGO_ENABLED=1`.
- Git 2.50.1.
- Node.js 26.5.0 and `npx` are present.
- Playwright is not installed in this workspace; no package lock or
  browser dependency was added.
- The Verity Go contracts currently use only the standard library.

The `go.mod` language floor is Go 1.23. CI should include the minimum
supported Go version and the current stable Go version before release.

## Git worktree lifecycle probe

Ran an isolated probe in a disposable repository under `/private/tmp`:

1. Initialized a repository and an empty initial commit.
2. Added a detached worktree at the same commit.
3. Confirmed both worktrees resolve to the same commit and the added path
   appears in `git worktree list --porcelain`.
4. Removed the owned, clean worktree and confirmed it no longer appears.

Result: add/list/remove passed on Git 2.50.1. The probe does not validate
dirty-worktree safeguards, concurrent operations, crash recovery or
Windows/Linux behavior; those belong in the workspace implementation
milestone. Verity is not currently a Git checkout, so the probe used a
throwaway repository and did not change the user's Git state.

## Playwright evaluation

The official Playwright documentation supports CI execution after installing
the npm package and matching browser binaries. Playwright Test writes
screenshots, videos and traces under its configured output directory; these
files can be ingested by Verity's artifact adapter. Browser binaries are
version-coupled to Playwright, so the adapter must pin the package and
install matching browsers in its execution image. Keep the Go core free of
a Playwright dependency; invoke it through a bounded browser-check adapter
in the verification milestone. Playwright is optional per project.

References: [Playwright CI](https://playwright.dev/docs/ci), [test artifacts configuration](https://playwright.dev/docs/test-use-options), [browser installation](https://playwright.dev/docs/browsers).

## Existing Vigil public boundary

Inspection was limited to Vigil's README, CLI command surface and stdio
MCP implementation. The external surfaces are its CLI and MCP JSON-RPC
protocol (`initialize`, `tools/list`, `tools/call`) with published
`vigil_*` tools. No stable Verity bridge contract or Go library API is
declared. Vigil's SQLite database is internal; Verity must not read or
write it. Any later bridge must be a separate, versioned adapter using an
explicitly supported public surface and must remain isolated until the
final roadmap phase. No Vigil files were changed.


## Durable execution records

Schema migration 6 adds `execution_attempts` and append-only
`execution_attempt_events`; existing SQLite data is preserved. Startup recovery
marks in-flight attempts UNKNOWN and never automatically repeats them. Operators
must reconcile or abandon an unknown attempt before reusing its task attempt
number. This is local single-process recovery; it does not provide distributed
leases or guarantee that an external runtime stopped after Verity exited.

## Typed candidate records

New durable attempts use record schema version 2 and contain a versioned typed
base candidate. Successful results, verification schema version 2, proof
bundle schema version 2, and Verity-owned Vigil link schema version 2 carry a
typed candidate identity. Existing pre-versioned durable attempts are read as
schema version 1; schema-version-1 verification records and proof bundles
remain readable. The JSON-body upgrade does not rewrite SQLite rows.

Schema migration 8 adds the durable GitHub pull-request job queue. Migration 7
remains the execution-attempt scope correction. Migration 9 preserves queued
jobs while allowing an authorized manual trigger to create a new run for a
previously verified PR head. Migration 10 adds an append-only audit record for
the authenticated actor and manual trigger. Dashboard readers accept schema
10. Older binaries do not understand queued GitHub work or its multiple-run-
per-head history and must not open a database after migration 10 has been
applied.
GitHub Enterprise Git remotes are available through an explicit
operator-supplied HTTPS base URL. GitHub App authentication discovers the
installation for the worker's configured repository and refreshes scoped
installation tokens; manual retrigger remains PAT-only.
