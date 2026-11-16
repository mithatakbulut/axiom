# Vigil

Vigil is a local monitor for Codex sessions. It records selected session activity, spots a few signs that work may be stuck, and helps you review usage, progress, and verification evidence. You choose how much intervention it can make.

Vigil stores its event database on your computer. It does not save prompts, tool inputs, tool responses, or transcripts. Its hook coverage is best effort and is not a security boundary. Read [Privacy and security](docs/security.md) before using it with sensitive work.

## What Vigil does

- Records allowlisted Codex hook metadata in a local SQLite database.
- Shows recent events, session summaries, usage, and progress signals.
- Lets you record task requirements and review evidence against them.
- Exposes the same controls through a command-line interface and a local stdio MCP server.
- Can prepare a read-only advisor request when a session appears stuck. Advisor use is subject to your selected mode, configuration, and approval.

Vigil does not edit your project or terminate processes. Its progress signals are heuristic and can miss events or interpret them incorrectly. See known limitations and evaluation.

## Modes

| Mode | Behavior |
| --- | --- |
| `observe` | Records and analyzes activity. It does not request an advisor intervention. This is the default. |
| `assist` | Surfaces an eligible intervention at a supported Codex Stop hook. You review and explicitly approve an advisor run from your terminal. The advisor is read-only and returns a recommendation. |
| `autonomous` | Records shadow decisions by default. Live continuation is gated: it requires changing the escalation settings and is intended only after the release criteria are met. |

You can pause or resume interventions at any time. Changing the mode does not itself enable live autonomous continuation.

## Install

Vigil requires Go 1.26.5 or newer, CGO enabled, a C compiler, and Codex for its integration. On macOS, install the C compiler with `xcode-select --install`. From the `vigil/` module checkout, build the binary and register it with Codex:

```sh
go build -o ./vigil ./cmd/vigil
./vigil install
```

The installer registers Vigil as an MCP server and adds its hook handlers to your Codex configuration. It uses `$CODEX_HOME` when set, or `~/.codex` otherwise. Keep the binary at the installed path, since the registration refers to it. Review and trust the new hooks in Codex before they run: use `/hooks` in the CLI or **Settings → Hooks** in the desktop app. Then open a new session.

To use a separate configuration file, pass `--config /absolute/path/to/config.yaml` to install and to later Vigil commands. The installer registers that same path with Codex. Relative database paths in a configuration file resolve from the file's directory.

Vigil defaults to `observe`. If no configuration file exists, defaults are used. On macOS the default configuration is `~/Library/Application Support/Vigil/config.yaml`; the database is `vigil.db` beside it. A minimal configuration looks like this:

```yaml
version: 1
mode: observe
storage:
  path: /absolute/path/to/vigil.db
  retention_days: 30
```

For a first check, finish a short Codex turn, then run `./vigil status` and `./vigil history --last 20`. You can also call `vigil_status` and `vigil_history` from a new Codex turn.

## Everyday use

```sh
./vigil status
./vigil history --last 20
./vigil progress --session SESSION_ID
./vigil budget --session SESSION_ID
./vigil explain --session SESSION_ID
```

Use `history` to find session, turn, and event IDs. `progress` summarizes observed test trends, repeated failures and commands, and optional tracked Git changes. `budget` reports observed usage and configured limits. Missing hook data can make these reports incomplete; unknown token counts and estimated API costs stay unknown.

Change modes and intervention state with:

```sh
./vigil mode observe
./vigil mode assist
./vigil mode autonomous
./vigil pause
./vigil resume
```

To prepare an approved advisor request in Assist mode, check model availability and review the candidate first:

```sh
./vigil advisor check
./vigil advisor request --session SESSION_ID --turn TURN_ID
./vigil advisor run --approved --session SESSION_ID --turn TURN_ID --repo /absolute/path/to/project
```

The advisor request runs read-only and returns a recommendation for you to review and share with Codex. `--approved` is required for the terminal run. Vigil does not silently substitute another model if the configured model is unavailable. Model access depends on your Codex account and configuration.

## MCP tools

Vigil registers these tools with Codex:

| Tool | What it does |
| --- | --- |
| `vigil_status` | Shows the active mode, pause state, event count, database path, and whether interventions are available. |
| `vigil_history` | Lists recent recorded events. |
| `vigil_explain` | Shows the latest progress assessment and advisor decision for a session. |
| `vigil_budget` | Reports observed usage and configured limits for a session or turn. |
| `vigil_verification` | Shows task requirements, their verification state, and missing evidence. |
| `vigil_record_evidence` | Attaches evidence to an existing task requirement. |
| `vigil_eval` | Runs isolated, deterministic detector checks. |
| `vigil_pause` / `vigil_resume` | Pauses or resumes interventions. |
| `vigil_set_mode` | Selects `observe`, `assist`, or `autonomous`. |
| `vigil_request_advisor` | Prepares a read-only advisor request for explicit terminal approval. |

The MCP server uses standard input and output and shares mode and pause settings with the CLI.

## Task evidence

Create a task contract, attach evidence to its requirements, then review the verification result. Evidence is not automatically accepted just because a test suite passed; linked test evidence must match a captured compatible result. A manual attestation is recorded as such.

```sh
printf '%s\n' '{"id":"ship","title":"Ship the fix","requirements":[{"id":"tests","description":"Focused tests pass"}]}' |
  ./vigil task create --session SESSION_ID
printf '%s\n' '{"kind":"unit_test","source":"example_test.go","result":"Focused tests passed","state":"verified"}' |
  ./vigil task evidence --session SESSION_ID --task ship --requirement tests
./vigil task verify --session SESSION_ID --task ship
```

## Data, backup, and removal

Hook events are retained according to `storage.retention_days` (30 days by default). Task contracts and evidence persist beyond event retention, including a bounded summary of linked event fields. Historical verified test evidence whose event was already deleted is downgraded to evidenced on schema upgrade and requires review. Exact byte-identical hook payloads are deduplicated, so separate identical events may collapse. Arbitrary identifiers can still contain sensitive information; protect the database and use trusted event producers. See the [hook contract](docs/hook-contract.md) for captured fields and coverage limits.

Create a consistent SQLite backup at a new path:

```sh
./vigil backup --output /path/to/vigil-backup.db
```

Remove Vigil's hook and MCP registrations while keeping your database:

```sh
./vigil uninstall
```

Use `./vigil rollback` to restore the original Codex configuration bytes and permissions when the affected files have not been changed since installation. Use the same `$CODEX_HOME` for install, uninstall, and rollback. The private install backup contains copies of affected configuration files and may contain credentials; it is removed after successful uninstall or rollback.

## Documentation

- [Privacy and security boundaries](docs/security.md)
- [Hook fields and coverage](docs/hook-contract.md)
- Evaluation and autonomous-mode criteria
- Release status and known limitations
- [Full configuration example](configs/example.yaml)

## Build packages

On macOS with Xcode command-line tools and Go, run `scripts/package-macos.sh`. It writes architecture-specific archives and `SHA256SUMS` to `dist/`. Verify from that directory with:

```sh
shasum -a 256 -c SHA256SUMS
```
