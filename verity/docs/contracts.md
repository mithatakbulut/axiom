# Initial domain contracts

Contracts are JSON-compatible Go records with explicit `schema_version`.
Version 1 is the first on-disk/wire representation; incompatible changes
require a new version and migration strategy.

Domain IDs use type `domain.ID` with a typed prefix and 128-bit random
suffix (for example, `wf_<32 lowercase hex digits>`). Current prefixes are
`prj`, `wf`, `task`, `cand`, `pol`, and `evt`.

## Task contract

Version 1 is scoped to a workflow and includes a stable task ID, title,
description, acceptance criteria, dependency task IDs and bounded
execution policy (`max_attempts`, timeout). Dependencies must form a DAG
and refer to tasks in the same workflow. Acceptance criteria have stable
IDs and descriptions; an agent completion claim cannot verify them.

## Execution record

Version 1 records an execution ID, workflow/task IDs, attempt number,
provider identifier, start/finish timestamps, outcome and candidate
revision when one exists. Output may reference stored artifact IDs but
must not contain credentials. Outcomes distinguish success, failure,
timeout, cancellation, blocked and unknown. An unknown external side-effect
outcome requires reconciliation before retry.

Phase 3 Go contracts live in `internal/execution`. Each request has a
positive attempt number, workspace, timeout and full base SHA. A successful
result must include a full resulting candidate SHA; another outcome may
include a candidate only when one is known. Runtime capabilities are queried before invocation,
and `Run` applies the request timeout and parent cancellation. The original `Tracker` remains in-memory for non-durable callers. `RunDurable` commits a durable invocation intent before runtime execution, then persists the observed result; errors without an authoritative outcome are recorded UNKNOWN. `ManualRuntime` emits a handoff through its
configured sink and returns `BLOCKED` with no candidate; a later human result
must be recorded separately and is never treated as verification.

The handoff package is versioned JSON with execution identity, base SHA,
workspace and bounded instruction text. Git workspaces are detached at the
recorded base commit. Cleanup requires the adjacent ownership marker, Git
common-directory match and clean status; otherwise it refuses removal.

## Vigil bridge

Bridge link schema version 1 records Verity workflow/task/attempt/correlation
IDs, full base/candidate SHAs, an explicit Vigil session ID, execution-only
status, normalized signals and compact budget/advisor facts. Links are
append-only in Verity's database and are included in proof export only when
both workflow and candidate SHA match. The linked session ID is supplied by
the runtime/operator because current Vigil public interfaces do not accept
Verity correlation metadata or expose a replayable session event cursor.

## Process Control

The workflow `State` and `Event` values in
`internal/process/workflow/state.go` define the deterministic transition
contract. `Transition` is pure. SQLite commits each accepted state update
and its sequenced event in one transaction. Events include actor, timestamp,
correlation ID and reason; database triggers reject update and delete.

## Verification

`verification.Run` contains a schema version, workflow/task IDs, a full
candidate SHA/digest, profile, aggregate state, acceptance criteria, review
record and check observations. Check outcomes are `PASS`, `FAIL`, `BLOCKED`,
`SKIPPED` or `TIMED_OUT`; aggregate states are `PASS`, `FAIL`, `BLOCKED`,
`NEEDS_HUMAN`, `STALE`, `PARTIAL` or `CANCELLED`. Validation rejects missing
identity, unsupported schema versions, mutable/short candidate references,
duplicate checks and unknown outcomes. A missing, unavailable, skipped,
timed-out or optional-only set of checks can never produce `PASS`.

The version 1 project manifest lives at `.verity/verification.yaml`. Check
commands are displayed as argv in an immutable candidate-pinned plan and
require explicit approval. String commands are split into argv words only;
YAML sequences support arguments containing spaces. No shell interpolation
is performed. Commands run in a disposable Git archive snapshot with bounded
timeouts/output and a reduced environment. Acceptance criteria map to check
IDs; every criterion needs a passing required check. A result becomes stale
when the candidate changes. If a configured current-candidate lookup fails,
verification returns an error instead of accepting a potentially stale result.
Advisory reviewer output cannot accept a result; a required review remains
`NEEDS_HUMAN` until an explicit decision includes a nonempty human actor.
The lifecycle obtains this decision separately through its `HumanReview`
dependency; the caller is responsible for authenticating the actor. Existing
review records without an actor require a new attributed decision before
they can pass reevaluation.
Playwright scenarios use the same bounded runner and become blocked if the
Playwright executable is unavailable.

## Proof

`proof.Evidence` stores provenance, content SHA-256, and byte size. Artifact
bytes are stored separately in the content-addressed local artifact store
and rehashed on retrieval. Evidence, verification runs and append-only proof
audit events are persisted in SQLite. Required check outputs can be stored
and attached to each verification observation before saving the run. A
version 1 proof bundle binds the verification run, evidence and artifact
references, optional contract payload, and audit history to one workflow
and candidate. Each evidence record must have exactly one matching artifact
reference; verification reads and hashes every referenced artifact. Export
sorts collections for reproducible JSON and rejects missing evidence for
required passing checks. Retention policies provide
validated age limits and explicit text redaction; applying retention or
redaction is left to a caller and does not mutate existing proof records.
Records are not cryptographically signed.

## Task and execution examples

Phase 2 process definitions use schema version 1 and retain an immutable
versioned snapshot in each run. Definition budgets bound task count, each
task's attempts, and total scheduling/state transitions. Dependencies must
refer to tasks in the same definition and form a DAG. A task includes stable
identity, description, acceptance criteria, timeout, bounded attempts,
dependency IDs and an optional approval-node marker. Run snapshots persist
task states and attempts; scheduler leases are owner-bound and expire.
Only explicit task completion advances dependents. Approval nodes stop the
run until an explicit approval or rejection. The built-in feature, bugfix,
and documentation templates are deterministic examples, not agent prompts.

The `control.Run` methods reject invalid transitions, retries beyond the
task bound, and schedules beyond the run transition budget. Persisted updates
use compare-and-swap on the transition count. No runtime execution contract
is introduced in Phase 2.

```json
{
  "schema_version": 1,
  "workflow_id": "wf_example",
  "task_id": "task_example",
  "title": "Implement the feature",
  "description": "Deliver the requested behavior",
  "acceptance_criteria": [
    {"id": "criterion_1", "description": "Expected behavior is verified"}
  ],
  "depends_on": [],
  "execution_policy": {"max_attempts": 2, "timeout_seconds": 900}
}
```

```json
{
  "schema_version": 1,
  "execution_id": "exec_example",
  "workflow_id": "wf_example",
  "task_id": "task_example",
  "attempt": 1,
  "provider": "manual",
  "outcome": "SUCCEEDED",
  "candidate_sha": "0123456789abcdef0123456789abcdef01234567"
}
```


## GitHub and preview integrations (Phase 7)

GitHub webhook deliveries are identified by the provider delivery ID and event
type, signature-checked against the raw request body, and normalized to
repository, pull number, head/base SHA, ref, or Actions run identity. Supported
webhook families are `pull_request`, `push`, and `workflow_run`. Pull workflow
associations are keyed by repository, PR number, and full head SHA. A changed
head SHA creates a separate association; consumers must invalidate earlier
verification for that pull request. Duplicate provider delivery IDs and
repeated association attempts do not create duplicate work.

Published Check Runs carry the exact full candidate SHA and stable external ID.
The caller supplies an explicit GitHub conclusion derived from validated
Verity results; the integration never infers success from free-form output.
PR summaries carry the same SHA and can include a proof URL. GitHub Actions run
queries require a full SHA; artifact archives are bounded and digest checked
when GitHub returns a digest. Netlify preview build requests are tied to the
requested SHA through a configured SHA-to-branch resolver and must return the
same `commit_ref`; preview URLs require HTTPS and may be host allowlisted.


## Durable execution attempts (schema 1)

SQLite stores the immutable request/provider intent and append-only lifecycle
events. States are RUNNING, UNKNOWN, FINISHED and ABANDONED. On restart, RUNNING
becomes UNKNOWN; no process or workspace inspection is used to guess an outcome.
An operator reconciliation requires actor and reason, may cite an evidence
reference, and can only record an execution outcome. Unknown attempts block reuse
of the task attempt number until reconciliation or explicit abandonment; retries use the next sequential attempt number.
Duplicate identical results/reconciliations are idempotent; conflicting decisions
are rejected. Candidate SHAs are validated as full immutable revisions.
