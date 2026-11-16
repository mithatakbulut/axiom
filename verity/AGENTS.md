# Verity development

Verity owns workflow state, approvals, verification and proof. Run commands in
`verity/`; follow the root AGENTS.md for the local workflow.

- Workflow and approval: start at `internal/process/control/` and
  `cmd/verity/chat.go`; run `go test ./internal/process/control ./cmd/verity`.
- Verification: start at `internal/verification/`; run
  `go test ./internal/verification`.
- Vigil–Verity MCP bridge: start at `internal/integrations/vigil/` and
  `cmd/verity/`; run `go test ./internal/integrations/vigil ./cmd/verity`.
  Exercise the disposable synthetic hook and MCP link in
  `docs/integrations/local-example.md` from the Axiom root.
- Tracker: `python3 codex-plugin/test_hook.py`. Full suite: `go test ./...`.

Record consequential architectural decisions in `docs/decisions/`. A bridge
snapshot or caller-supplied link does not prove execution or grant approval.
