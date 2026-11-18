# Verity

Verity is a local-first software delivery control plane. It records workflow
state, candidate-pinned verification, and content-addressed proof artifacts.
The v0.1.0 CLI is an early single-node release; see [supported capabilities](docs/compatibility.md)
and [known limitations](docs/status.md).

## Install

Requirements: Go 1.23 or newer, a C compiler, and CGO enabled (the SQLite
driver uses CGO). On macOS, install the C compiler with
`xcode-select --install`. From the `verity/` module checkout, install the CLI:

```sh
go install ./cmd/verity
```

Go places the binary in `GOBIN`, or in `GOPATH/bin` when `GOBIN` is unset.
If `verity` is not found, add that directory to your shell's `PATH`. For
example, in a POSIX-compatible shell:

```sh
go_bin="$(go env GOBIN)"
if [ -z "$go_bin" ]; then go_bin="$(go env GOPATH)/bin"; fi
export PATH="$go_bin:$PATH"
verity version
```

Add the appropriate `export PATH=...` to your shell's startup file to retain
it in future sessions. Alternatively, build an archive on the target host:

```sh
make package VERSION=0.1.0
```

The archive is written to `dist/verity_0.1.0_<os>_<arch>.tar.gz`. Extract it
and place the `verity` executable in a directory on your `PATH`, for example:

```sh
mkdir -p "$HOME/.local/bin"
tar -xzf dist/verity_0.1.0_$(go env GOOS)_$(go env GOARCH).tar.gz -C "$HOME/.local/bin"
export PATH="$HOME/.local/bin:$PATH"
verity version
```

Add `$HOME/.local/bin` to your shell's startup `PATH` to retain it in future
sessions. The archive is built for the current host only.

## Initialize and check a project

```sh
verity version
verity init /path/to/project
verity config validate /path/to/project/.verity/config.yaml
verity doctor /path/to/project/.verity/config.yaml
```

The CLI supports workflow and task inspection/creation, and proof bundle
inspection/export. Run `verity` without arguments to see the command groups.
Configuration starts with schema version 1 and stores its database and data
under `.verity/` by default. Keep that directory private; it may contain
workflow metadata and proof references. Do not put credentials in config,
workflow inputs, or captured evidence.

For current provider support, environment requirements, and security limits,
see [compatibility](docs/compatibility.md) and [security](docs/security.md).
Upgrade and release steps are in [release procedures](docs/release.md).

## CLI quick start

Run commands from an initialized project, or pass `--config /path/to/project/.verity/config.yaml` to select another project. For example:

```sh
verity init
verity doctor
verity workflow create feature feature-definition feature-1
verity workflow list
verity workflow show feature-1
verity task list feature-1
```

Use `verity --help` or, for example, `verity workflow create --help` for command syntax. Workflow creation, workflow and task inspection, and proof inspection accept `--json` for machine-readable output; list commands and detail views otherwise show concise summaries. Proof export writes a JSON bundle. Older positional configuration paths remain supported. For Vigil arguments that overlap with Verity options, place them after `--`.

A disposable [Vigil integration example](docs/integrations/local-example.md) demonstrates the explicit MCP link and its limits.

To independently check an exported bundle without the project database, copy
the content-addressed artifact directory with it and run
`verity proof verify proof.json /path/to/artifacts`. For a default project the
directory is `.verity/artifacts`. This validates bundle consistency and artifact
digests; it does not authenticate the producer, prove that checks were actually
executed, or turn a non-PASS result into a PASS. Bundles are not signed.

To turn a forwarded signed GitHub pull-request delivery into a local workflow record, see [GitHub delivery](docs/integrations/github-delivery.md).

## Local source verification without Git

For an initialized project, define `.verity/verification.yaml` and create a
workflow. Capture explicitly selected source paths with `verity verify snapshot <path>...`;
use the returned `snapshot:<digest>` with `verity verify plan <workflow-id> <snapshot> <profile>`.
Review the manifest and plan, then supply the returned approval digest to
`verity verify run <workflow-id> <snapshot> <profile> <approval-digest>`.
Check output and the result are persisted; inspect
the returned verification ID with `verity proof show`. Source selection omits
`.git`, `.verity`, `node_modules`, and `dist`. The snapshot identifies only
the captured files, and checks run with the current user's OS permissions.
The local `.verity/config.yaml` and `.verity/verification.yaml` are trusted
control files: Git candidate verification rejects a candidate containing
either path, and the approval digest binds the exact config bytes and parsed
manifest.
These commands do not automatically register or drive Codex work.

For a Git repository, `verity verify index plan <workflow-id> <profile>`
captures the complete staged tree, its current `HEAD` base, and the configured
checks. Review the returned `git_index` candidate and approval digest, then run
`verity verify index run <workflow-id> <profile> <approval-digest>`. Checks run
in a disposable tree materialized from staged blobs, so unstaged edits and
untracked files are excluded. The approval is rejected if the staged tree,
base, workflow, manifest, profile, or checks differ. The result records the
tree identity and base revision. Unmerged entries, submodules, and Git LFS
pointers are rejected. Checks still run with the current user's OS permissions.

To enable the opt-in local Git gate, run `verity hooks install`, inspect it
with `verity hooks status`, and remove it with `verity hooks uninstall`.
Existing hooks require `--chain` and run before Verity. The pre-commit hook
blocks unless the configured `pre-commit` profile passes. Its post-commit hook
records a proof binding only when `HEAD^{tree}` matches that passing index
candidate. Git can bypass local hooks with `git commit --no-verify`; this is
developer feedback, not repository enforcement. See `docs/hooks.md`.

For an existing chat, `verity chat start feature` creates a workflow; use
`verity chat begin <id> <task>` and `verity chat finish <id> <task>` to record
work actually performed. Begin `verify`, then run `verity chat verify <id>
<source-path>...`; that command captures source, performs the project's
configured checks, persists evidence and advances only on PASS. The feature
template retains its human approval gate. These are explicit agent-reported
actions, not authenticated session correlation or proof that every chat
automatically called the tools. This approach starts no additional Codex.

To start a new managed job, put the instruction in a file and run
`verity work run <instruction-file> <source-path>...`. Verity launches the
local Codex CLI in the configured project, records its attempt, captures the
selected paths and runs the standard verification profile. It pauses when the
runtime result is uncertain or verification does not pass. Set `codex_model`
in the project config when the CLI's default model is unavailable. The
instruction is stored in the local attempt record. The command modifies the
project through Codex, so select paths that cover the work; omitted paths are
not verified. Jobs started directly in the Codex app are not automatically
managed by this command.

For sequential, durable multi-step work, see [persistent plans](docs/persistent-plans.md).
