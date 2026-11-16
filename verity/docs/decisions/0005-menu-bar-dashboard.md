# 0005: Local menu-bar snapshot consumer

The desktop app calls the Verity dashboard v1 CLI rather than linking to SQLite
or exposing a network service. A transaction-scoped read-only connection returns
all Process Control runs without evidence or descriptions. Snapshot polling is
bounded and serialized; a schema mismatch or read error produces a stale or
unknown state instead of an empty result. Paths are chosen through native file
dialogs and commands use an argument vector without a shell. The renderer has a
sandboxed preload interface and no Node integration.

Electron 45.3.3 and electron-builder 26.0.12 target macOS 13 and later. Local
builds are unsigned and do not bundle the separately selected CLI. Start,
success, failure and approval notifications and sounds default off. The initial
snapshot is a baseline, not an event source; no event replay or completion time
is claimed. This design cannot guarantee observation between polls or when the
app is closed. Signing and notarization require separate release credentials.
