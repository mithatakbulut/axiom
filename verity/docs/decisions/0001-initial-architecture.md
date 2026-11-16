# ADR 0001: Initial architecture and execution boundary

- Status: Accepted by project owner on 2026-11-16
- Date: 2026-11-16

## Context

The plan calls for an agent-independent, local-first control plane with
three layers. The repository currently contains only the plan. Foundational
choices should avoid coupling the domain to Vigil or external providers.

## Decisions

1. Keep Verity as a standalone Go module under `verity/`.
2. Place the three layers under `verity/internal/process`,
   `verity/internal/verification` and `verity/internal/proof`.
3. Start with a local single-node deployment. Team deployment remains a
   later target; do not introduce PostgreSQL or a service API yet.
4. Prefer a persisted deterministic Go state machine over Temporal for the
   first release. Revisit if durable timers, distributed workers or
   cross-service orchestration become MVP requirements.
5. Target SQLite for local persistence and local filesystem artifact bytes;
   keep provider-specific storage behind future interfaces. No database or
   artifact implementation is included in this initial slice.
6. Keep Git and repository commands behind adapters. Domain packages never
   shell out. No agent execution is enabled.
7. Keep verification outputs and proof evidence bound to a full candidate
   SHA/digest. A check result is not a gate decision.
8. Do not integrate with or modify Vigil before the final roadmap phase.

## Consequences

The first implementation can be run and reasoned about without external
services. Persistence, API, CLI, worktree lifecycle, Playwright and
artifact storage still need their own implementation milestones. These
are accepted Phase 0 starting decisions; revisit through a new ADR if
team access, operational evidence or reliability requirements change the
assumptions.
