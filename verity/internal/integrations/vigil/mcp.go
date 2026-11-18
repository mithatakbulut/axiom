package vigil

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
)

const maxResponse = 256 * 1024

// MCPClient invokes a configured Vigil executable through its documented
// newline-delimited stdio MCP transport. It never reads Vigil's database.
type MCPClient struct {
	Command string
	Args    []string
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}
type rpcResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c MCPClient) Inspect(ctx context.Context, sessionID, turnID string) (Snapshot, error) {
	if c.Command == "" || sessionID == "" {
		return Snapshot{}, errors.New("Vigil command and explicit session ID are required")
	}
	cmd := exec.CommandContext(ctx, c.Command, c.Args...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return Snapshot{}, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return Snapshot{}, err
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return Snapshot{}, fmt.Errorf("start Vigil MCP: %w", err)
	}
	defer func() { _ = in.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	r := bufio.NewReader(io.LimitReader(out, maxResponse*8))
	call := func(id int, method string, params any, dst any) error {
		b, e := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
		if e != nil {
			return e
		}
		if _, e = fmt.Fprintf(in, "%s\n", b); e != nil {
			return e
		}
		line, e := r.ReadBytes('\n')
		if e != nil {
			return fmt.Errorf("read Vigil MCP response: %w", e)
		}
		if len(line) > maxResponse {
			return errors.New("Vigil MCP response exceeded size limit")
		}
		var response rpcResponse
		if e = json.Unmarshal(line, &response); e != nil || response.ID != id {
			return errors.New("invalid Vigil MCP response")
		}
		if response.Error != nil {
			return fmt.Errorf("Vigil MCP: %s", response.Error.Message)
		}
		return json.Unmarshal(response.Result, dst)
	}
	var initialized struct {
		ServerInfo struct{ Name, Version string } `json:"serverInfo"`
	}
	if err = call(1, "initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "verity", "version": "0.1.0"}}, &initialized); err != nil {
		return Snapshot{}, err
	}
	if initialized.ServerInfo.Name != "vigil" || initialized.ServerInfo.Version != "0.1.0" {
		return Snapshot{}, ErrIncompatible
	}
	var catalog struct {
		Tools []struct{ Name string } `json:"tools"`
	}
	if err = call(2, "tools/list", nil, &catalog); err != nil {
		return Snapshot{}, err
	}
	available := map[string]bool{}
	for _, t := range catalog.Tools {
		available[t.Name] = true
	}
	if !available["vigil_explain"] || !available["vigil_budget"] {
		return Snapshot{}, ErrIncompatible
	}
	callTool := func(id int, name string, args map[string]string) (json.RawMessage, error) {
		var result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if e := call(id, "tools/call", map[string]any{"name": name, "arguments": args}, &result); e != nil {
			return nil, e
		}
		if result.IsError || len(result.Content) == 0 {
			return nil, errors.New("Vigil MCP tool returned no usable result")
		}
		var raw json.RawMessage
		if e := json.Unmarshal([]byte(result.Content[0].Text), &raw); e != nil {
			return nil, errors.New("Vigil MCP tool returned invalid JSON")
		}
		return raw, nil
	}
	explain, err := callTool(3, "vigil_explain", map[string]string{"session_id": sessionID})
	if err != nil {
		return Snapshot{}, err
	}
	args := map[string]string{"session_id": sessionID}
	if turnID != "" {
		args["turn_id"] = turnID
	}
	budget, err := callTool(4, "vigil_budget", args)
	if err != nil {
		return Snapshot{}, err
	}
	var exp struct {
		Progress struct {
			Decision string   `json:"decision"`
			Signals  []string `json:"signals"`
		} `json:"latest_progress"`
		Advisor *struct {
			State string `json:"state"`
		} `json:"latest_advisor"`
	}
	if err = json.Unmarshal(explain, &exp); err != nil {
		return Snapshot{}, err
	}
	var budgetData struct {
		Warnings []string `json:"warnings"`
		Usage    struct {
			ModelRequests  int   `json:"observed_model_requests"`
			RuntimeSeconds int64 `json:"observed_runtime_seconds"`
		} `json:"usage"`
	}
	if err = json.Unmarshal(budget, &budgetData); err != nil {
		return Snapshot{}, err
	}
	advisorAction := ""
	if exp.Advisor != nil {
		advisorAction = exp.Advisor.State
	}
	progressJSON, _ := json.Marshal(exp.Progress)
	return Snapshot{VigilVersion: initialized.ServerInfo.Version, SessionID: sessionID, Signals: normalizeProgress(progressJSON), AdvisorAction: advisorAction, BudgetWarnings: unique(budgetData.Warnings), ObservedModelRequests: budgetData.Usage.ModelRequests, ObservedRuntimeSeconds: budgetData.Usage.RuntimeSeconds, CapturedAt: time.Now().UTC()}, nil
}

type Correlation struct {
	SchemaVersion int       `json:"schema_version"`
	SessionID     string    `json:"session_id"`
	TurnID        string    `json:"turn_id"`
	ObservedAt    time.Time `json:"observed_at"`
	VigilVersion  string    `json:"-"`
}

// CorrelateSession reads only Vigil's versioned public MCP session lookup. The
// caller supplies the provider session ID and bounded execution interval.
func (c MCPClient) CorrelateSession(ctx context.Context, sessionID string, start, end time.Time) ([]Correlation, error) {
	if c.Command == "" || sessionID == "" || start.IsZero() || end.Before(start) {
		return nil, errors.New("invalid Vigil session correlation request")
	}
	cmd := exec.CommandContext(ctx, c.Command, c.Args...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("start Vigil MCP: %w", err)
	}
	defer func() { _ = in.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	reader := bufio.NewReader(io.LimitReader(out, maxResponse*4))
	call := func(id int, method string, params any, dst any) error {
		body, marshalErr := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
		if marshalErr != nil {
			return marshalErr
		}
		if _, writeErr := fmt.Fprintf(in, "%s\n", body); writeErr != nil {
			return writeErr
		}
		line, readErr := reader.ReadBytes('\n')
		if readErr != nil {
			return readErr
		}
		if len(line) > maxResponse {
			return errors.New("Vigil MCP response exceeded size limit")
		}
		var response rpcResponse
		if json.Unmarshal(line, &response) != nil || response.ID != id {
			return errors.New("invalid Vigil MCP response")
		}
		if response.Error != nil {
			return fmt.Errorf("Vigil MCP: %s", response.Error.Message)
		}
		return json.Unmarshal(response.Result, dst)
	}
	var initialized struct {
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err = call(1, "initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "verity", "version": "0.1.0"}}, &initialized); err != nil {
		return nil, err
	}
	if initialized.ServerInfo.Name != "vigil" {
		return nil, ErrIncompatible
	}
	var catalog struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err = call(2, "tools/list", nil, &catalog); err != nil {
		return nil, err
	}
	available := false
	for _, item := range catalog.Tools {
		if item.Name == "vigil_correlate_session" {
			available = true
		}
	}
	if !available {
		return nil, ErrIncompatible
	}
	var toolResult struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	params := map[string]any{"name": "vigil_correlate_session", "arguments": map[string]any{"schema_version": 1, "session_id": sessionID, "not_before": start.UTC().Format(time.RFC3339Nano), "not_after": end.UTC().Format(time.RFC3339Nano)}}
	if err = call(3, "tools/call", params, &toolResult); err != nil {
		return nil, err
	}
	if toolResult.IsError || len(toolResult.Content) == 0 {
		return nil, errors.New("Vigil session correlation tool returned no usable result")
	}
	var records []Correlation
	if err = json.Unmarshal([]byte(toolResult.Content[0].Text), &records); err != nil {
		return nil, errors.New("Vigil returned invalid session observations")
	}
	for i := range records {
		r := &records[i]
		if r.SchemaVersion != 1 || r.SessionID != sessionID || r.TurnID == "" || r.ObservedAt.Before(start) || r.ObservedAt.After(end) {
			return nil, errors.New("Vigil returned an invalid session observation")
		}
		r.VigilVersion = initialized.ServerInfo.Version
	}
	return records, nil
}

// ValidateResult rejects MCP error payloads whose process exits without closing.
func ValidateResult(s Snapshot) error {
	if s.SessionID == "" || s.VigilVersion == "" || s.CapturedAt.IsZero() {
		return ErrIncompatible
	}
	return nil
}
