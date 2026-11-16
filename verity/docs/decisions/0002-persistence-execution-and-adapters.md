# ADR 0002: Persistence, workflow execution and adapter boundaries

- Status: Accepted by project owner on 2026-11-16
- Date: 2026-11-16
- Depends on: ADR 0001

## Context

Phase 0 compared the options in the implementation plan against the
single-node MVP. The chosen design must preserve restart recovery and
evidence provenance without requiring operators to run multiple services.

## Decisions

### Local target and database

Start as a local, single-node executable with SQLite and versioned
migrations. SQLite provides transactional writes, foreign keys and a
single-file backup model suited to a local workflow store. PostgreSQL is a
future adapter when team concurrency and server deployment are required;
it would add connection, deployment and operational management now. The
domain/application contracts must not depend on SQLite APIs.

### Workflow engine

Use a persisted deterministic Go state machine and one scheduler process.
It is sufficient for bounded MVP workflows and easier to install and
recover locally. Temporal is deferred because durable distributed timers
and worker fleets are not MVP requirements and would add an operated
service plus deterministic-replay constraints. Reconsider only if a
technical spike shows single-process recovery cannot meet a stated
reliability requirement.

### Artifact storage

Start with local filesystem bytes and database metadata containing relative
artifact key, size, media type, SHA-256, candidate identity, and provenance.
An S3-compatible backend may be added behind an artifact-store interface.
Never store bytes or credentials in workflow records. Hash and verify bytes
on ingest and retrieval.

### Git and repository commands

Use the Git CLI behind a narrow adapter. A disposable local probe confirmed
worktree add/list/remove behavior, but did not validate dirty-worktree or
crash recovery paths. Never shell-interpolate user commands. A future check
runner must use explicit argv, working directory, timeout, output limit,
cancellation and restricted environment, with an approval gate for
repository-provided commands. No command or agent execution is enabled by
this ADR.

### Browser checks

Playwright remains an optional adapter, invoked outside the Go core with a
pinned package/browser image. Store its configured output artifacts as
ordinary candidate-bound evidence. Playwright was absent locally, so Phase
0 verified the documented integration model but did not run a browser
spike. Implement and validate this adapter in the verification milestone.

### CLI and API

Make the CLI the initial user interface. Start with `init`, `doctor`,
`config validate`, and read-only project/workflow inspection. Defer HTTP
API and dashboard until application contracts stabilize; then use a
versioned `/api/v1` facade over the same application services.

### Vigil boundary

Do not modify Vigil or read its SQLite database. Current public surfaces
are CLI and stdio MCP; the eventual integration requires a versioned,
explicit bridge contract in the final roadmap phase.

## Consequences

These decisions allow Phase 1 to implement a small dependency footprint
and a single-node database design. PostgreSQL, Temporal, browser runtime,
remote artifact storage, HTTP API, agent execution and Vigil integration
remain deferred. The decisions may be changed only through a documented
ADR based on new requirements or measured evidence.
