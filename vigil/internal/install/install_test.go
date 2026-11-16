package install

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const existingConfig = `# preserve my settings
model = 'custom'
[mcp_servers.existing]
command = 'existing-command'
`

const existingHooks = `{"description":"existing", "hooks":{"Stop":[{"hooks":[{"type":"command","command":"existing-hook","timeout":4}]}],"FutureEvent":[{"hooks":[]}]}}`

func write(t *testing.T, home, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, name), []byte(value), 0640); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, home, name string) file {
	t.Helper()
	f, err := readFile(filepath.Join(home, name), maxBackupSize)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func run(t *testing.T, action, home, binary, config string) {
	t.Helper()
	if _, err := Run(action, home, binary, config); err != nil {
		t.Fatal(err)
	}
}

func TestInstallRollback(t *testing.T) {
	home := t.TempDir()
	binary := filepath.Join(home, "vigil ' $ and spaces")
	cfg := filepath.Join(home, "test '$ config.yaml")
	write(t, home, configFiles[0], existingConfig)
	write(t, home, configFiles[1], existingHooks)
	original := snapshot{read(t, home, configFiles[0]), read(t, home, configFiles[1])}
	run(t, "install", home, binary, cfg)
	state := read(t, home, backupName)
	if !state.Exists || state.Mode != 0600 {
		t.Fatal("missing private backup")
	}
	installed := snapshot{read(t, home, configFiles[0]), read(t, home, configFiles[1])}
	if !bytes.HasPrefix(installed[0].Data, original[0].Data) || !bytes.Contains(installed[1].Data, []byte("existing-hook")) || !bytes.Contains(installed[1].Data, []byte("FutureEvent")) {
		t.Fatal("installation lost existing configuration")
	}
	root, err := parseTOML(installed[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	registration := root["mcp_servers"].(map[string]any)["vigil"].(map[string]any)
	if registration["command"] != binary || registration["args"].([]any)[2] != cfg {
		t.Fatalf("incorrect MCP command: %v", registration)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	var hooks struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(installed[1].Data, &hooks); err != nil {
		t.Fatal(err)
	}
	if got := hooks.Hooks["Stop"][1].Hooks[0].Timeout; got != 45 {
		t.Fatalf("Stop timeout: %d", got)
	}
	if got := hooks.Hooks["PostToolUse"][0].Hooks[0].Timeout; got != 3 {
		t.Fatalf("tool timeout: %d", got)
	}
	out, err := exec.Command("/bin/sh", "-c", hooks.Hooks["Stop"][1].Hooks[0].Command).CombinedOutput()
	if err != nil || string(out) != "hook\n--config\n"+cfg+"\n" {
		t.Fatalf("incorrect shell command: %q, %v", out, err)
	}
	run(t, "install", home, binary, cfg)
	for i, name := range configFiles {
		if !read(t, home, name).equal(installed[i]) || installed[i].Mode != original[i].Mode {
			t.Fatalf("repeat install or file permissions changed %s", name)
		}
	}
	if !read(t, home, backupName).equal(state) {
		t.Fatal("idempotent install rewrote the backup")
	}
	run(t, "rollback", home, "", "")
	for i, name := range configFiles {
		if !read(t, home, name).equal(original[i]) {
			t.Fatalf("did not restore %s", name)
		}
	}
	if read(t, home, backupName).Exists {
		t.Fatal("rollback left backup")
	}
}

func TestUninstallPreservesLaterEdits(t *testing.T) {
	home := t.TempDir()
	write(t, home, configFiles[0], existingConfig)
	write(t, home, configFiles[1], existingHooks)
	run(t, "install", home, "/opt/vigil", "")
	later := "\n[mcp_servers.later]\ncommand = 'later-command'\n"
	write(t, home, configFiles[0], string(read(t, home, configFiles[0]).Data)+later)
	hooks := strings.Replace(string(read(t, home, configFiles[1]).Data), "existing-hook", "edited-hook", 1)
	write(t, home, configFiles[1], hooks)
	if _, err := Run("rollback", home, "", ""); err == nil {
		t.Fatal("rollback accepted subsequent edits")
	}
	run(t, "uninstall", home, "", "")
	run(t, "uninstall", home, "", "")
	if got := string(read(t, home, configFiles[0]).Data); got != existingConfig+later {
		t.Fatalf("uninstall changed user TOML: %q", got)
	}
	got := string(read(t, home, configFiles[1]).Data)
	if strings.Contains(got, "/opt/vigil") || !strings.Contains(got, "edited-hook") || !strings.Contains(got, "FutureEvent") {
		t.Fatalf("incorrect hook cleanup: %s", got)
	}
}

func TestUninstallAfterCodexReformatsMCP(t *testing.T) {
	home := t.TempDir()
	run(t, "install", home, "/opt/vigil", "/opt/config.yaml")
	config := `# user settings
[mcp_servers."vigil"]
command = "/opt/vigil"
args = [
  "mcp",
  "--config",
  "/opt/config.yaml",
]

# keep this adjacent comment
[features]
hooks = true
`
	write(t, home, configFiles[0], config)
	run(t, "install", home, "/opt/vigil", "/opt/config.yaml")
	run(t, "uninstall", home, "", "")
	want := "# user settings\n\n# keep this adjacent comment\n[features]\nhooks = true\n"
	if got := string(read(t, home, configFiles[0]).Data); got != want {
		t.Fatalf("uninstall changed user TOML: %q", got)
	}
}

func TestInstallRejectsConflictsBeforeWriting(t *testing.T) {
	for _, tc := range []struct{ name, config, hooks string }{
		{"existing server", "[mcp_servers.'vigil']\ncommand='another'\n", existingHooks},
		{"inline table", "mcp_servers = { other = { command = 'another' } }\n", existingHooks},
		{"malformed TOML", "[invalid", existingHooks},
		{"malformed hooks", existingConfig, `{"hooks":{"Stop":{}}}`},
		{"existing hook", existingConfig, `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"'/opt/vigil' hook"}]}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			write(t, home, configFiles[0], tc.config)
			write(t, home, configFiles[1], tc.hooks)
			if _, err := Run("install", home, "/opt/vigil", ""); err == nil {
				t.Fatal("accepted conflicting configuration")
			}
			if string(read(t, home, configFiles[0]).Data) != tc.config || string(read(t, home, configFiles[1]).Data) != tc.hooks || read(t, home, backupName).Exists {
				t.Fatal("conflict modified configuration")
			}
		})
	}
}

func TestInterruptedInstallAndUninstall(t *testing.T) {
	home := t.TempDir()
	statePath := filepath.Join(home, backupName)
	saved := backup{Version: 1, Binary: "/opt/vigil", Events: hookEvents}
	installed, err := saved.withRegistrations()
	if err != nil {
		t.Fatal(err)
	}
	saved.Pending = &transaction{Action: "install", After: installed}
	if _, err := save(statePath, file{}, saved); err != nil {
		t.Fatal(err)
	}
	if err := replace(filepath.Join(home, configFiles[0]), file{}, installed[0], maxConfigSize); err != nil {
		t.Fatal(err)
	}
	run(t, "install", home, saved.Binary, "")
	if !read(t, home, configFiles[1]).equal(installed[1]) {
		t.Fatal("retry did not finish installation")
	}
	saved.Pending = &transaction{Action: "uninstall", Before: installed}
	if _, err := save(statePath, read(t, home, backupName), saved); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(home, configFiles[0])); err != nil {
		t.Fatal(err)
	}
	run(t, "uninstall", home, "", "")
	for _, name := range []string{configFiles[0], configFiles[1], backupName} {
		if read(t, home, name).Exists {
			t.Fatalf("retry left %s", name)
		}
	}
}
