package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
	"vigil/internal/control"
	"vigil/internal/recorder"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}
type tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

func schema(properties map[string]any) any {
	if properties == nil {
		properties = map[string]any{}
	}
	return map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
}
func tools() []tool {
	return []tool{
		{"vigil_status", "Show current status", schema(nil)}, {"vigil_history", "Show recent recorded events", schema(map[string]any{"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}})}, {"vigil_explain", "Explain the latest decision", schema(fields("session_id"))}, {"vigil_pause", "Pause interventions", schema(nil)}, {"vigil_resume", "Resume interventions", schema(nil)}, {"vigil_set_mode", "Set mode", schema(map[string]any{"mode": map[string]any{"type": "string", "enum": []string{"observe", "assist", "autonomous"}}})},
		{"vigil_budget", "Report observed usage and configured limits", schema(fields("session_id", "turn_id"))},
		{"vigil_correlate_session", "Resolve hook observations for a provider session within an execution window", schema(map[string]any{"schema_version": map[string]any{"type": "integer", "const": 1}, "session_id": stringSchema, "not_before": stringSchema, "not_after": stringSchema})},
		{"vigil_request_advisor", "Prepare a read-only advisor request for explicit terminal approval", schema(fields("session_id", "turn_id"))},
		{"vigil_record_evidence", "Attach evidence to an existing requirement", schema(map[string]any{"session_id": stringSchema, "task_id": stringSchema, "requirement_id": stringSchema, "evidence": schema(fields("kind", "source", "result", "state", "event_id"))})},
		{"vigil_verification", "Show requirement verification and missing evidence", schema(fields("session_id", "task_id"))},
		{"vigil_eval", "Run isolated deterministic detector checks", schema(nil)},
	}
}

var stringSchema = map[string]string{"type": "string"}

func fields(names ...string) map[string]any {
	p := make(map[string]any, len(names))
	for _, name := range names {
		p[name] = stringSchema
	}
	return p
}
func Serve(ctx context.Context, in io.Reader, out io.Writer, s control.Service) error {
	scan := bufio.NewScanner(in)
	scan.Buffer(make([]byte, 4096), 256*1024)
	enc := json.NewEncoder(out)
	for scan.Scan() {
		var req request
		if err := json.Unmarshal(scan.Bytes(), &req); err != nil {
			if err = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "Parse error"}}); err != nil {
				return err
			}
			continue
		}
		if len(req.ID) == 0 {
			continue
		}
		var result any
		var rpcErr any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "vigil", "version": "0.1.0"}}
		case "ping":
			result = map[string]any{}
		case "tools/list":
			result = map[string]any{"tools": tools()}
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if json.Unmarshal(req.Params, &p) != nil {
				rpcErr = map[string]any{"code": -32602, "message": "Invalid params"}
				break
			}
			var a struct {
				Limit         int               `json:"limit"`
				Mode          string            `json:"mode"`
				Session       string            `json:"session_id"`
				Turn          string            `json:"turn_id"`
				Task          string            `json:"task_id"`
				Requirement   string            `json:"requirement_id"`
				Evidence      recorder.Evidence `json:"evidence"`
				SchemaVersion int               `json:"schema_version"`
				NotBefore     string            `json:"not_before"`
				NotAfter      string            `json:"not_after"`
			}
			if len(p.Arguments) > 0 && json.Unmarshal(p.Arguments, &a) != nil {
				rpcErr = map[string]any{"code": -32602, "message": "Invalid arguments"}
				break
			}
			var value any
			var err error
			switch p.Name {
			case "vigil_status":
				value, err = s.Status(ctx)
			case "vigil_history":
				if a.Limit == 0 {
					a.Limit = 20
				}
				value, err = s.History(ctx, a.Limit)
			case "vigil_explain":
				value, err = s.Explain(ctx, a.Session)
			case "vigil_budget":
				value, err = s.Budget(ctx, a.Session, a.Turn, time.Now())
			case "vigil_correlate_session":
				if a.SchemaVersion != 1 {
					err = fmt.Errorf("unsupported correlation schema")
				} else {
					start, startErr := time.Parse(time.RFC3339Nano, a.NotBefore)
					end, endErr := time.Parse(time.RFC3339Nano, a.NotAfter)
					if startErr != nil || endErr != nil {
						err = fmt.Errorf("invalid correlation window")
					} else {
						value, err = s.Store.SessionObservations(ctx, a.Session, start, end)
					}
				}
			case "vigil_request_advisor":
				value, err = s.AdvisorPreview(ctx, a.Session, a.Turn)
			case "vigil_record_evidence":
				value, err = s.Evidence(ctx, a.Session, a.Task, a.Requirement, a.Evidence, false, time.Now())
			case "vigil_verification":
				value, err = s.Verification(ctx, a.Session, a.Task)
			case "vigil_eval":
				value, err = s.Eval(ctx)
			case "vigil_pause":
				err = s.SetPaused(ctx, true)
				value = "paused"
			case "vigil_resume":
				err = s.SetPaused(ctx, false)
				value = "resumed"
			case "vigil_set_mode":
				err = s.SetMode(ctx, a.Mode)
				value = a.Mode
			default:
				rpcErr = map[string]any{"code": -32602, "message": "Unknown tool"}
			}
			if rpcErr == nil {
				if err != nil {
					result = map[string]any{"isError": true, "content": []any{map[string]string{"type": "text", "text": err.Error()}}}
				} else {
					b, _ := json.Marshal(value)
					result = map[string]any{"content": []any{map[string]string{"type": "text", "text": string(b)}}}
				}
			}
		default:
			rpcErr = map[string]any{"code": -32601, "message": "Method not found"}
		}
		response := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if rpcErr != nil {
			response["error"] = rpcErr
		} else {
			response["result"] = result
		}
		if err := enc.Encode(response); err != nil {
			return fmt.Errorf("write response: %w", err)
		}
	}
	return scan.Err()
}
