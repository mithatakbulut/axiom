# Initial threat model

## Assets

- Workflow state and transition history.
- Candidate identity and verification results.
- Evidence metadata, artifact bytes and approval records.
- Repository data and credentials supplied to future integrations.

## Threats and current controls

| Threat | Initial control |
| --- | --- |
| Agent claims checks passed without running them | Process state is separate from check observations; no agent completion API exists. |
| Verification is reused for a changed candidate | Each verification run requires a full immutable revision. |
| Evidence is altered after capture | Evidence records include a SHA-256 content digest. |
| Repository content injects commands | Verification commands are displayed before execution, passed as argv without a shell, and run only after explicit plan approval in a disposable candidate snapshot with bounded time/output. |
| Secret leaks into proof | Evidence records store provenance and digest, while artifact bytes live separately. Explicit redaction is available before capture; callers remain responsible for filtering sensitive output. |
| Vigil data is treated as Verity authority | The optional Verity-owned MCP client reads Vigil snapshots and records explicit links; neither snapshots nor links prove execution or grant approval. Verity does not depend on the Vigil database. |
| Cleanup removes user work | Git cleanup requires an adjacent Verity ownership marker, matching Git common directory, and clean status; dirty worktrees are retained. |
| Runtime outlives its task budget | Runtime calls receive a derived context with the request timeout and parent cancellation. |
| Manual completion is mistaken for acceptance | Manual handoff returns `BLOCKED` without a candidate SHA; execution records do not pass verification. |

## Open work

Execution runtimes remain deterministic fake/manual adapters; they do not
launch agents. The Phase 4 check runner now executes repository commands in
a disposable source snapshot with a reduced environment, bounded time and
captured output, and explicit plan approval. It is not an OS sandbox: checks
run with the Verity process user's permissions and may access available
network and system resources. Stronger isolation, CPU/memory limits, and
automated artifact retention policy remains caller-managed; retention age and
redaction policy primitives are now available. This document is an initial
boundary, not a complete security review.


## GitHub and preview provider boundary

Webhook HMAC-SHA256 signatures are checked before JSON decoding. Payloads are
limited to 1 MiB. Delivery IDs are persisted before dispatch and released if
dispatch fails, allowing GitHub retry; a concurrent duplicate while a delivery
is in flight is acknowledged without a second dispatch. GitHub API tokens and
webhook secrets are runtime constructor inputs, never webhook fields or proof
data. API calls use bounded response reads, cancellation, bounded retries, and
pagination limits. Downloaded Action artifacts are capped at 50 MiB and are
size/digest checked when metadata provides them. Netlify responses must report
the expected candidate SHA, and preview URL resolution enforces HTTPS.
