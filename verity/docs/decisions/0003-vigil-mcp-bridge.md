# ADR 0003: Read-only Vigil MCP bridge

- Status: Accepted for Phase 9 implementation on 2026-11-16
- Depends on: ADR 0001, ADR 0002

## Context

Inspection of Vigil 0.1.0 found documented CLI and stdio MCP interfaces, but
no Verity-specific event, export, launch, attach, or correlation contract.
Vigil MCP exposes session-scoped `vigil_explain` and `vigil_budget` tools.
Its history tool is not session-filtered. Direct SQLite access and transcript
parsing are prohibited by the product boundary.

## Decision

Implement an optional Verity-owned MCP client that starts the configured
Vigil executable, validates MCP identity/version and the tool catalog, and
reads only the explicit session's explanation and budget. Define a versioned
JSON link record with execution-only statuses. A Vigil signal is advisory
metadata and cannot pass Verity verification or accept a workflow.

Persist attached link records append-only in Verity, keyed by attempt, and
include only matching workflow/candidate references in proof export. Expose
CLI inspection and explicit attach commands. Do not add a Vigil-side
interface, auto-launch Codex, or infer session IDs. Preserve operation
without Vigil.

## Consequences

Verity gains a real, testable adapter for the current documented MCP surface
without taking ownership of Vigil data. Explicit session attachment provides
operator/runtime-mediated correlation and proof-bundle references. Automatic
launch/session discovery and replay remain unavailable until Vigil publishes
an explicit correlation/export contract. An attached session ID is supplied
by the caller and does not prove that the session executed the attempt.
