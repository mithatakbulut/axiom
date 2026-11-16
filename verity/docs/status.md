# Verity status

## 2026-11-16 — Existing Codex chat workflow

The `chat` CLI can create and progress a feature, bugfix or documentation
workflow through explicit agent calls. The `chat verify` command snapshots
selected source, runs the configured checks, saves evidence and advances the
verification task only on PASS; the feature approval gate remains human.
The project root instructions direct Codex to use these commands for
substantive changes, without launching a second Codex process. Calls are
agent-reported and not authenticated to a Codex session; they can be skipped
and do not make hook observations authoritative. No Verity MCP server exists.
The project manifest checks the workflow control and tracker units only.

Vigil's obsolete executable registration was removed and reinstalled at the
user's explicit approval, pointing to the existing workspace executable. Its
status reports observe mode, not paused, and database integrity OK; Codex MCP
lists it enabled. Hook review/trust and real hook delivery in a new session
have not yet been verified.

## Electron application removed

The Axiom Electron menu-bar application, its packaged builds and its desktop
test manifest were removed at the user's request. The dashboard CLI contract
and the Codex observation plugin remain available independently. Historical
worklog entries below describe the former application, not current features.

## 2026-11-16 — Local verification without Git

The CLI now has `verify snapshot`, `verify plan`, and `verify run` for an
explicit set of local source paths. Snapshots are content-addressed archives;
the digest is checked before unpacking into a disposable directory. The
approval digest binds the workflow, source identity, manifest, and plan.
Required check output is recorded as evidence and the verification is saved.
The previously configured desktop-test manifest and captured desktop source
snapshot have been removed with the application. Historical workflow and
verification records remain in the project database.

`verity work run` now starts a new Codex CLI turn, stores durable invocation
intent and the observed result, captures selected source paths, then runs
independent verification before completing its two-step workflow. A real
Codex turn and verification passed in a temporary project and again in the
Axiom workspace with a no-edit connectivity task and desktop tests. Uncertain
runtime results and non-passing checks pause the workflow. The configured model must
be available to the selected Codex account. This manages new CLI-started jobs;
it does not automatically adopt existing desktop chats, infer arbitrary
semantic substeps, or establish real Vigil session correlation.

Snapshots require explicit source selection and exclude `.git`, `.verity`,
`node_modules`, and `dist`. They do not represent files omitted from the
selection, and a result does not establish that a later workspace still
matches the captured snapshot. Project checks execute with the Verity user's
OS permissions. A general dynamic workflow planner,
recovery UI, and independently attributable Vigil correlation remain
unfinished.

Current milestone: Phase 10 complete. Verity persists execution intent and append-only attempt events, recovers interrupted attempts as UNKNOWN, and supports explicit reconciliation/abandonment. Phase 9 Vigil references remain optional and cannot establish execution completion.

Post-plan hardening: configured candidate lookup failures now stop verification;
proof bundles require one artifact reference per evidence record; invalid capture
cannot delete shared artifacts; and required human review uses a separate,
actor-attributed decision. Existing unattributed decisions need renewed review
when reevaluated. Actor authentication remains the caller's responsibility.

## Complete

- Initial architecture, accepted ADRs, threat model, execution boundaries, CLI direction, and compatibility notes.
- Phase 1 foundation: YAML config, structured logging, typed IDs, SQLite schema and transactional workflow events, and `init`, `doctor`, and `config validate` commands.
- Phase 2 Process Control: versioned definitions and immutable run snapshots; dependency DAG scheduling; pause/resume/cancel; approval nodes; bounded retries and transition budgets; expiring persisted scheduler leases; deterministic workflow driver and templates; workflow/task CLI inspection and creation.
- Phase 3 execution foundation: provider-neutral request/result/capability contracts; timeout and cancellation-aware invocation; fake and manual handoff providers; in-process attempt tracking; portable JSON handoff package writing; Git root/HEAD/status inspection; detached worktree creation and guarded cleanup.
- Phase 4 Verification: version 1 project manifest and deterministic profile plans; argv-only command and Playwright scenario checks; explicit plan approval; full-SHA pinning; disposable Git archive snapshots; bounded per-check timeout and output capture; restricted environment; acceptance criteria mapping; advisory review plus explicit human decision; stale invalidation; aggregate PASS, FAIL, BLOCKED, NEEDS_HUMAN, STALE, PARTIAL and CANCELLED states.
- Phase 5 Proof: content-addressed local artifacts with digest verification; evidence provenance; persisted verification runs, evidence, and append-only audit events; required-check evidence binding; candidate-bound proof bundle validation and deterministic JSON export; `proof show` and `proof export`; retention and redaction policy primitives.
- Phase 6 lifecycle: candidate implementation behind a bounded correction interface; candidate-pinned independent Verification and review; preview deployment bound to the verified source SHA; required preview smoke evidence; post-deploy verification contract; explicit human release approval; candidate-bound Proof bundle assembly. No production deployment adapter exists, and enabling production returns an error.
- Phase 8 release readiness: concurrent content-addressed artifact writes publish atomically; deterministic and failure-path coverage is recorded; artifact and SQLite evidence benchmarks are reproducible; install/configuration, compatibility, release, and upgrade procedures are documented; current-host CLI archives can be built with `make package VERSION=0.1.0`.
- Phase 9 Vigil bridge: inspected the actual Vigil 0.1.0 CLI, hooks, MCP tools and schemas; documented a compatibility matrix; added a versioned execution-only bridge record, stdio MCP client, and `verity vigil inspect` / `verity vigil attach` commands. Append-only session links are candidate-bound and included in proof exports. The bridge rejects incompatible servers and cannot infer execution completion or bypass Verity gates.
- Phase 10 durable execution attempts: `RunDurable` persists intent before provider invocation; SQLite schema 6 stores immutable attempt intent and append-only lifecycle events. Restart recovery and uncertain runtime errors produce `UNKNOWN`; explicit actor-attributed reconciliation or abandonment is required before the next sequential attempt. Candidate SHA and Vigil ownership boundaries are preserved.

## Not implemented yet

HTTP API, full workflow-trigger orchestration, distributed execution scheduling, and automatic Vigil/Codex launch/session discovery remain future work. Explicit link records can be attached by session ID but do not prove that session performed the referenced Verity attempt. Vigil exposes no public Verity correlation field, event cursor/export, completion callback, or general-purpose execution API. Phase 7 provides GitHub webhook/client and Netlify preview adapters. A configured signed pull-request delivery can create an initial workflow through the local receiver; hosting and forwarding remain application responsibilities. Playwright scenarios run only when a compatible `playwright` executable and project dependencies are available in the check environment; otherwise the check is recorded BLOCKED. Verification commands run with the Verity process user's OS permissions and are not containerized or otherwise OS-sandboxed. No Vigil files were changed.

Phase 1 acceptance evidence on 2026-11-16: `go test ./...`, `go vet ./...`, `go build ./...`, and CLI smoke runs for `init`, `config validate`, and `doctor` passed. Phase 2 acceptance evidence on 2026-11-16: `go test ./...`, `go vet ./...`, and `go build ./...` passed with a temporary GOCACHE. Phase 3 acceptance evidence on 2026-11-16: `GOCACHE=/private/tmp/verity-gocache go test ./...`, `GOCACHE=/private/tmp/verity-gocache go vet ./...`, and `GOCACHE=/private/tmp/verity-gocache go build ./...` passed. Phase 4 acceptance evidence on 2026-11-16: the same three commands passed. Focused tests cover manifest validation, explicit approval, snapshot isolation, command failure and timeout results, criteria completeness, required-check gating, human review, stale invalidation, and every aggregate result state.

Phase 5 acceptance evidence on 2026-11-16: focused proof and SQLite tests plus `GOCACHE=/private/tmp/verity-gocache go test ./...`, `go vet ./...`, and `go build ./...` passed. Proof tests cover digest verification/tampering, deduplication, required-check evidence, deterministic export, persistence, and append-only audit enforcement.

Phase 6 acceptance evidence on 2026-11-16: lifecycle end-to-end test creates a disposable Git feature repository, verifies a commit in an isolated snapshot, checks preview source-SHA binding, captures check and preview evidence, validates the proof bundle and artifact digests, and confirms production deployment remains unavailable. `GOCACHE=/private/tmp/verity-gocache go test ./...`, `go vet ./...`, and `go build ./...` passed.

Phase 7 acceptance evidence on 2026-11-16: focused GitHub webhook/client, preview adapter, and SQLite integration tests passed. Tests cover signature rejection, persisted delivery deduplication, PR workflow association idempotency across duplicate and changed SHAs, exact-SHA Check Run updates, visible permission failures, Netlify source-SHA binding, and HTTPS/host allowlisting. `GOCACHE=/private/tmp/verity-gocache go test ./...`, `go vet ./...`, and `go build ./...` passed.

Phase 8 acceptance evidence on 2026-11-16: uncached full test suite, vet, build, and focused race tests passed. The local benchmark sample and host details are recorded in `docs/verification-evaluation.md`. `make package VERSION=0.1.0` produced a darwin/arm64 archive and its binary reported `verity 0.1.0`. Release, install, compatibility, upgrade, recovery, and known limitation documentation is in `README.md`, `docs/compatibility.md`, `docs/release.md`, and `docs/verification-evaluation.md`.

Phase 9 acceptance evidence: MCP fixture tests exercise initialization, catalog validation, session-scoped calls, signal normalization and unsupported-version rejection; SQLite/proof tests cover immutable link persistence and exact workflow/candidate proof binding. The `inspect` and `attach` commands were smoke-tested against the built Vigil 0.1.0 executable using isolated temporary configurations. `GOCACHE=/private/tmp/verity-gocache go test ./...`, `go vet ./...`, `go build ./...`, and focused race tests passed. No Vigil files or user configuration were changed.


Phase 10 acceptance evidence on 2026-11-16: focused execution and SQLite tests cover durable intent-before-runtime invocation, persisted outcomes, restart recovery to UNKNOWN, idempotent recovery/result/reconciliation, conflict rejection, attempt-number uniqueness, candidate SHA validation, and append-only event enforcement. Full-module tests, vet, build, and focused race tests passed. SQLite migration 6 preserves existing records. Runtime errors without authoritative outcomes remain UNKNOWN; operator reconciliation/abandonment is required before the next sequential attempt can be created. Automatic Codex session discovery remains unavailable through Vigil's documented public interfaces.

## 2026-11-16 — Integration review

The local [Vigil example](integrations/local-example.md) was run from clean temporary storage against freshly built binaries. It creates a Verity workflow, feeds a synthetic Vigil hook, reads the MCP snapshot, and attaches a caller-supplied candidate-bound link. It does not verify an actual candidate or prove the claimed session/attempt relationship. Corrected the bridge command's argument separator. Full module tests passed before this documentation-only change. Full event-to-execution orchestration, a user-facing end-to-end driver and real execution correlation remain unimplemented; the existing webhook and lifecycle tests exercise components rather than a deployed integration. HTTP API, distributed scheduling and production deployment remain outside the demonstrated local use case and need explicit acceptance criteria before additional infrastructure is justified.

## 2026-11-16 — Atomic GitHub pull-request trigger

Added a signed, repository-scoped one-shot CLI receiver and exact-SHA association listing; see [GitHub delivery](integrations/github-delivery.md) and [decision](decisions/0011-github-atomic-pull-trigger.md). Delivery, definition, association and READY run commit together. Tests cover signatures, duplicate and new SHA deliveries, repository rejection and rollback on a run collision. This adds no webhook hosting, task execution, GitHub reporting, or real GitHub deployment. A previously recorded delivery without a matching association fails closed and needs operator reconciliation. Full lifecycle orchestration, independently verified proof from this trigger, and real Vigil session correlation remain open.

Validation: `GOCACHE=/private/tmp/verity-task-cache go test ./...`, `go test -race ./...`, `go vet ./...`, and `go build ./...` passed; the CLI association-list test also passed separately after it was added. `golangci-lint` is not available on PATH. No live GitHub delivery was sent.

## Menu bar dashboard work

The read-only dashboard contract remains available; see `docs/dashboard-contract.md`. The former Electron consumer has been removed.

## 2026-11-16 — Execution attempt scope correction

Durable attempt numbers are now scoped to `(workflow_id, task_id)` rather than
the task ID alone. A RUNNING or UNKNOWN attempt blocks another attempt for that
same workflow and task. Migration 7 transactionally copies existing attempt
and event history into tables with the corrected uniqueness constraint. The
read-only dashboard accepts schema 7. A migration test opens a populated old
schema and checks retained history, independent workflow attempts, and retry
blocking. Back up the database before upgrading; older binaries do not support
the new schema contract.

The [local Vigil example](integrations/local-example.md) was rerun with freshly
built binaries: the synthetic hook was visible through MCP and the supplied
link was stored. It still does not demonstrate actual agent execution or
candidate verification. GitHub-to-execution-to-proof orchestration remains
unimplemented; this correction does not complete it. No live GitHub event or
advisor was used.

Validation: focused SQLite tests, both modules' `go test ./...`,
`go test -race ./...`, and `go vet ./...`, plus Verity `go build ./...`, passed
with temporary Go caches. Git metadata is absent, so no Git diff was possible.

Codex observation prototype: a local tracker plugin records minimal hook
metadata in the existing default project database and the Codex panel shows
matching historical signals with unknown state. The `verity-local` marketplace
and tracker plugin were installed locally; Codex hook trust remains necessary.
This is partial coverage, not automatic workflow registration or reliable live
status. See `codex-plugin/README.md` and `docs/decisions/codex-observation-arena.md`.

## 2026-11-16 — Codex turn observation follow-up

The tracker now maintains one minimal observation per session and turn in a
separate local store without requiring a project database. The former Axiom
consumer displayed matching turns and reported store read failure.
Neither hook delivery nor current status is guaranteed; these records are not
Process Control workflows. The installed plugin cache matches this revision. Hook trust must be reviewed
and actual delivery verified before this version can be considered operational. Prior
project database observations are preserved but not migrated. See
`codex-plugin/README.md`. Real desktop hook delivery remains unverified.

Validation for the follow-up: tracker unit tests (including concurrent writes)
and 13 desktop tests passed. The unsigned arm64 app and ZIP were rebuilt using
an existing Electron cache. `open` returned success outside the sandbox, but
computer-use app inspection timed out, so a visible window and real hook event
were not verified. An initial sandboxed package attempt failed because network
DNS was unavailable; the cached build succeeded. No Vigil files were changed.
