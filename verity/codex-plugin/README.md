# Codex turn observations (experimental)

The plugin records the documented session ID, turn ID, event name, working
directory and observation time in `~/.verity/codex-observations.sqlite3`.
Prompt, transcript and tool contents are not stored. The store is separate from
project databases and does not require a project to be initialized. The hook makes a
best effort and must never block the turn. A read error or more than 10,000
observed turns causes snapshot reads to fail instead of silently returning a partial
list. Stored paths and identifiers can be sensitive.

These are observations, not Process Control workflow/task records. They do not
establish current execution, waiting for approval, success, failure, or complete
delivery. A Stop hook may be followed by continuation. Old or missing events
remain unknown. Local threads absent from app-server's list, remote execution,
working-directory mismatches, and untrusted, skipped or failed hooks are not
covered. Records made by the old prototype in project databases remain untouched
and are not imported into this store.

## Local setup

The `verity-local` marketplace and this revision of **verity-tracker** are
installed on this machine. Restart Codex, then use **Review hooks** in the composer or **Settings > Hooks**
to inspect and trust the current definitions. Installation alone does not trust
hooks. For another machine register this checkout with
`codex plugin marketplace add /absolute/path/to/verity`, then run
`codex plugin add verity-tracker@verity-local`. Python 3 at
`/usr/bin/python3` is required. Only future delivered events are recorded.
Removing the plugin and, if desired, the separate observation database stops
collection; do not delete other files in `~/.verity`.

Official references: https://learn.chatgpt.com/docs/hooks and
https://developers.openai.com/plugins/build/plugins .
