# Vigil compatibility and bridge contract

Inspection date: 2026-11-16. Inspected the checked-in Vigil 0.1.0 source,
release artifacts, README, hook contract, CLI, and stdio MCP implementation.
The runtime MCP server reports `serverInfo.name=vigil` and
`serverInfo.version=0.1.0`.

## Compatibility matrix

| Capability | Public surface | Observed support | Verity use / limit |
| --- | --- | --- | --- |
| Status and mode | CLI `status`; MCP `vigil_status` | JSON via MCP; CLI status is human text | Not needed for task correlation |
| Session progress and advisor explanation | CLI `explain --session`; MCP `vigil_explain(session_id)` | JSON with latest progress and advisor records | Consumed as a point-in-time snapshot |
| Session/turn budget | CLI `budget`; MCP `vigil_budget(session_id, turn_id)` | JSON usage, configured limits and warnings | Consumed as a point-in-time snapshot |
| Event history | CLI `history`; MCP `vigil_history(limit)` | JSON events, but no session filter in the tool | Not consumed; cannot safely correlate history to a session via this tool |
| Task evidence | CLI `task`; MCP `vigil_record_evidence`, `vigil_verification` | Vigil-owned session task contracts | Not used; Verity owns project verification and proof |
| Hook events | `vigil hook` from configured Codex hooks | Allowlisted metadata only; payload is not a general Verity callback | No Verity metadata/correlation injection contract exists |
| Launch/attach runtime | None found | No general-purpose execution or attach API | Verity does not launch or claim to associate a real Codex run automatically |
| Export/event cursor | None found | No public export cursor or replay API | Reconciliation after disconnect is unavailable |
| Persistence | Local SQLite | Internal implementation | Verity does not access it |

Vigil's CLI emits JSON for selected reports, but does not publish a versioned
machine-readable contract for all commands. The bridge therefore uses MCP,
whose initialization response, tool catalog, tool names and input schemas are
observable public protocol surfaces. MCP itself is not a Verity-specific
versioned contract, so Verity checks Vigil identity/version and requires the
specific tools it consumes. Only Vigil 0.1.0 is currently allowlisted;
unsupported versions or missing tools return
`ErrIncompatible`.

## Bridge contract v1

`internal/integrations/vigil.Link` is a JSON record with `schema_version: 1`,
workflow/task/attempt IDs, a correlation ID, base and optional candidate SHA,
runtime/session IDs, status, normalized agent signals, optional event
references, summary, and capture time. SHA values must be full lowercase
40- or 64-character hexadecimal IDs. Status is limited to
`execution_completed`, `execution_failed`, `execution_blocked`, and
`candidate_ready`. The type rejects `verification_passed` and
`workflow_accepted`: only Verity can create those decisions.

`MCPClient.Inspect` launches the configured Vigil executable with `mcp` and
optional caller-supplied arguments, performs MCP `initialize` and
`tools/list`, then calls `vigil_explain` and `vigil_budget` with an explicit
session ID (and optional turn ID). It captures no raw hook events. The
snapshot retains normalized progress signals, advisor state, budget warnings,
observed model requests/runtime, and the capture time. It does
not infer execution completion, candidate readiness, verification, or
acceptance from a snapshot. MCP access is read-only for these two calls.

First prepare a version 1 link JSON after the execution result and candidate
SHA are known. Its `workflow_id`, `task_id`, `attempt_id`, and
`correlation_id` are Verity identifiers; `session_id` is the explicit Codex
session ID. The workflow run and task must already exist in Verity, and the
task must belong to that run. Then attach the Vigil summary:

```sh
verity vigil attach verity-vigil-link.json /absolute/path/to/vigil -- --verity-config /path/to/.verity/config.yaml --config /path/to/vigil.yaml
```

The link must include `schema_version`, Verity IDs, `base_sha`,
`candidate_sha`, `session_id`, and an execution-only `status`; the attach
command fills Vigil version, normalized signals/budget summaries, and capture
time. `--verity-config` selects Verity's database configuration; remaining
arguments (such as Vigil's `--config`) are passed to the Vigil MCP process.
It persists the link append-only in Verity's SQLite database. The
`verity proof export` command includes only links matching that proof's exact
workflow and candidate. The `inspect` command remains available for read-only
snapshots. Verity does not discover session IDs or inject metadata into Vigil.

## Known integration gaps

The public surface supports explicit human/runtime handoff correlation by
session ID, but not automatic launch/session discovery: Vigil exposes no task metadata
ingestion, Verity correlation field, callback, session-filtered event cursor,
or machine-readable completion event. The attach operation therefore binds a
caller-supplied session ID to a Verity execution attempt record; it cannot
prove that the session performed that attempt. This limitation is visible in
the CLI contract and must be retained in operational use.

If either is pursued later, add a separately accepted integration ADR and
keep the new Vigil interface backward compatible. No Vigil files or data
were changed for this phase.

For a disposable, runnable MCP and explicit-link example, see [local example](local-example.md).
