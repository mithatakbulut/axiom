# Dashboard snapshot contract (v1)

`verity dashboard summary --config /absolute/project/.verity/config.yaml --json`
prints one JSON object on stdout. It uses the same project-root resolution as
other commands: the database path is relative to the parent of the config
directory. The absolute config path is the local project identity; the project
directory basename is its display name. Renaming or moving the project changes
that identity. The CLI version is available through `verity version` (the
current unconfigured build reports `dev`); consumers must use `schema_version`
for compatibility, not infer it from the CLI version.

The object contains `schema_version: 1`, `project_id`, `project_name`,
`generated_at` (UTC), and `runs` (always an array). Each run contains `id`,
`title` (the definition name), `state`, `created_at`, and `tasks`; each task
contains `id`, `title`, and `state`. Raw status strings are preserved. No
summary includes descriptions, criteria, evidence or credentials; titles
and paths can still contain sensitive information. No rows are truncated.
Creation time is not a completion or update time. The snapshot is taken in a
single read transaction. Database schema versions 1–7 are accepted; missing or
future schemas fail instead of being migrated. Config and database files are
never created. A WAL database may require access to existing `-wal` and `-shm`
files, or directory permissions that allow SQLite to manage shared memory;
read-only database-file permissions alone do not guarantee WAL readability.

The command exits 0 on success. Errors exit 1 and, for the dashboard command,
stderr contains a JSON object with `error: "dashboard_summary_failed"` and a
human-readable `message`; stdout contains no successful snapshot. Other CLI
commands retain their existing error format.

## Display and event examples

* No runs: `runs: []`; active count 0, neutral indicator.
* A RUNNING run with RUNNING and WAITING_APPROVAL tasks counts as one active
  run, and both task labels are shown. READY tasks are also active stages.
* READY, RUNNING, PAUSED and WAITING_APPROVAL runs count as active; DRAFT is
  separate. SUCCEEDED, FAILED and CANCELLED do not count as active.
* Unknown run or task states are preserved and displayed as unknown, never
  silently treated as success or zero.
* An access error is unknown/stale, not an empty project. Retain the last
  successful snapshot with its timestamp, visibly marked stale.
* Indicator precedence: stale/error; new unseen failure; approval or pause;
  ready/running; newly observed success; neutral. Historical terminal runs at
  baseline do not trigger an event. New observed failures/successes clear when
  shown; failure acknowledgement is persisted by project and run identity.
* Compare consecutive successful snapshots. Baseline and project switches do
  not notify. Re-reading the same state does not notify. Polling cannot see
  transitions that occur entirely between reads or while the app is closed.

The former Electron consumer has been removed; the CLI contract remains available.
