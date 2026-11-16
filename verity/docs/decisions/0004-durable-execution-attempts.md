# ADR 0004: Durable execution attempts and recovery

- Status: Accepted for Phase 10 implementation on 2026-11-16
- Depends on: ADR 0001, ADR 0002, ADR 0003

## Context

Phase 3 runtime tracking was process-local. Restarting Verity could lose whether
an external invocation had begun or completed, making a retry unsafe. Phase 9
provides candidate-bound Vigil references but no public completion/correlation
contract, so those references cannot resolve execution status automatically.

## Decisions

1. Persist immutable execution intent before invoking a runtime.
2. Store subsequent lifecycle observations in append-only SQLite events, with a
   unique task/attempt number and unique execution ID.
3. Recover a persisted RUNNING attempt as UNKNOWN. Do not infer its outcome from
   process state, workspace contents, messages, or Vigil links.
4. Require explicit actor-attributed reconciliation or abandonment before an
   UNKNOWN attempt number may be reused. Reconciliation records execution facts
   only and cannot pass verification or accept a workflow.
5. Treat identical result/reconciliation submissions idempotently and reject
   conflicting decisions.
6. Keep recovery single-node and local. Distributed leases and external runtime
   cancellation guarantees remain out of scope.

## Consequences

SQLite migration 6 adds durable attempt intent and event tables. Existing
in-memory `Tracker` callers remain supported; `RunDurable` is the durable path.
Uncertain provider errors remain UNKNOWN until reconciled. Vigil remains optional
and is not queried to infer attempt completion.
