# Axiom --- Execution Integrity Plan

> **Purpose:** Move Axiom from a strong set of workflow, execution,
> verification, proof, and observation components to a reliably
> integrated engineering control plane.
>
> **Status:** Core lifecycle implemented and locally verified for macOS trusted-repository use. Live Vigil detection/advisor evaluation, desktop GUI/Stop acceptance, and managed real-hook correlation remain open; see [local acceptance](macos-local-acceptance.md). Untrusted PR execution, Linux isolation, live GitHub hosting, and release automation are out of scope for this target.\
> **Repository baseline reviewed:** `mithatakbulut/axiom`, `main`, 18
> November 2026\
> **Primary owners:** Verity (workflow, execution control, verification,
> proof); Vigil (agent observation, intervention signals, advisor)

------------------------------------------------------------------------

## 1. Executive direction

Axiom should make one promise:

**No work is considered accepted merely because an agent says it is
done. Axiom must identify the exact candidate, run the required checks
against that candidate, preserve the evidence, and make the resulting
decision inspectable.**

The next stage is not another architectural layer. It is closing the
gaps between the layers that already exist:

1.  A developer or agent creates a candidate.
2.  Process Control records what is supposed to happen.
3.  Execution runs against an isolated, identifiable workspace.
4.  Verification checks the exact candidate---not a nearby or later
    workspace state.
5.  Proof preserves the provenance and integrity of the result.
6.  Integrations publish the decision against the same candidate.
7.  Vigil contributes observation signals without becoming an authority
    for completion or acceptance.

This plan deliberately keeps the existing ownership boundary:

  -----------------------------------------------------------------------
  Component               Owns                    Must not become
  ----------------------- ----------------------- -----------------------
  **Verity / Process      Workflow state, task    An agent-observation
  Control**               scheduling, approvals,  database
                          retry policy, lifecycle
                          decisions

  **Verity / Execution**  Provider invocation,    A source of acceptance
                          workspace ownership,    decisions
                          durable attempts and
                          recovery

  **Verity /              Candidate identity,     An agent's
  Verification**          check plans, execution  self-reported success
                          of checks, aggregate
                          result

  **Verity / Proof**      Evidence provenance,    A claim that hashes
                          artifact integrity,     alone establish
                          proof bundle assembly   authenticity

  **Vigil**               Hooks, session/turn     The authority for task
                          observations, Stop and  completion,
                          advisor signals         verification, or
                                                  approval

  **Integrations**        GitHub and              Business logic hidden
                          preview-provider        inside provider
                          translation and         callbacks
                          delivery
  -----------------------------------------------------------------------

**Architectural rule:** preserve the current layers and contracts
wherever possible. Extend them through explicit, versioned interfaces.
Do not add a new top-level layer unless implementation proves that an
existing owner cannot reasonably own the responsibility.

## 2. Current repository baseline

This plan starts from the repository as it exists, not from an imagined
greenfield implementation.

### 2.1 Already implemented

  -----------------------------------------------------------------------
  Area                                Current capability
  ----------------------------------- -----------------------------------
  Process Control                     Versioned workflow definitions and
                                      run snapshots, task DAGs,
                                      dependency scheduling, approval
                                      nodes, bounded retries and
                                      transition budgets, persisted state
                                      and events

  Execution                           Provider contracts,
                                      workspace/worktree support, local
                                      Codex CLI execution through managed
                                      work paths, durable invocation
                                      intent and append-only attempt
                                      events

  Recovery                            Interrupted or uncertain attempts
                                      become `UNKNOWN`; explicit
                                      actor-attributed reconciliation or
                                      abandonment is required before
                                      retry

  Verification                        Versioned manifest, deterministic
                                      check plans, explicit plan
                                      approval, command and Playwright
                                      check adapters, bounded execution,
                                      complete staged-index candidates,
                                      typed candidate identity, trusted
                                      policy snapshots, optional rootless
                                      no-network isolation for untrusted
                                      checks,
                                      aggregate states including `PASS`,
                                      `FAIL`, `BLOCKED`, `NEEDS_HUMAN`,
                                      `STALE`, `PARTIAL`, and `CANCELLED`

  Snapshotting                        Content-addressed selected-source,
                                      commit, and full Git-index snapshots;
                                      immutable materialization and
                                      candidate digest validation

  Proof                               Content-addressed artifacts,
                                      SHA-256 integrity checks, evidence
                                      provenance, persisted verification
                                      records, append-only audit events,
                                      deterministic JSON proof export

  Lifecycle                           Candidate-pinned verification and
                                      review contracts, preview
                                      source-SHA binding, preview smoke
                                      evidence contracts, explicit
                                      release approval; production
                                      deployment remains unavailable

  GitHub                              Webhook signature validation and
                                      delivery deduplication, PR/workflow
                                      associations keyed by exact head
                                      SHA, durable PR worker with exact
                                      head checks and proof-bound Check
                                      Runs, GitHub App token lifecycle,
                                      and signed local receiver

  Preview                             Netlify adapter with source-SHA
                                      matching and HTTPS URL policy

  Vigil bridge                        Optional Verity-owned MCP client,
                                      `inspect`, explicit `attach`, and
                                      exact-session/time-window
                                      correlation, append-only
                                      candidate-bound links

  Developer workflow                  Root and module agent instructions,
                                      Go tests, vet, formatting, builds,
                                      Python hook tests, minimum
                                      Go-version CI job
  -----------------------------------------------------------------------

### 2.2 Important limitations that remain

These are not reasons to redesign the project. They define the next
work.

-   The Git pre-commit gate is opt-in and local. It can be bypassed and
    does not enforce repository policy remotely.
-   `verity chat verify` is an explicit agent-reported action. It is not
    authenticated to a Codex session and can be skipped by an agent that
    ignores instructions.
-   `verity work run` and persistent plans can manage new Codex CLI
    work, but arbitrary existing Codex desktop chats are not
    automatically adopted.
-   GitHub service and worker components are self-hosted building blocks;
    live deployment, real GitHub delivery, and hosted proof downloads have
    not been demonstrated.
-   Trusted local verification commands run with the Verity process user's
    operating-system permissions. Untrusted checks require the separate
    Linux rootless, no-direct-network container adapter. Explicit destination
    allowlists are implemented through a loopback relay and host broker, but
    real runtime and malicious container tests have not run here.
-   Vigil correlation is observational and best-effort. It does not prove
    that Codex hooks delivered events or make Vigil authoritative for
    execution completion or approval.
-   Proof bundles have content integrity checks, but they are not
    cryptographically signed attestations.
-   Local Git hooks can improve developer feedback, but they cannot
    enforce repository policy by themselves because users can bypass
    them.

### 2.3 Existing documents and code to preserve as implementation entry points

-   `AGENTS.md`
-   `verity/AGENTS.md`
-   `verity/docs/architecture.md`
-   `verity/docs/contracts.md`
-   `verity/docs/status.md`
-   `verity/docs/compatibility.md`
-   `verity/docs/security.md`
-   `verity/docs/persistent-plans.md`
-   `verity/docs/integrations/github.md`
-   `verity/docs/integrations/github-delivery.md`
-   `verity/docs/decisions/0003-vigil-mcp-bridge.md`
-   `verity/docs/decisions/0004-durable-execution-attempts.md`
-   `verity/cmd/verity/chat.go`
-   `verity/cmd/verity/verify.go`
-   `verity/internal/verification/`
-   `verity/internal/execution/`
-   `verity/internal/proof/`
-   `verity/internal/integrations/github/`
-   `verity/internal/integrations/vigil/`
-   `vigil/`

Update existing status and decision documents as implementation
progresses. Do not leave this roadmap as the only source of truth.

------------------------------------------------------------------------

## 3. Product and engineering principles

All phases must follow these invariants.

### 3.1 Candidate identity is first-class

Every verification, execution result, proof bundle, and published status
must point to one immutable candidate identity.

A candidate may be:

-   A full Git commit SHA.
-   A Git index/tree candidate that has not yet been committed.
-   A content snapshot digest for non-Git or explicitly selected-source
    workflows.

These identities must be typed and distinguishable. Do not silently
treat a snapshot digest as a commit SHA.

### 3.2 Execution completion is not acceptance

-   `COMPLETED` means an execution attempt returned an authoritative
    completion result.
-   `PASS` means all required verification checks passed against the
    identified candidate and all required acceptance criteria are
    covered.
-   Human approval remains a separate, actor-attributed decision.
-   Vigil signals, agent summaries, logs, and runtime exit codes are not
    substitutes for verification.

### 3.3 Fail closed on uncertainty

The following must never become `PASS` by fallback:

-   Missing or unavailable required check.
-   Candidate mismatch or candidate mutation.
-   Timeout.
-   Incomplete evidence.
-   Unknown runtime outcome.
-   Failed proof integrity check.
-   Missing required human decision.
-   Stale PR head SHA.
-   Missing isolation capability when the selected policy requires it.

Use explicit states such as `BLOCKED`, `UNKNOWN`, `STALE`, and
`NEEDS_HUMAN`. Do not collapse them into generic failure or success.

### 3.4 No hidden trust in repository code

Repository content, workflow descriptions, check commands, runtime
output, browser content, and webhook payloads are untrusted inputs.

A manifest or check definition supplied by a PR must not be able to
weaken the trusted policy that decides whether that same PR passes.
Hosted verification must distinguish trusted policy from
candidate-controlled configuration.

### 3.5 Evidence must be attributable and reproducible

Record at minimum:

-   Candidate identity and candidate type.
-   Base revision where applicable.
-   Manifest and plan digests.
-   Check ID, exact argv, profile, start/end time, exit state, and
    bounded output artifact reference.
-   Execution and attempt IDs.
-   Relevant approval actor and reason.
-   Proof artifact digest and provenance.
-   Integration delivery ID and published Check Run ID, when applicable.

Do not store credentials or unnecessary sensitive output in evidence.

------------------------------------------------------------------------

# 4. Implementation roadmap

## Phase 0 --- Contract freeze and acceptance design

**Goal:** Make the candidate and trust model explicit before adding more
integration code.

### Work

1.  Review current contracts and record the new roadmap decisions in
    ADRs.
2.  Define a typed candidate contract with:
    -   `kind`: `git_commit`, `git_index`, or `content_snapshot`.
    -   Immutable candidate identifier.
    -   Repository identity where applicable.
    -   Base revision where applicable.
    -   Creation timestamp and capture provenance.
3.  Define how candidate identity flows through Execution, Verification,
    Proof, and integrations.
4.  Separate trusted verification policy from candidate-controlled
    project configuration.
5.  Define state mapping for local hooks, managed execution, GitHub
    Checks, and proof publication.
6.  Update `verity/docs/status.md` and `verity/docs/compatibility.md` to
    reflect the real baseline.

### Decisions required

-   What is the canonical representation of a staged index candidate?
-   Where does trusted verification policy live for a hosted PR
    workflow?
-   Which verification profiles are supported in the first release?
-   Which operating systems and container runtimes are supported
    initially?
-   Which events cause a new workflow, and which events only update an
    existing workflow?

Decisions for the current local/self-hosted release are recorded: the staged
candidate is the full tree from `git write-tree`, with repository common
directory and `HEAD` provenance; self-hosted PR workers load operator-local
policy outside the candidate; profiles remain manifest-selected; untrusted
container checks require rootless Docker on Linux; Windows and Podman are
unsupported pending validation. PR open/reopen/synchronize/ready events create
exact-head work, duplicate delivery is idempotent, close cancels queued work,
and push/Actions events do not enter the PR execution queue.

### Acceptance criteria

-   Candidate identity and state transitions are documented.
-   A verification result cannot be represented as passing without a
    candidate identity.
-   Trusted policy cannot be silently replaced by a candidate's modified
    manifest.
-   New contracts are versioned and migration behavior is documented.
-   Existing local workflows remain compatible or have an explicit
    migration.

### Phase 0 candidate contract progress recorded 2026-11-18

Added a shared versioned `candidate.Identity` for Git commits, Git index
trees, and selected-source snapshots. New durable execution attempts now
persist a typed base candidate and successful typed results; verification
records use schema version 2 and require a typed candidate, and new proof
bundles use schema version 2. New Verity-owned Vigil links use schema version
2 and bind a typed candidate. Existing schema-version-1 verification and
proof records remain readable; pre-versioned attempts are read as legacy
schema 1. Focused tests cover identity validation, cross-field mismatches,
durable persistence, and legacy reads. Phase 0 implementation acceptance is
complete for the local/self-hosted release boundary. A hosted multi-tenant
policy service is outside the product boundary.

------------------------------------------------------------------------

## Phase 1 --- Immutable Git candidate and staged-index verification

**Goal:** Verify exactly what will be committed, including staged
changes that have not yet become a commit.

This phase is the foundation for pre-commit verification.

### 1.1 Add a Git index candidate adapter

Create a Git-specific adapter under Verity's existing Git/workspace or
verification boundary. Do not put Git plumbing inside the pure domain
model.

The adapter must:

1.  Resolve the repository root and Git common directory safely.
2.  Read the staged index and create a canonical tree identity.
3.  Reject unmerged index entries and unresolved conflicts.
4.  Materialize a disposable candidate workspace from the index---not
    from the developer's working tree.
5.  Preserve relevant Git semantics such as executable bits, symlinks,
    and file paths.
6.  Detect unsupported or special entries, including submodules, and
    fail explicitly until their semantics are implemented.
7.  Never mutate the user's index, working tree, refs, or current
    branch.
8.  Re-check the index tree identity before returning a successful
    result.

Investigate Git plumbing such as `git write-tree` and a detached
temporary worktree or equivalent exact-tree materialization. Select the
implementation only after tests demonstrate that the materialized source
matches the index tree. Do not assume that a normal working-tree copy or
a path-limited snapshot is equivalent to the staged candidate.

### 1.2 Candidate semantics

The index tree represents the complete tree that a normal commit would
record---not merely the diff.

-   Staged tracked modifications are included.
-   Newly staged files are included.
-   Staged deletions are included.
-   Unstaged modifications are excluded.
-   Untracked, unstaged files are excluded.
-   The candidate identity is derived from the complete index tree.
-   The base `HEAD` is recorded separately for provenance and diff
    reporting.

If the index changes while verification is running, the result must
become `STALE` or be rejected. Never attach the result to a different
index tree.

### 1.3 Verification plan binding

Extend the current plan approval digest to bind:

-   Candidate kind and immutable identity.
-   Base revision.
-   Verification manifest digest.
-   Profile.
-   Exact check definitions and argv.
-   Acceptance criteria mapping.
-   Relevant isolation policy.

A plan approved for one index tree must not run against another tree.

### 1.4 CLI proposal

Exact command names can be finalized during implementation, but the
capability should be discoverable and scriptable. A possible surface:

``` sh
verity verify index --profile pre-commit
verity verify candidate <candidate-id> --profile standard
verity proof show <verification-id>
verity proof export <verification-id>
```

The hook itself should call an application service, not reimplement
verification logic in a shell script.

### 1.5 Tests

Cover:

-   Staged change with no commit.
-   Staged and unstaged edits to the same file.
-   Staged deletion.
-   Staged rename.
-   Newly staged file.
-   Untracked file excluded from the candidate.
-   Partial staging of a file.
-   File paths with spaces and non-ASCII characters.
-   Executable-bit and symlink changes.
-   Merge conflict / unmerged index.
-   Submodule and Git LFS behavior, with explicit supported or blocked
    outcomes.
-   Index mutation during verification.
-   Candidate snapshot tampering.
-   Interrupted capture and cleanup.

### Acceptance criteria

-   A test proves that verification reads staged bytes and not unstaged
    working-tree bytes.
-   The original repository index and working tree are byte-for-byte
    unchanged by capture and verification.
-   The same index tree produces the same candidate identity.
-   A changed index cannot reuse a prior passing result.
-   Every result identifies the candidate kind, tree identity, base
    revision, and manifest digest.

### Progress recorded 2026-11-18

The first usable Phase 1 slice now exposes `verity verify index plan|run`.
Its approval digest binds the workflow, `git_index` kind, complete staged
tree, base `HEAD`, repository common directory, manifest, profile, current
isolation policy, and planned checks. Run checks execute from staged Git blobs
in fresh disposable trees; an earlier check cannot change the source seen by
a later check. The engine checks the same index tree and base before and after
execution. Verification evidence and proof bundles retain the candidate kind,
tree ID, base, repository identity, manifest SHA-256, and isolation policy.
The active local config's exact bytes are bound into plan approval, and Git
candidates cannot contain the active config or verification manifest. Typed
candidate identity is now carried by new execution, verification, proof, and
Verity-owned Vigil bridge records; version-1 verification and proof records
remain readable.

Coverage now includes staged/unstaged edits, deletion, rename, staged and
untracked additions, partial staging, spaces and Unicode paths, executable
bits, symlinks, split indexes, linked worktrees, unresolved conflicts,
submodules, LFS pointers, index changes during verification, index preservation,
and cleanup after capture failure. Unsupported submodule/LFS/unmerged
states fail closed. Trusted policy is separated for Git candidates. Remaining
work is tracked in Phases 3–7.

### Phase 2 progress recorded 2026-11-18

Verity now provides opt-in `hooks install|status|uninstall` management. It
respects repository `core.hooksPath`, supports linked worktrees, refuses to
replace an existing hook unless `--chain` is explicit, preserves chained hook
bytes and mode, restores them on uninstall, and refuses to overwrite hook
drift. Installation does not alter Git configuration. The pre-commit command
executes the trusted `pre-commit` profile against the captured index tree,
records workflow/check evidence, and blocks on every non-PASS outcome. Tests
prove staged bytes are checked despite different unstaged contents, failure
blocks, and the original hook is preserved/restored.

The managed post-commit hook appends a proof audit binding only when the actual
commit tree, repository identity, profile, candidate kind, and passing result
all match. A mismatch is reported and remains non-blocking; it never changes
the already-created commit or claims proof for a different tree. Local hooks
remain bypassable with `git commit --no-verify` and are a developer feedback
mechanism, not a remote enforcement boundary. Isolation remains the existing
reduced-environment local process policy by default. Phase 3 now has an
explicit `trust_level: untrusted` manifest setting that requires a Linux
rootless Docker adapter with no network, a minimal container environment,
bounded CPU/memory/PID/tmpfs storage, and a digest-pinned image. A local runner
cannot execute an untrusted plan, and proof records its trust level and image.

An additional CLI integration test builds the real Verity executable, installs
both managed hooks in a disposable Git repository, and commits through Git's
actual hook invocation. A passing staged candidate commits and receives an
exact-tree post-commit audit binding; a failing staged candidate is rejected
and leaves `HEAD` unchanged.

Phase 3 now bounds each host-side candidate archive and extracted snapshot to
512 MiB and caps each snapshot at 50,000 entries; rootless container workspace
and temporary storage are separately bounded with tmpfs limits. A per-user
Unix file lock serializes snapshot/archive lifetimes across Verity processes
sharing the same temporary directory, bounding aggregate host-side staging to
one active snapshot and archive at a time.
The macOS-local Phase 3 acceptance is complete: trusted local checks use
the reduced-environment runner, immutable snapshots, bounded time/output,
and explicit approval. This does not provide OS sandboxing. The optional
Linux untrusted-code path remains unvalidated and out of scope. Exact host:port
allowlists now flow from the trusted manifest into the approval plan and run
proof. Direct container networking stays disabled; allowlisted CONNECT traffic
uses a loopback relay, a private host Unix socket, public-IP validation, and
direct IP dialing to prevent DNS rebinding to local/private ranges. For TLS,
the broker also requires the ClientHello SNI to match the CONNECT authority.
It does not decrypt TLS or inspect encrypted HTTP authority headers; endpoint
services remain responsible for application-level routing and authorization.

The host-side missing-runtime path is covered by a regression that forces
the container adapter to report `BLOCKED` without invoking a check. This
does not replace validation against a real Linux rootless runtime.

### Phase 3 host extraction quotas recorded 2026-11-18

Snapshot extraction and container archive creation enforce the 512 MiB source
and archive limits and a 50,000-entry cap before materializing candidate
files. Regular-file payload sizes are checked cumulatively and truncated
archive entries fail closed. A user-owned `flock` lease is held from before
`git archive` through verification cleanup, so concurrent Verity processes
using the same temp directory cannot multiply these host staging allocations.
The next lease removes only uniquely named Verity snapshot directories with a
matching owner marker and owned archive files; old or unmarked paths are left
untouched. Tests cover quota serialization, cancellation, cleanup, and
preserving unrelated temp directories. This does not replace Linux runtime or
malicious-container testing.

Verifier output is now redacted for common token, bearer, cloud-key, password,
private-key, and credential-bearing URL patterns before it is returned as
verification evidence. This is defense in depth; repository checks must not
receive provider credentials, and pattern-based redaction is not exhaustive.
Playwright base URLs containing URL user information or commonly named
credential query parameters are rejected by trusted manifest validation.
Browser scenarios now support an explicit, per-scenario host-environment
credential binding with an RFC3339 expiry and a maximum 10-minute exposure
window. The selected token is released only to that rootless Playwright check,
whose egress policy is narrowed to its HTTPS base host; missing or stale
credentials block before candidate materialization. This is controlled release,
not a general token issuer: test code can read the value, Verity does not
verify server-side scopes, and TLS path-level authorization remains the test
service's responsibility.
The fake-runtime suite also verifies the runner classifies a timed-out check
as `TIMED_OUT` and requests forced container removal. This exercises host-side
cleanup control flow only; a real daemon/runtime test remains required.

An opt-in Linux/rootless-Docker integration test executes the malicious
fixture probe and checks protected writes, symlink escape, external egress
denial, the isolated loopback-only interface, credential-named environment
variables, a host-home sentinel, runtime-socket absence, and the PID ceiling.
Docker's `none` network intentionally retains loopback inside the container,
so the probe checks local loopback operation instead of treating it as host
access. It is skipped in ordinary test runs and has not run in this macOS
environment.

### Phase 5 progress recorded 2026-11-18

GitHub intake now atomically persists normalized PR facts, exact-SHA
associations, workflow records, and durable queue jobs. Duplicate deliveries
are idempotent; newer heads supersede queued/running older jobs; close events
cancel outstanding jobs. Repository-scoped `github serve` and `github worker
once|loop` commands provide a self-hosted receiver/worker boundary. The worker
loads operator-local trusted policy, fetches the PR ref into a temporary bare
repository, checks the full SHA, requires the untrusted rootless-container
policy, records check-output evidence, validates the proof bundle and artifact
digests, rechecks the GitHub PR head, and publishes an exact-SHA Check Run.

An explicit `verity github retrigger owner/repo <pull-number>` command now
requires an authenticated user with repository write permission, resolves the
live head SHA through the GitHub API, and records the actor and delivery ID in
a fresh exact-SHA worker job. Duplicate delivery IDs are idempotent; a second
manual run is refused while the same-head job is pending or leased. SQLite
migration 9 preserves prior pull jobs while removing the one-job-per-head
constraint; migration 10 adds an append-only actor audit exposed in workflow
listing. The worker still rechecks the live head immediately before publication.

The worker now supports GitHub App installation discovery by configured
repository, RS256 app JWT creation, repository-scoped installation tokens,
and refresh before expiry. Personal access tokens remain supported, and
manual user-authorized retrigger remains PAT-only. Focused fake-transport
tests cover signing, claims, installation discovery, token scope, caching,
and refresh. Phase 5 is implemented with local integration coverage. Live GitHub deployment, Linux
container execution, and hosted proof URLs are excluded from the macOS-local target. GitHub Enterprise API and Git bases can be configured separately;
the worker requires an HTTPS Git base without embedded credentials and scopes
its fetch authorization header to that base path. Check Run output
refers to a local proof ID. Phase 3 now supports explicit network allowlists;
real Linux runtime tests and malicious runtime tests remain open. Aggregate host snapshot
staging is serialized per user and temporary directory under a cross-process
lease, with bounded per-snapshot size and entry count; it is no longer an
outstanding aggregate quota item. Phase 4 now gives each agent attempt a fresh owned
workspace based on the preceding immutable candidate, captures full plan
source paths, and safely removes only verified owned workspaces. The local
restart/failure matrix and lifecycle acceptance evidence are now covered by
the CLI, SQLite recovery, proof, and fixture tests. Live GitHub, Linux runtime,
and real provider observations remain separate release evidence.

The GitHub worker fixture now enters through an HMAC-signed webhook request,
then exercises durable queueing, verification, proof reference, and exact-SHA
Check Run publication using a fake GitHub transport. The stale-head path remains
covered. This is deterministic local end-to-end coverage, not a live GitHub or
rootless-container run.

------------------------------------------------------------------------

## Phase 2 --- Verity pre-commit verification gate

**Goal:** Make Axiom available at the exact moment a developer or coding
agent attempts to create a commit.

### 2.1 Hook behavior

Implement a managed Git `pre-commit` hook that invokes Verity's
staged-index verification service.

Expected flow:

1.  Git invokes the hook.
2.  The hook resolves the repository and Verity project configuration.
3.  Verity captures the staged index as an immutable candidate.
4.  Verity resolves the applicable trusted policy and `pre-commit`
    profile.
5.  Verity displays the planned checks and runs the required profile.
6.  Verity records check results and proof evidence.
7.  Only aggregate `PASS` returns exit code `0`.
8.  Any other result blocks the commit and prints a concise explanation
    plus a command for inspecting the proof.

Default blocking outcomes:

-   `FAIL`
-   `BLOCKED`
-   `TIMED_OUT`
-   `STALE`
-   `NEEDS_HUMAN`
-   `PARTIAL`
-   `CANCELLED`
-   `UNKNOWN`

A local hook must not silently turn an unavailable check into a pass. If
the project has no configured pre-commit profile, show a clear setup
error rather than pretending verification occurred.

### 2.2 Profile design

Keep commit-time checks fast and deterministic.

Suggested profile tiers:

  -----------------------------------------------------------------------
  Profile                 Intended use            Examples
  ----------------------- ----------------------- -----------------------
  `pre-commit`            Every commit            Formatting, lint,
                                                  focused unit tests,
                                                  static checks

  `pre-push`              Before sharing changes  Broader
                                                  unit/integration tests,
                                                  build

  `standard`              Managed task            Project's required
                          verification            baseline suite

  `full`                  CI or release candidate Full test suite,
                                                  browser scenarios,
                                                  expensive checks
  -----------------------------------------------------------------------

The repository chooses which checks belong to each profile. Do not
hardcode Go, React, or any framework assumptions into Verity.

The hook should have a configurable timeout and clear output. It should
not silently start a long-running agent or contact a hosted service.

### 2.3 Hook installation and lifecycle

Provide explicit commands, for example:

``` sh
verity hooks install
verity hooks status
verity hooks uninstall
```

Requirements:

-   Installation is opt-in and idempotent.
-   Detect `core.hooksPath` and existing hooks.
-   Never overwrite an existing hook without explicit consent.
-   Support a safe chain/wrapper mode that preserves the existing hook
    and its exit behavior.
-   Mark Axiom-managed files clearly.
-   Uninstall only files that Axiom owns; restore a preserved prior hook
    exactly.
-   Do not modify global Git configuration by default.
-   Work in linked worktrees and normal repositories.
-   Provide a diagnostic when Git hooks are disabled or redirected
    elsewhere.

### 2.4 Post-commit binding

A `post-commit` hook may bind a successful pre-commit verification to
the actual commit only when the commit's tree matches the verified index
tree.

If the actual commit tree does not match, preserve the verification as
an index-candidate result but do not claim that it proves the created
commit.

The post-commit hook is for recording/binding only; it cannot reject a
commit that has already been created.

### 2.5 Important limitation

Git's local `pre-commit` hook can be bypassed (for example with
`git commit --no-verify`) and is controlled by the developer. It is a
developer feedback and safety mechanism, not a security boundary or
organization-wide enforcement mechanism.

Remote policy must be enforced by required CI / GitHub Checks and branch
protection or repository rules.

### Phase 2 regression coverage recorded 2026-11-18

The hook manager has a focused regression proving that an existing
symbolic-link hook is rejected even with explicit chain mode, while both
the link and its target bytes remain unchanged. This covers safe refusal
for shared/external hook targets; it does not change the local hook's
bypassable status described above.

### Acceptance criteria

-   A successful staged candidate allows the commit.
-   Any non-PASS result blocks the commit.
-   The hook never stages, unstages, resets, or edits user files.
-   Existing hooks are preserved.
-   Installation and removal are safe and reversible.
-   `--no-verify` bypass is documented and does not create a false
    proof.
-   Post-commit binding checks the actual commit tree before associating
    proof.
-   Hook output tells the developer how to inspect the exact
    verification result.

------------------------------------------------------------------------

## Phase 3 --- Execution and verifier isolation

**Goal:** Make it safe to execute repository-controlled code, especially
when processing external pull requests.

For this macOS-only plan, this phase means safe local execution of
trusted repositories. Unattended execution of untrusted repositories is
out of scope; the current Linux rootless-container adapter remains an
optional path and is not a macOS security boundary.

### 3.1 Separate workspaces

Use a disposable workspace for each execution attempt.

-   Create it from the exact base/candidate revision.
-   Give each attempt a unique ownership marker.
-   Keep agent modifications isolated from the developer's original
    working tree.
-   Run verification against a fresh candidate snapshot, not a mutable
    agent workspace.
-   Clean up only when ownership and cleanliness checks pass.
-   Retain dirty or uncertain workspaces for explicit operator recovery.
-   Record the workspace identity and candidate digest in the attempt
    record.

The agent and verifier must not share a mutable workspace as their only
source of truth. Verification should consume a frozen candidate produced
after the agent attempt.

### 3.2 Introduce a runtime isolation adapter

Keep the domain independent of Docker, Podman, or another runtime. Add
an adapter behind the existing execution/verification boundary.

The selected macOS mode:

-   `local_process_reduced_env` for explicitly trusted local checks.
-   Checks run with the Verity process user's macOS permissions and may
    access the user's files and network; this is not OS sandboxing.
-   Do not accept `trust_level: untrusted` on this path. It remains
    blocked unless the Linux container policy is explicitly selected.
-   No silent fallback between policies: a requested unsupported policy
    returns `BLOCKED`.

macOS users must review the manifest commands before approving a plan
and must not run checks from untrusted repositories. macOS sandboxing
or a VM-based isolation adapter is a separate future capability.

### 3.3 Minimum isolation policy

For the optional Linux untrusted-code path:

-   Run as a non-root user.
-   Use rootless/user-namespace execution where available.
-   Drop unnecessary Linux capabilities.
-   Enable `no-new-privileges`.
-   Keep the container root filesystem read-only.
-   Use a disposable writable workspace and bounded temporary storage.
-   Use the runtime's default seccomp profile; do not disable it for
    convenience.
-   Apply CPU, memory, PID, disk, wall-clock, and output limits.
-   Disable network by default; allow only explicit destinations when a
    check genuinely requires network access.
-   Do not mount the host home directory, SSH agent, cloud credentials,
    Docker socket, or unrelated host paths.
-   Do not pass the host's environment wholesale.
-   Do not expose provider/API credentials to repository-controlled test
    commands.
-   Bound artifact collection and redact known secret patterns before
    persistence.

Do not use privileged containers as a shortcut. Container isolation
reduces risk but is not equivalent to a dedicated VM or microVM.
Document the remaining host-kernel and runtime trust assumptions.

### 3.4 Agent versus verifier boundary

Treat agent execution and verification as separate trust zones:

-   The agent receives only the runtime capabilities required to perform
    its task.
-   The verifier receives the frozen candidate and check policy, not the
    agent's mutable working directory.
-   The verifier must not inherit agent credentials.
-   Runtime credentials should be brokered by the trusted host process
    where practical.
-   Browser verification receives only the candidate and explicitly
    configured test credentials.
-   Test credentials must be scoped, short-lived, and unavailable to
    arbitrary build scripts unless the policy explicitly allows them.

### 3.5 Tests

Add malicious fixture repositories that attempt to:

-   Read files outside the candidate workspace.
-   Read environment variables and host home files.
-   Access SSH or cloud credentials.
-   Reach the network when disabled.
-   Access the container runtime socket.
-   Spawn excessive processes or consume memory/disk.
-   Escape via symlinks, path traversal, or cleanup races.
-   Leave background processes running after timeout.

### Acceptance criteria

-   Linux untrusted checks run only through the configured isolation adapter.
-   macOS-only use runs trusted checks with a reduced environment and
    explicitly acknowledges that they are not OS sandboxed.
-   There is no unattended untrusted PR execution in the macOS target.
-   Failure to provision a selected policy is visible as `BLOCKED`.
-   A verifier cannot mutate the developer's original repository.
-   Security assumptions and residual risks are documented.

------------------------------------------------------------------------

## Phase 4 --- Complete local execution lifecycle

**Goal:** Connect the existing components into one user-visible, durable
flow.

### 4.1 Build one application-level orchestrator

Do not put orchestration inside GitHub handlers, CLI commands, or
individual provider adapters.

The orchestrator should compose the existing services:

``` text
Request / Trigger
       |
       v
Process Control
       |
       v
Workspace + Candidate
       |
       v
Execution Provider
       |
       v
Frozen Candidate
       |
       v
Verification
       |
       v
Proof Assembly
       |
       v
Final Workflow Decision
```

The state machine remains the authority for workflow state. Adapters
report facts; they do not directly mark a workflow successful.

### 4.2 Lifecycle requirements

For each task:

1.  Persist execution intent before invoking the provider.
2.  Create and record an owned workspace.
3.  Execute with bounded timeout and cancellation.
4.  Persist the authoritative provider outcome.
5.  Capture the resulting candidate.
6.  Run independent verification against that candidate.
7.  Attach required check evidence.
8.  Validate the proof bundle.
9.  Advance the workflow only when all required gates pass.
10. On restart, recover uncertain attempts as `UNKNOWN` and require
    reconciliation.

### 4.3 Recovery behavior

Preserve the current durable-attempt contract.

-   Never rerun an uncertain attempt automatically.
-   Do not infer success from a workspace that happens to contain
    expected files.
-   Do not infer completion from a Vigil session or hook event.
-   Do not retry until the attempt has an authoritative result or an
    explicit actor-attributed reconciliation.
-   Make recovery inspectable through CLI commands and append-only
    events.

### 4.4 Acceptance criteria

-   One local command can run a multi-step plan through execution,
    verification, and proof.
-   Every attempt has durable intent and an immutable identity.
-   Restart tests prove that in-flight attempts become `UNKNOWN`.
-   A passing execution without passing verification cannot complete the
    workflow.
-   A passing verification against an old candidate cannot complete a
    newer candidate's task.
-   Proof export is reproducible and validates all referenced artifact
    digests.

### Phase 4 progress recorded 2026-11-18

The durable multi-task driver now lives in `verity/internal/orchestration`;
the CLI delegates to it. It persists invocation intent/results through the
execution store, verifies the exact captured content candidate, validates
required evidence before task completion, and refuses automatic retries of
UNKNOWN attempts. Existing recovery tests exercise interrupted execution,
provider failure, human review attribution, and non-completion without passing
verification. Agent attempts now use fresh owned per-attempt workspaces based
on the preceding immutable candidate; the user project root is not passed to
the agent. Focused workspace tests cover ownership and recovery after cleanup.
The acceptance criteria for the local Phase 4 lifecycle are covered by the
multi-step CLI, recovery, stale-candidate, evidence, and proof-export tests
below. This is deterministic local evidence; it does not claim live GitHub,
Linux container, or real provider delivery, which remain tracked by their
respective phases.

A focused CLI regression test now seeds a passing verification for a different
candidate while a task executes. The runner still verifies the execution's
actual candidate; a failing current check leaves the task incomplete despite
the stale PASS.

The restart regression suite now names the crash window after intent
persistence but before provider invocation, and asserts that restart records
`UNKNOWN` with no fabricated result and never invokes the provider on resume.
A separate unknown provider outcome models timeout/uncertain completion and
also proves no automatic retry occurs.

The SQLite recovery test now injects process loss after the provider returns
but before its result can be persisted. Reopening the database converts the
durable in-flight intent to `UNKNOWN`, keeps the result absent, refuses a new
attempt for that task, and confirms the provider was invoked only once.

GitHub client tests now cover transient 5xx retry and visible 403/429
rate-limit outcomes without a live API account.

A platform-independent worker orchestration test now drives a queued PR
through fake Git fetch and verification services, proof validation, and Check
Run updates. It covers a current head passing and a head that changes during
verification being published as cancelled without a PR summary. The fake
verifier tests worker orchestration only; the production path still uses the
rootless container verifier, and this test does not prove runtime isolation.

A separate platform-independent worker test injects an API `429` during
progress Check Run publication and verifies the durable queue retries the job,
keeps the workflow READY, and does not publish success.

Pull-request webhook normalization now retains the installation ID, and the
App worker verifies it against the installation discovered for its configured
repository before publishing progress or fetching the candidate. Persisted job
repository, number, and head SHA are also checked against the normalized event.
The signed receiver can additionally be configured with
`VERITY_GITHUB_INSTALLATION_ID` to reject mismatched installations before the
delivery creates a durable workflow.
Completed Check Runs include the local proof ID and export command; hosted
download URLs remain a deployment responsibility.

The multi-step plan integration test now changes the project verification
manifest after its human approval gate and confirms the plan continues using
the immutable manifest snapshot captured at creation.

The staged-hook integration test now mutates the real Git index from a trusted
check during pre-commit verification and confirms the hook returns a stale
candidate failure instead of allowing that result to authorize the changed
index.

The local lifecycle acceptance path now also checks proof export itself:
exporting the same verification twice produces byte-identical bundles, and
export refuses a bundle when a referenced content-addressed artifact has been
corrupted. The CLI verifies each stored artifact's content digest and size
before writing the bundle. This strengthens local proof integrity evidence;
release-level GitHub, Linux runtime, and real Codex/Vigil evidence remain
tracked in their respective phases.

------------------------------------------------------------------------

## Phase 5 --- GitHub event-to-proof orchestration (out of scope for macOS-local target)

**Goal:** Turn the existing GitHub integration pieces into a complete,
exact-SHA workflow.

### 5.1 Event intake

Build a deployable webhook receiver/worker boundary around the existing
signature-validation and event-normalization code.

Requirements:

-   Verify the raw-body signature before parsing.
-   Enforce request size and timeout limits.
-   Persist delivery IDs and normalized event facts.
-   Deduplicate deliveries idempotently.
-   Retry transient processing failures without creating duplicate work.
-   Keep webhook secrets and GitHub App credentials outside repository
    configuration and workflow payloads.
-   Scope installations and repositories explicitly.

The current one-shot local receiver remains useful for development and
operator workflows; it is not a substitute for a hosted receiver or
durable worker.

### 5.2 PR lifecycle

Support the relevant PR actions:

-   `opened`
-   `reopened`
-   `ready_for_review`
-   `synchronize`
-   `closed`

For each PR head SHA:

1.  Validate repository and installation scope.
2.  Create or retrieve the exact `(repository, PR number, head SHA)`
    association.
3.  Create a durable workflow.
4.  Fetch the exact candidate revision.
5.  Run the configured workflow.
6.  Verify the resulting candidate.
7.  Assemble and validate proof.
8.  Publish the result to the exact SHA.
9.  Re-check the PR's current head before publishing a final PR-level
    summary.

A new head SHA creates a new candidate/workflow association. Old results
remain historical; they must never be presented as the result for the
new head.

### 5.3 GitHub Check Run behavior

Map validated Verity outcomes to GitHub Check Run states explicitly.

-   Create the check against the exact full SHA.
-   Use a stable external ID for updates.
-   Publish progress as `queued` / `in_progress`.
-   Publish a completed conclusion only after the proof and final
    workflow decision are validated.
-   Never publish `success` for `UNKNOWN`, `BLOCKED`, `STALE`,
    `NEEDS_HUMAN`, missing evidence, or a mismatched SHA.
-   Include a concise summary and a link or downloadable reference to
    the proof.
-   Surface permission failures and rate limits rather than silently
    dropping results.

The GitHub integration should translate validated decisions; it must not
decide that a free-form agent message means success.

### 5.4 Stale candidate handling

If a PR receives new commits while an older workflow is running:

-   Mark the older PR association as superseded/stale for current-head
    purposes.
-   Do not publish its success as the current PR result.
-   Keep its proof for historical inspection.
-   Start or resume work for the new exact head SHA.
-   Re-check the latest head immediately before final publication.

### 5.5 Manual trigger

Add an explicit maintainer/contributor retrigger path after the
automatic flow is reliable. A PR comment command such as
`@axiom qa this` may request a run, but it must still resolve to the
current exact head SHA, be authorized, and be deduplicated.

### 5.6 Acceptance criteria

-   A real GitHub PR event creates exactly one workflow per delivery and
    PR-head association.
-   Duplicate deliveries do not create duplicate executions.
-   A changed PR SHA gets a separate workflow.
-   Check Runs are attached to the exact SHA.
-   A stale run cannot make a newer PR head green.
-   Proof and Check Run publication are recoverable after process
    restart.
-   Integration tests cover invalid signatures, duplicate delivery,
    permission errors, rate limits, retries, and SHA churn.
-   The receiver and worker can be deployed self-hosted without
    requiring contributors to modify application code.

------------------------------------------------------------------------

## Phase 6 --- Explicit Vigil ↔ Verity execution correlation (optional)

**Goal:** Connect Vigil observations to the execution attempt that
caused them without confusing observation with authority.

This is a deliberately late phase. The current manual bridge is safer
than pretending that an unverified session ID proves execution.

### 6.1 Preserve ownership

-   Verity remains authoritative for execution attempts, workflow state,
    verification, and proof.
-   Vigil remains authoritative for its own hook/session observations
    and advisor signals.
-   Verity must not read or write Vigil's private SQLite database.
-   Do not parse Codex transcripts as a substitute for a supported
    interface.
-   Missing Vigil data must not break Verity execution.

### 6.2 Proposed correlation protocol

1. The trusted Codex CLI runtime captures the provider session/thread ID
   from its structured `thread.started` event and stores it with the
   durable attempt result.
2. Verity asks Vigil's versioned public MCP tool for observations matching
   that session ID inside the attempt's start/finish interval.
3. Vigil returns only session ID, turn ID, schema version, and observation
   time. Verity validates the response and stores an append-only,
   candidate-bound observational link.
4. Missing, ambiguous, unavailable, or unsupported Vigil data is
   non-blocking and is not treated as proof of execution success.

Do not pass a correlation secret through the agent environment: agent code
can read that environment. This session-ID handshake avoids a bearer token,
but the equality between the provider thread ID and Vigil hook session ID
must be confirmed with actual hook delivery before claiming automatic
correlation support.

### 6.3 Security and semantics

-   Never expose a correlation secret to the agent workspace or process
    environment.
-   Match only an exact provider session ID and bounded attempt interval.
-   A matching session ID is observational provenance, not proof that a
    hook event was caused by the attempt or that execution succeeded.
-   Vigil's observed start/turn/stop events do not establish successful
    execution.
-   The provider's authoritative result and Verity's independent
    verification remain separate.

### 6.4 Acceptance criteria

-   A managed Verity execution can be correlated to the correct Vigil
    session without manual session-ID copying, demonstrated with real
    hook delivery.
-   A mismatched session or observation outside the attempt window is
    rejected.
-   Correlation records are append-only and candidate-bound.
-   Vigil unavailable or disabled does not block ordinary Verity
    workflows.
-   No Vigil database access or transcript scraping is introduced.
-   Existing explicit `vigil attach` remains available as a documented
    fallback, labeled caller-supplied and non-authoritative.

------------------------------------------------------------------------

### Phase 6 implementation progress recorded 2026-11-18

The Codex runtime now records the structured provider thread ID in its
durable result. Vigil exposes a versioned, read-only session observation MCP
tool bounded by session ID and execution time window; Verity validates those
fields and stores a candidate-bound observational link. No token or secret
is passed to the agent. Focused tests cover the MCP contract and time-window
filtering. This does not yet prove actual Codex hook delivery or that the
provider thread ID and Vigil hook session ID are the same identifier, so
Phase 6's protocol and failure handling are implemented and locally tested.
Real Codex hook delivery is optional runtime evidence, not a macOS-local
core-lifecycle acceptance requirement. Phase 6's own real-hook criterion above
remains open; the overall local release must not be described as fully accepted
on synthetic protocol tests alone. See [local acceptance](macos-local-acceptance.md).

Phase 7 now also covers the failure path where Vigil cannot be started and the
path where its MCP tool returns an incomplete observation. Both cases create
no candidate-bound link and remain non-blocking for orchestration.

## Phase 7 --- macOS-local evaluation and release

**Goal:** Prove the system as a whole, not just as a collection of
passing unit tests.

### 7.1 End-to-end fixture repository

Maintain a small fixture repository that can be used by CI and local
development. It should include:

-   A simple application with a passing test suite.
-   A deterministic failing test.
-   A deliberately slow test.
-   A test that produces bounded artifacts.
-   A Playwright smoke scenario where supported.
-   A candidate change that becomes stale during verification.
-   A malicious script fixture for isolation testing.
-   A Git history with multiple commits and staged/unstaged differences.

Use fake providers and deterministic fixtures for the normal CI suite.
For the macOS-local target, use fake providers and deterministic local
fixtures in the required suite. Real Codex/Vigil hook delivery is optional
product evidence. Linux container and live GitHub tests are excluded from
macOS acceptance; the Linux container option needs separate Linux evidence
before it may be used for untrusted code.

### Phase 7 fixture progress recorded 2026-11-18

Added a generator for a disposable multi-commit repository with staged,
unstaged, untracked, passing, deterministic failing, slow, bounded-artifact,
optional Playwright, and isolation-probe scenarios. A verification package
test generates the repository and checks history, index/worktree separation,
baseline/failure behavior, artifact bounds, and now runs the opt-in slow case
with a bounded duration. The real container probe is not run on the host. This
fixture is only an initial slice; it does not cover the complete failure
matrix or substitute for real Linux, GitHub, browser, and hook tests.

The fixture generator test now has a separate `VERITY_FIXTURE_PLAYWRIGHT=1`
path that runs `playwright/smoke.spec.js` with the supplied `PLAYWRIGHT_CLI`.
It is kept out of the default suite because the Go test environment does not
provision Node packages or browser binaries; setup instructions are in the
fixture README. The opt-in path was run successfully here using a temporary
Playwright install and Chromium browser, without adding project dependencies.

The Git hook bypass case now has a dedicated CLI integration test: it creates
a staged change with a failing pre-commit profile, installs managed hooks,
commits with `--no-verify`, confirms the pre-commit hook did not run while the
post-commit observer did, and verifies that no passing result exists for that
exact staged tree. This documents and tests the local hook's bypass boundary;
it does not turn local hooks into remote policy enforcement.

### 7.2 Required failure scenarios

-   Process crash before provider invocation.
-   Process crash after invocation but before result persistence.
-   Provider timeout with unknown external outcome.
-   Duplicate GitHub delivery.
-   PR head changes during execution.
-   Verification result refers to an old candidate.
-   Manifest changes after approval.
-   Index changes during pre-commit verification.
-   Hook installation conflicts with an existing hook.
-   Hook is bypassed.
-   Isolation runtime is missing or unhealthy.
-   Container attempts network or host access.
-   Proof artifact is missing or corrupted.
-   Vigil is unavailable or emits incomplete observations.
-   GitHub API returns permission failure or rate limit.

### 7.3 Quality gates

For each phase:

-   Focused unit tests.
-   Integration tests for adapters and persistence.
-   Race tests for shared state and event handling.
-   `go test ./...` for both Go modules.
-   `go vet ./...` for both modules.
-   `gofmt` verification.
-   Root `make check`.
-   Build checks for both binaries.
-   Python tracker tests.
-   Migration tests against populated older databases.
-   Documentation and compatibility updates.

Do not claim end-to-end support based only on unit tests or
component-level adapter tests.

### 7.4 Operational visibility

Expose enough information to diagnose a run without reading raw
transcripts:

-   Workflow and task state.
-   Current candidate and base SHA.
-   Execution attempt and provider outcome.
-   Verification profile and per-check result.
-   Proof integrity and artifact references.
-   Current PR head versus tested SHA.
-   Isolation mode and policy.
-   Recovery/reconciliation requirement.
-   Vigil correlation status, explicitly labeled observational.

Logs must be structured, bounded, and free of credentials.

### 7.5 Release acceptance

A release candidate is ready only when:

1.  A fixture repository can be processed through local verification and proof.
2.  Every local decision is tied to the exact candidate identity.
3.  A commit-time hook verifies the exact staged index.
4.  Trusted local checks run with a reduced environment and documented
    macOS process permissions; untrusted repositories are not run unattended.
5.  A process restart does not cause duplicate or unsafe execution.
6.  Proof can be exported and independently revalidated.
7.  Stale candidate and failure paths never produce a false green result.
8.  Existing local CLI and Vigil-only workflows continue to work.
9.  Install, upgrade, recovery, security, and compatibility docs match
    actual behavior.

------------------------------------------------------------------------

# 5. Suggested milestone order

The sequence below minimizes rework and keeps the riskiest work behind
explicit prerequisites.

  ------------------------------------------------------------------------------
  Milestone               Deliverable                    Depends on
  ----------------------- ------------------------------ -----------------------
  M0                      Candidate/trust contracts and  Current baseline
                          ADRs

  M1                      Immutable Git index candidate  M0
                          and `verify index`

  M2                      Safe pre-commit                M1
                          install/run/status/uninstall

  M3                      Isolated execution and         M0, then M1
                          verifier adapter

  M4                      Complete local execution →     M1, M3
                          verification → proof lifecycle

  M5                      GitHub event → workflow →      M3, M4
                          execution → proof → Check Run

  M6                      Explicit Vigil correlation     M4; Vigil interface
                          protocol                       change if required

  M7                      Full E2E evaluation,           M1--M6
                          hardening, release
  ------------------------------------------------------------------------------

### Recommended first implementation slice

Do not start with the webhook server or the Vigil correlation protocol.

Start with **M1: immutable Git index candidate**. It unlocks pre-commit
verification and strengthens the candidate model used by every later
phase.

Then implement **M2: pre-commit gate**. This gives Axiom an immediate,
visible capability in its own development loop and creates a concrete
way to dogfood Verity.

For the macOS-local target, prioritize the trusted local lifecycle and its
candidate/proof guarantees. Do not enable unattended untrusted PR execution;
that requires a separately validated isolation host.

------------------------------------------------------------------------

# 6. Explicit non-goals

The following should not be pulled into this roadmap unless a concrete
requirement appears:

-   A new top-level Axiom layer.
-   Replacing Vigil or merging Vigil's database into Verity.
-   A general-purpose autonomous coding agent product.
-   A hosted multi-tenant SaaS control plane before the local lifecycle
    is proven.
-   Distributed scheduling or PostgreSQL solely for architectural
    fashion.
-   A production deployment adapter before preview and verification
    semantics are proven.
-   A custom container runtime.
-   Automatic adoption of arbitrary Codex desktop sessions without a
    supported API.
-   Treating LLM review or agent self-report as authoritative
    acceptance.
-   Treating SHA-256 content integrity as a cryptographic signature or
    identity proof.
-   Broad framework-specific project generators.

------------------------------------------------------------------------

# 7. Definition of done for this roadmap

Axiom reaches the intended next stage when all of the following are
true:

-   A developer stages changes and runs `git commit`.
-   Axiom captures the exact Git index candidate without modifying the
    user's repository.
-   Required pre-commit checks run against that candidate.
-   A non-PASS result blocks the local commit.
-   A successful result can be bound to the actual commit only after its
    tree is confirmed.
-   A managed agent execution runs in an owned, isolated workspace.
-   The resulting candidate is independently verified in a separate
    environment.
-   Execution uncertainty survives restarts as `UNKNOWN` and requires
    reconciliation.
-   A valid proof bundle records the candidate, checks, evidence,
    provenance, and decisions.
-   A GitHub PR event can trigger the complete workflow and publish a
    Check Run for the exact head SHA.
-   New commits invalidate the relevance of old results for the current
    PR head.
-   Vigil observations can be correlated when supported, but never
    override Verity's execution or verification authority.
-   The whole lifecycle is covered by deterministic tests and at least
    one real end-to-end fixture run.

**The milestone is not complete when every component has an API. It is
complete when one real change can travel through the entire system and
Axiom can explain---using candidate-bound evidence---why it was accepted
or rejected.**

------------------------------------------------------------------------

## 8. Reference material

### Repository references

-   [Axiom repository](https://github.com/mithatakbulut/axiom)
-   [Verity
    architecture](https://github.com/mithatakbulut/axiom/blob/main/verity/docs/architecture.md)
-   [Verity
    contracts](https://github.com/mithatakbulut/axiom/blob/main/verity/docs/contracts.md)
-   [Verity
    status](https://github.com/mithatakbulut/axiom/blob/main/verity/docs/status.md)
-   [Verity security
    model](https://github.com/mithatakbulut/axiom/blob/main/verity/docs/security.md)
-   [GitHub
    integration](https://github.com/mithatakbulut/axiom/blob/main/verity/docs/integrations/github.md)
-   [GitHub local
    delivery](https://github.com/mithatakbulut/axiom/blob/main/verity/docs/integrations/github-delivery.md)
-   [Persistent
    plans](https://github.com/mithatakbulut/axiom/blob/main/verity/docs/persistent-plans.md)
-   [Vigil bridge
    ADR](https://github.com/mithatakbulut/axiom/blob/main/verity/docs/decisions/0003-vigil-mcp-bridge.md)
-   [Durable execution
    ADR](https://github.com/mithatakbulut/axiom/blob/main/verity/docs/decisions/0004-durable-execution-attempts.md)

### External technical references

-   [Git hooks documentation](https://git-scm.com/docs/githooks) ---
    hook execution, `pre-commit`, `post-commit`, and bypass behavior.
-   [Git FAQ: enforcing policy with
    hooks](https://git-scm.com/docs/gitfaq#hooks) --- why local hooks
    are not an enforcement boundary.
-   [GitHub Checks API](https://docs.github.com/en/rest/checks/runs) ---
    Check Runs attached to a specific commit.
-   [Docker Engine security](https://docs.docker.com/engine/security/)
    --- namespaces, capabilities, and runtime security boundaries.
-   [Docker rootless
    mode](https://docs.docker.com/engine/security/rootless/) ---
    reducing daemon and container privilege.
-   [Docker seccomp
    profiles](https://docs.docker.com/engine/security/seccomp/) ---
    syscall filtering and least privilege.
-   [Playwright CI](https://playwright.dev/docs/ci) --- browser test
    execution in CI.

------------------------------------------------------------------------

**Guiding principle:** Axiom should not try to make agents look
trustworthy. It should make their work independently inspectable,
reproducible, and accountable.
