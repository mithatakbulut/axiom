# Local Vigil and Verity example

Run from the directory containing the `vigil` and `verity` checkouts. Requires Go, CGO and a C compiler. This uses disposable data and a synthetic hook; it does not install hooks or contact an advisor.

```sh
work=$(mktemp -d)
printf "Temporary data: %s\n" "$work"
(cd vigil && go build -o "$work/vigil" ./cmd/vigil)
(cd verity && go build -o "$work/verity" ./cmd/verity)
cat > "$work/vigil.yaml" <<YAML
version: 1
mode: observe
storage:
  path: $work/vigil.db
  retention_days: 30
YAML
"$work/verity" init "$work/project"
cfg="$work/project/.verity/config.yaml"
"$work/verity" --config "$cfg" workflow create feature example-definition example-run
printf '%s\n' '{"hook_event_name":"PostToolUse","session_id":"example-session","tool_name":"Bash","exit_code":0,"tool_response":"Tests: 1 passed, 0 failed"}' | "$work/vigil" hook --config "$work/vigil.yaml"
"$work/verity" vigil inspect "$work/vigil" example-session -- --config "$work/vigil.yaml"
"$work/verity" --config "$cfg" task list example-run
cat > "$work/link.json" <<'JSON'
{"schema_version":1,"workflow_id":"example-run","task_id":"implement","attempt_id":"example-attempt","correlation_id":"example-correlation","base_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","candidate_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","session_id":"example-session","status":"candidate_ready"}
JSON
"$work/verity" vigil attach "$work/link.json" "$work/vigil" -- --verity-config "$cfg" --config "$work/vigil.yaml"
```

The inspect result demonstrates MCP compatibility and a session-scoped snapshot. The attach result persists an explicit candidate-bound reference; the supplied SHA, attempt and session relationship are synthetic and are not checked as an actual execution. The workflow remains pending: this example does not run an implementation, execute independent verification, produce a proof bundle, validate real hook delivery, or prove a candidate correct. Use the lifecycle and proof tests for the separately implemented verification path. Delete the printed temporary directory when no longer needed.
