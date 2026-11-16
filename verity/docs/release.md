# Release and upgrade procedures

## Build and verify a release

Run from the `verity/` module root with Go 1.23+, CGO enabled, and a working
C toolchain:

```sh
GOCACHE=/private/tmp/verity-gocache go test ./...
GOCACHE=/private/tmp/verity-gocache go vet ./...
GOCACHE=/private/tmp/verity-gocache go build ./...
make package VERSION=0.1.0
```

The package target builds for the current host only. Build separately on each
supported OS/architecture because the SQLite driver uses CGO. Record the Go
version, OS/architecture, source revision, and validation output with each
published archive. The archive currently contains only the CLI executable;
configuration and database are created by `verity init` at the target
project.

Before publishing, review `docs/status.md` for open limitations and run the
release invariants in the verification section below. Releases are manually
built and published; this repository does not yet automate signing, checksums,
or distribution.

## Upgrade

1. Stop any Verity process using the project database.
2. Back up `.verity/` (database, WAL files, and artifact directory) as one
   consistent unit. Use SQLite's backup API or copy while Verity is stopped.
3. Replace the executable with the new host-matched archive and retain the
   previous binary until validation completes.
4. Run `verity config validate` and `verity doctor` against the existing
   config. Startup applies idempotent SQLite schema migrations.
5. Inspect workflows and proof exports, then resume normal use.
6. If validation fails, stop the new binary and restore the complete `.verity/`
   backup and prior executable together.

There is no automated downgrade migration. Never restore only the database
without its matching artifact directory when proof references are in use.

## Release invariants

- A run cannot PASS without at least one required passing check; optional
  checks cannot establish acceptance.
- Required checks need evidence before a PASS verification run is persisted.
- Verification is pinned to a full candidate SHA; changed candidates become
  stale and cannot reuse old results.
- Duplicate GitHub delivery IDs and duplicate repository/PR/head-SHA
  associations do not create duplicate records.
- Preview deployment must report the verified source SHA and resolve to HTTPS.
- Check commands use argv, an approved plan, a disposable source snapshot,
  bounded time/output, and a reduced environment. They are not OS sandboxed.
- No production deployment adapter is present; production enablement fails
  closed.

## Benchmark

Run `make benchmark` for repeatable local artifact-store Put (deduplicated)
and Get measurements at 64 KiB with allocation counts. Capture the Go version,
host, and output when comparing releases. These microbenchmarks measure local
filesystem operations only; they are not representative workflow latency or a
cross-machine performance guarantee. SQLite and end-to-end task performance
have not been benchmarked in v0.1.0.
