// Package install registers Vigil in one Codex configuration directory.
package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

const backupName = ".vigil-install-backup.json"
const maxBackupSize = 12 * maxConfigSize

var configFiles = [2]string{"config.toml", "hooks.json"}

// These events are recognized by the baseline Codex CLI, 0.147.0.
// Newer model-request and Interrupt events are accepted by the recorder but
// are not recognized by that release's hook registry.
var hookEvents = []string{
	"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse",
	"SubagentStart", "SubagentStop", "PreCompact", "PostCompact", "Stop",
	"SessionEnd", "PermissionRequest",
}

type snapshot [len(configFiles)]file

type transaction struct {
	Action string   `json:"action"`
	Before snapshot `json:"before"`
	After  snapshot `json:"after"`
}

type backup struct {
	Version   int          `json:"version"`
	Binary    string       `json:"binary"`
	Config    string       `json:"config,omitempty"`
	Events    []string     `json:"events"`
	Original  snapshot     `json:"original"`
	Installed *snapshot    `json:"installed,omitempty"`
	Pending   *transaction `json:"pending,omitempty"`
}

func Home() (string, error) {
	if path := os.Getenv("CODEX_HOME"); path != "" {
		return filepath.Abs(path)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

// Run installs, removes, or restores Vigil's registrations. Hook trust is
// managed separately by Codex.
func Run(action, home, binary, config string) (string, error) {
	if action != "install" && action != "uninstall" && action != "rollback" {
		return "", fmt.Errorf("unknown installation action")
	}
	if action == "install" {
		if err := os.MkdirAll(home, 0700); err != nil {
			return "", err
		}
	}
	dir, err := os.Open(home)
	if errors.Is(err, os.ErrNotExist) && action != "install" {
		return "Vigil is not installed", nil
	}
	if err != nil {
		return "", err
	}
	defer func() { _ = dir.Close() }()
	if err = syscall.Flock(int(dir.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return "", fmt.Errorf("could not lock the Codex configuration directory: %w", err)
	}
	statePath := filepath.Join(home, backupName)
	state, err := readFile(statePath, maxBackupSize)
	if err != nil {
		return "", err
	}
	if !state.Exists && action != "install" {
		return "Vigil is not installed", nil
	}
	var saved backup
	if state.Exists {
		if err := json.Unmarshal(state.Data, &saved); err != nil || (saved.Version != 1 && saved.Version != 2) || !filepath.IsAbs(saved.Binary) || len(saved.Events) == 0 {
			return "", fmt.Errorf("invalid Vigil installation backup")
		}
		if saved.Pending != nil {
			// Reconcile an interrupted operation before starting a new one.
			if err := finish(home, statePath, state, saved); err != nil {
				return "", err
			}
			if saved.Pending.Action == action && (action != "install" || (binary == saved.Binary && config == saved.Config)) {
				return success(action), nil
			}
			state, err = readFile(statePath, maxBackupSize)
			if err != nil {
				return "", err
			}
			if !state.Exists && action != "install" {
				return "Vigil is not installed", nil
			}
		}
	}
	var current snapshot
	for i, name := range configFiles {
		current[i], err = readFile(filepath.Join(home, name), maxConfigSize)
		if err != nil {
			return "", err
		}
	}
	var next snapshot
	switch action {
	case "install":
		if state.Exists {
			if binary != saved.Binary || config != saved.Config {
				return "", fmt.Errorf("an installation already exists in this directory, uninstall it before changing paths")
			}
			mcp, err := saved.editMCP(current[0].Data, false)
			if err != nil {
				return "", err
			}
			if slices.Equal(mcp, current[0].Data) {
				return "", fmt.Errorf("incomplete Vigil registration, uninstall it before reinstalling")
			}
			if _, err := saved.editHooks(current[1].Data, false, true); err != nil {
				return "", err
			}
			return success(action), nil
		}
		if !filepath.IsAbs(binary) || (config != "" && !filepath.IsAbs(config)) || strings.ContainsAny(binary+config, "\r\n") {
			return "", fmt.Errorf("the Vigil binary and explicit configuration require absolute paths without newlines")
		}
		saved = backup{Version: 2, Binary: binary, Config: config, Events: hookEvents, Original: current}
		next, err = saved.withRegistrations()
		saved.Installed = &next
	case "uninstall":
		next, err = saved.withoutRegistrations(current)
	case "rollback":
		var installed snapshot
		installed, err = saved.installedFiles()
		if err == nil {
			for i, f := range current {
				if !f.equal(installed[i]) && !f.equal(saved.Original[i]) {
					return "", fmt.Errorf("%s changed since installation, use uninstall to preserve unrelated edits", configFiles[i])
				}
			}
			next = saved.Original
		}
	}
	if err != nil {
		return "", err
	}
	for _, f := range next {
		if len(f.Data) > maxConfigSize {
			return "", fmt.Errorf("resulting Codex configuration exceeds the size limit")
		}
	}
	saved.Pending = &transaction{Action: action, Before: current, After: next}
	pending, err := save(statePath, state, saved)
	if err != nil {
		return "", err
	}
	if err := finish(home, statePath, pending, saved); err != nil {
		return "", err
	}
	return success(action), nil
}

func success(action string) string {
	switch action {
	case "install":
		return "Vigil is registered. Review hooks in Codex with /hooks or Settings > Hooks."
	case "rollback":
		return "Pre-installation Codex configuration restored"
	default:
		return "Vigil registrations removed"
	}
}

func (s backup) withRegistrations() (snapshot, error) {
	mcp, err := s.editMCP(s.Original[0].Data, true)
	if err != nil {
		return snapshot{}, err
	}
	hooks, err := s.editHooks(s.Original[1].Data, true, false)
	if err != nil {
		return snapshot{}, err
	}
	return snapshot{s.Original[0].withData(mcp), s.Original[1].withData(hooks)}, nil
}

func (s backup) installedFiles() (snapshot, error) {
	if s.Installed != nil {
		return *s.Installed, nil
	}
	return s.withRegistrations()
}

func (s backup) withoutRegistrations(current snapshot) (snapshot, error) {
	installed, err := s.installedFiles()
	if err != nil {
		return snapshot{}, err
	}
	mcp, err := s.editMCP(current[0].Data, false)
	if err != nil {
		return snapshot{}, err
	}
	hooks, err := s.editHooks(current[1].Data, false, false)
	if err != nil {
		return snapshot{}, err
	}
	result := current
	for i, edited := range [][]byte{mcp, hooks} {
		switch {
		case current[i].equal(installed[i]):
			result[i] = s.Original[i]
		case !slices.Equal(current[i].Data, edited):
			result[i] = current[i].withData(edited)
		}
	}
	return result, nil
}

func save(path string, before file, s backup) (file, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return file{}, err
	}
	if len(data) > maxBackupSize {
		return file{}, fmt.Errorf("the Vigil installation backup exceeds the size limit")
	}
	after := file{}.withData(data)
	return after, replace(path, before, after, maxBackupSize)
}

func finish(home, statePath string, state file, s backup) error {
	p := s.Pending
	if p.Action != "install" && p.Action != "uninstall" && p.Action != "rollback" {
		return fmt.Errorf("invalid operation in the Vigil installation backup")
	}
	var current snapshot
	for i, name := range configFiles {
		path := filepath.Join(home, name)
		f, err := readFile(path, maxConfigSize)
		if err != nil {
			return err
		}
		if !f.equal(p.Before[i]) && !f.equal(p.After[i]) {
			return fmt.Errorf("%s changed during an interrupted Vigil %s; backup retained at %s", name, p.Action, statePath)
		}
		current[i] = f
	}
	for i, name := range configFiles {
		if err := replace(filepath.Join(home, name), current[i], p.After[i], maxConfigSize); err != nil {
			return fmt.Errorf("interrupted Vigil %s; rerun after resolving the error: %w", p.Action, err)
		}
	}
	if p.Action != "install" {
		return replace(statePath, state, file{}, maxBackupSize)
	}
	s.Pending = nil
	_, err := save(statePath, state, s)
	return err
}
