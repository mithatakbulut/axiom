package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

func shellWord(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func (s backup) hookCommand() string {
	command := shellWord(s.Binary) + " hook"
	if s.Config != "" {
		command += " --config " + shellWord(s.Config)
	}
	return command
}

func (s backup) mcpBlock() []byte {
	args := []string{"mcp"}
	if s.Config != "" {
		args = append(args, "--config", s.Config)
	}
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = tomlString(arg)
	}
	return fmt.Appendf(nil, "\n\n[mcp_servers.vigil]\ncommand = %s\nargs = [%s]\n",
		tomlString(s.Binary), strings.Join(quoted, ", "))
}

func tomlString(s string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			out.WriteByte('\\')
			out.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&out, `\u%04X`, r)
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return out.String()
}

func parseTOML(data []byte) (map[string]any, error) {
	var root map[string]any
	if err := toml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("config.toml is invalid TOML or conflicts with the Vigil MCP table")
	}
	return root, nil
}

func equalTOML(left, right map[string]any) bool {
	a, err := toml.Marshal(left)
	if err != nil {
		return false
	}
	b, err := toml.Marshal(right)
	return err == nil && bytes.Equal(a, b)
}

func (s backup) editMCP(data []byte, add bool) ([]byte, error) {
	root, err := parseTOML(data)
	if err != nil {
		return nil, err
	}
	servers, _ := root["mcp_servers"].(map[string]any)
	_, exists := servers["vigil"]
	block := s.mcpBlock()
	if !add {
		if !exists {
			return data, nil
		}
		desired, err := parseTOML(block)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(servers["vigil"], desired["mcp_servers"].(map[string]any)["vigil"]) {
			return nil, fmt.Errorf("the Vigil MCP registration was edited, remove or restore its table before retrying")
		}
		var updated []byte
		if bytes.Count(data, block) == 1 {
			updated = bytes.Replace(data, block, nil, 1)
		} else {
			updated, err = removeMCPTable(data)
			if err != nil {
				return nil, err
			}
		}
		remaining, err := parseTOML(updated)
		if err != nil {
			return nil, err
		}
		delete(servers, "vigil")
		if len(servers) == 0 {
			// The TOML parser may synthesize a parent for the removed table.
			if _, exists := remaining["mcp_servers"]; !exists {
				delete(root, "mcp_servers")
			}
		}
		if !equalTOML(remaining, root) {
			return nil, fmt.Errorf("removing the Vigil MCP table would change other TOML settings")
		}
		return updated, nil
	}
	if exists {
		return nil, fmt.Errorf("an MCP server named vigil already exists in config.toml")
	}
	updated := append(bytes.Clone(data), block...)
	if _, err := parseTOML(updated); err != nil {
		return nil, err
	}
	return updated, nil
}

// Codex can reformat config.toml when saving hook trust or other settings.
// Remove only the managed table header and its assignments in that case.
func removeMCPTable(data []byte) ([]byte, error) {
	var parser unstable.Parser
	parser.Reset(data)
	var output bytes.Buffer
	removedUntil := 0
	inVigil := false
	for parser.NextExpression() {
		node := parser.Expression()
		keys := node.Key()
		keys.Next()
		first := keys.Node()
		if node.Kind == unstable.Table || node.Kind == unstable.ArrayTable {
			inVigil = string(first.Data) == "mcp_servers" &&
				keys.Next() && string(keys.Node().Data) == "vigil" && !keys.Next()
		}
		if !inVigil {
			continue
		}
		start := bytes.LastIndexByte(data[:first.Raw.Offset], '\n') + 1
		last := int(first.Raw.Offset)
		if node.Kind == unstable.KeyValue {
			last = int(node.Raw.Offset + node.Raw.Length)
		}
		if newline := bytes.IndexByte(data[last:], '\n'); newline >= 0 {
			last += newline + 1
		} else {
			last = len(data)
		}
		output.Write(data[removedUntil:start])
		removedUntil = last
	}
	if parser.Error() != nil || removedUntil == 0 {
		return nil, fmt.Errorf("could not safely locate the Vigil MCP table")
	}
	output.Write(data[removedUntil:])
	return output.Bytes(), nil
}

type hookGroup struct {
	Hooks []hookHandler `json:"hooks"`
}

type hookHandler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

func (s backup) editHooks(data []byte, add, requireAll bool) ([]byte, error) {
	root := make(map[string]json.RawMessage)
	if len(data) != 0 {
		if err := json.Unmarshal(data, &root); err != nil || root == nil {
			return nil, fmt.Errorf("hooks.json must contain a valid JSON object")
		}
	}
	events := make(map[string]json.RawMessage)
	if raw, exists := root["hooks"]; exists {
		if err := json.Unmarshal(raw, &events); err != nil || events == nil {
			return nil, fmt.Errorf("hooks.json must contain a hooks object")
		}
	}
	command := s.hookCommand()
	changed := false
	for _, event := range s.Events {
		timeout := 3
		if event == "Stop" && s.Version >= 2 {
			timeout = 45
		}
		group := hookGroup{Hooks: []hookHandler{{Type: "command", Command: command, Timeout: timeout}}}
		expected, _ := json.Marshal(group)
		var groups []json.RawMessage
		if raw, exists := events[event]; exists {
			if err := json.Unmarshal(raw, &groups); err != nil || groups == nil {
				return nil, fmt.Errorf("hooks.json entry for %s must be an array", event)
			}
		}
		matches := 0
		kept := groups[:0]
		for _, candidate := range groups {
			var entry hookGroup
			if err := json.Unmarshal(candidate, &entry); err != nil {
				return nil, fmt.Errorf("hooks.json has an invalid %s matcher group", event)
			}
			found := false
			for _, handler := range entry.Hooks {
				found = found || handler.Command == command
			}
			if !found {
				kept = append(kept, candidate)
				continue
			}
			matches++
			var actual, wanted any
			_ = json.Unmarshal(candidate, &actual)
			_ = json.Unmarshal(expected, &wanted)
			if add || matches != 1 || !reflect.DeepEqual(actual, wanted) {
				return nil, fmt.Errorf("hooks.json contains a conflicting or edited Vigil command for %s", event)
			}
		}
		if requireAll && matches == 0 {
			return nil, fmt.Errorf("missing Vigil hook for %s, uninstall before reinstalling", event)
		}
		if add {
			kept = append(kept, expected)
		}
		if add || matches > 0 {
			changed = true
			if len(kept) == 0 {
				delete(events, event)
			} else {
				events[event], _ = json.Marshal(kept)
			}
		}
	}
	if !changed {
		return data, nil
	}
	root["hooks"], _ = json.Marshal(events)
	updated, err := json.MarshalIndent(root, "", "  ")
	return append(updated, '\n'), err
}
