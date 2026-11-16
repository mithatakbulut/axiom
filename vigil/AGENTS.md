# Vigil development

Vigil owns hook ingestion, observation, Stop and advisor policy; Verity owns
workflow approval, verification and proof. Run commands from `vigil/` and
follow the root AGENTS.md for substantive changes.

For hook/Stop changes start at `cmd/vigil/main.go`, `internal/control/` and
`internal/recorder/`; run `go test ./cmd/vigil ./internal/control ./internal/recorder`.
For MCP changes start at `internal/mcp/`; run `go test ./internal/mcp` and the
Verity bridge tests. The full module suite is `go test ./...`. The root's
`verity/docs/integrations/local-example.md` exercises a synthetic hook and MCP
bridge with temporary data, without installing hooks or calling an advisor.
