# Decision 0014: correlate Vigil observations by provider session ID

Date: 2026-11-18

## Context

Verity needs to associate optional Vigil hook observations with a durable
execution attempt. A bearer correlation token passed through a Codex worker
environment would be readable by agent code and candidate scripts. The
current Codex CLI emits a structured `thread.started` event containing a
provider session ID, while Vigil owns its hook-observed session and turn IDs.

## Decision

The trusted Codex runtime records the provider session ID on the execution
result. Verity may query Vigil through the public, versioned
`vigil_correlate_session` MCP tool using that ID and the execution time
interval. Vigil returns only session ID, turn ID, schema version, and
observation time. Verity validates and stores the association as an
append-only, candidate-bound observation. No private Vigil database access,
transcript parsing, or correlation secret is used.

Unavailable, ambiguous, or mismatched observations remain non-blocking and
never imply successful execution, verification, or approval. Automatic
correlation support is not claimed until real hook delivery confirms both
session ID compatibility and event timing.

## Consequences

- Agent-visible environments contain no Verity correlation credential.
- The integration depends on a stable provider session ID and Vigil MCP
  contract.
- Synthetic tests validate the protocol but cannot establish runtime hook
  propagation; that remains an explicit release gate.
