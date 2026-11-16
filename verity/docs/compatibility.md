# Compatibility and supported providers

## v0.1.0 support

- Go module: Go 1.23 language floor. SQLite is provided by
  `github.com/mattn/go-sqlite3`; builds require CGO and a C toolchain.
- Local persistence: SQLite in WAL mode, one process/connection at a time.
- GitHub: selected pull request, push, and Actions webhook normalization;
  exact-SHA Check Runs and PR summaries; paginated Actions run/artifact
  metadata and bounded artifact download. An application-supplied event
  handler is still required to start workflows and invalidate stale results.
- Preview: Netlify deployment adapter with source-SHA matching and HTTPS URL
  policy.
- Browser verification: optional Playwright executable and project/browser
  dependencies must be installed in the check environment. Missing tools
  produce BLOCKED.
- Runtime: fake and manual handoff adapters. No general-purpose coding agent
  runtime is launched by this release.
- Deployment: preview lifecycle contracts exist; production deployment is
  intentionally unavailable.

Only local single-node use is in scope. Concurrent multi-user/team service,
Windows/Linux provider validation, signed release archives, managed artifact
retention, OS-level command sandboxing, and automated workflow trigger
orchestration are not supported in v0.1.0.

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
