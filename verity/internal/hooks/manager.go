// Package hooks manages an opt-in, reversible local Git pre-commit hook.
package hooks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const statePath = ".verity/hooks-state-pre-commit.json"
const postCommitStatePath = ".verity/hooks-state-post-commit.json"
const managedMarker = "# verity-managed-pre-commit-v1"
const managedPostCommitMarker = "# verity-managed-post-commit-v1"
const maxHookBytes = 1 << 20

var (
	ErrExistingHook = errors.New("a Git hook already exists; pass --chain to preserve and run it")
	ErrHookDrift    = errors.New("managed hook changed since installation; refusing to overwrite or remove it")
	ErrHookSymlink  = errors.New("symbolic-link hooks are unsupported; refusing to replace them")
)

type state struct {
	Version         int    `json:"version"`
	Repository      string `json:"repository"`
	HookDir         string `json:"hook_dir"`
	HookPath        string `json:"hook_path"`
	Binary          string `json:"verity_binary"`
	InstalledSHA256 string `json:"installed_sha256"`
	OriginalExists  bool   `json:"original_exists"`
	OriginalSHA256  string `json:"original_sha256,omitempty"`
	OriginalMode    uint32 `json:"original_mode,omitempty"`
	Original        []byte `json:"original,omitempty"`
	BackupPath      string `json:"backup_path,omitempty"`
}

type Status struct {
	Installed bool   `json:"installed"`
	Active    bool   `json:"active"`
	HookPath  string `json:"hook_path,omitempty"`
	Message   string `json:"message"`
}

func Install(ctx context.Context, repository, binary string, chain bool) (Status, error) {
	return installHook(ctx, repository, binary, chain, "pre-commit", statePath, managedMarker, "run")
}

func InstallPostCommit(ctx context.Context, repository, binary string, chain bool) (Status, error) {
	return installHook(ctx, repository, binary, chain, "post-commit", postCommitStatePath, managedPostCommitMarker, "post-commit")
}

func installHook(ctx context.Context, repository, binary string, chain bool, hookName, stateName, marker, command string) (Status, error) {
	repo, hookDir, err := resolveHookDir(ctx, repository)
	if err != nil {
		return Status{}, err
	}
	if binary == "" {
		return Status{}, errors.New("Verity executable path is required")
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return Status{}, err
	}
	if err := os.MkdirAll(hookDir, 0755); err != nil {
		return Status{}, err
	}
	hookPath := filepath.Join(hookDir, hookName)
	stateFile := filepath.Join(repo, stateName)
	current, info, exists, err := readHook(hookPath)
	if err != nil {
		return Status{}, err
	}
	if existing, err := readState(stateFile); err == nil {
		if existing.Repository != repo || existing.HookPath != hookPath {
			return Status{}, ErrHookDrift
		}
		if exists && digest(current) == existing.InstalledSHA256 {
			return Status{Installed: true, Active: true, HookPath: hookPath, Message: "Axiom " + hookName + " hook is already installed."}, nil
		}
		if !matchesOriginal(current, exists, existing) {
			return Status{}, ErrHookDrift
		}
		script := wrapper(binary, existing.BackupPath, existing.OriginalExists, marker, command)
		existing.Binary = binary
		existing.InstalledSHA256 = digest(script)
		if err := writeState(stateFile, existing); err != nil {
			return Status{}, err
		}
		if err := atomicWrite(hookPath, script, 0755); err != nil {
			return Status{}, err
		}
		return Status{Installed: true, Active: true, HookPath: hookPath, Message: "Axiom " + hookName + " hook installed."}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Status{}, err
	}
	if exists && !chain {
		return Status{}, ErrExistingHook
	}
	if exists && info.Mode()&os.ModeSymlink != 0 {
		return Status{}, ErrHookSymlink
	}
	if exists && len(current) > maxHookBytes {
		return Status{}, errors.New("existing hook exceeds the 1 MiB preservation limit")
	}
	backupPath := ""
	if exists {
		backupPath = filepath.Join(repo, ".verity", "hooks", hookName+".original")
		if err := atomicWrite(backupPath, current, info.Mode().Perm()); err != nil {
			return Status{}, err
		}
	}
	script := wrapper(binary, backupPath, exists, marker, command)
	s := state{
		Version: 1, Repository: repo, HookDir: hookDir, HookPath: hookPath,
		Binary: binary, InstalledSHA256: digest(script), OriginalExists: exists,
		BackupPath: backupPath,
	}
	if exists {
		s.OriginalSHA256 = digest(current)
		s.OriginalMode = uint32(info.Mode().Perm())
		s.Original = current
	}
	if err := writeState(stateFile, s); err != nil {
		if backupPath != "" {
			_ = os.Remove(backupPath)
		}
		return Status{}, err
	}
	if err := atomicWrite(hookPath, script, 0755); err != nil {
		_ = os.Remove(stateFile)
		if backupPath != "" {
			_ = os.Remove(backupPath)
		}
		return Status{}, err
	}
	return Status{Installed: true, Active: true, HookPath: hookPath, Message: "Axiom " + hookName + " hook installed."}, nil
}

func Inspect(ctx context.Context, repository string) (Status, error) {
	return inspectHook(ctx, repository, statePath)
}

func InspectPostCommit(ctx context.Context, repository string) (Status, error) {
	return inspectHook(ctx, repository, postCommitStatePath)
}

func inspectHook(ctx context.Context, repository, stateName string) (Status, error) {
	repo, activeDir, err := resolveHookDir(ctx, repository)
	if err != nil {
		return Status{}, err
	}
	s, err := readState(filepath.Join(repo, stateName))
	if errors.Is(err, os.ErrNotExist) {
		return Status{Message: "Axiom managed hook is not installed."}, nil
	}
	if err != nil {
		return Status{}, err
	}
	data, _, exists, err := readHook(s.HookPath)
	if err != nil {
		return Status{}, err
	}
	if !exists || digest(data) != s.InstalledSHA256 {
		return Status{Installed: true, Active: false, HookPath: s.HookPath, Message: "The managed hook is missing or has been modified."}, nil
	}
	active := filepath.Clean(activeDir) == filepath.Clean(s.HookDir)
	message := "Axiom managed hook is installed and active."
	if !active {
		message = "Axiom hook file exists, but the repository's active core.hooksPath has changed."
	}
	return Status{Installed: true, Active: active, HookPath: s.HookPath, Message: message}, nil
}

func Uninstall(ctx context.Context, repository string) (Status, error) {
	return uninstallHook(ctx, repository, statePath)
}

func UninstallPostCommit(ctx context.Context, repository string) (Status, error) {
	return uninstallHook(ctx, repository, postCommitStatePath)
}

func uninstallHook(ctx context.Context, repository, stateName string) (Status, error) {
	repo, _, err := resolveHookDir(ctx, repository)
	if err != nil {
		return Status{}, err
	}
	stateFile := filepath.Join(repo, stateName)
	s, err := readState(stateFile)
	if errors.Is(err, os.ErrNotExist) {
		return Status{Message: "Axiom managed hook is not installed."}, nil
	}
	if err != nil {
		return Status{}, err
	}
	current, _, exists, err := readHook(s.HookPath)
	if err != nil {
		return Status{}, err
	}
	if exists && digest(current) == s.InstalledSHA256 {
		if s.OriginalExists {
			backup, err := os.ReadFile(s.BackupPath)
			if err != nil || digest(backup) != s.OriginalSHA256 || digest(backup) != digest(s.Original) {
				return Status{}, ErrHookDrift
			}
			if err := atomicWrite(s.HookPath, backup, os.FileMode(s.OriginalMode)); err != nil {
				return Status{}, err
			}
		} else if err := os.Remove(s.HookPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Status{}, err
		}
	} else if !matchesOriginal(current, exists, s) {
		return Status{}, ErrHookDrift
	}
	if s.BackupPath != "" {
		if err := os.Remove(s.BackupPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Status{}, err
		}
		_ = os.Remove(filepath.Dir(s.BackupPath))
	}
	if err := os.Remove(stateFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Status{}, err
	}
	return Status{Message: "Axiom managed hook removed; any previous hook was restored."}, nil
}

func resolveHookDir(ctx context.Context, repository string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repository, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("resolve Git repository: %w", err)
	}
	repo, err := filepath.Abs(strings.TrimSpace(string(out)))
	if err != nil {
		return "", "", err
	}
	repo, err = filepath.EvalSymlinks(repo)
	if err != nil {
		return "", "", err
	}
	config := exec.CommandContext(ctx, "git", "-C", repo, "config", "--path", "--get", "core.hooksPath")
	configured, configErr := config.Output()
	if configErr == nil {
		hooksDir := strings.TrimSpace(string(configured))
		if hooksDir == "" || hooksDir == os.DevNull {
			return "", "", errors.New("Git core.hooksPath disables hooks or is empty")
		}
		if !filepath.IsAbs(hooksDir) {
			hooksDir = filepath.Join(repo, hooksDir)
		}
		hooksDir, err = filepath.Abs(hooksDir)
		return repo, filepath.Clean(hooksDir), err
	}
	var exitErr *exec.ExitError
	if !errors.As(configErr, &exitErr) || exitErr.ExitCode() != 1 {
		return "", "", fmt.Errorf("resolve Git core.hooksPath: %w", configErr)
	}
	gitPath := exec.CommandContext(ctx, "git", "-C", repo, "rev-parse", "--path-format=absolute", "--git-path", "hooks")
	configured, err = gitPath.Output()
	if err != nil {
		return "", "", fmt.Errorf("resolve default Git hooks directory: %w", err)
	}
	hooksDir := strings.TrimSpace(string(configured))
	if !filepath.IsAbs(hooksDir) {
		hooksDir = filepath.Join(repo, hooksDir)
	}
	hooksDir, err = filepath.Abs(hooksDir)
	return repo, filepath.Clean(hooksDir), err
}

func wrapper(binary, previous string, hasPrevious bool, marker, command string) []byte {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString(marker + "\n")
	if hasPrevious {
		b.WriteString(shellQuote(previous) + " \"$@\"\n")
		b.WriteString("status=$?\n[ \"$status\" -eq 0 ] || exit \"$status\"\n")
	}
	b.WriteString("exec " + shellQuote(binary) + " hooks " + command + "\n")
	return []byte(b.String())
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
func digest(data []byte) string      { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func matchesOriginal(current []byte, exists bool, s state) bool {
	if !s.OriginalExists {
		return !exists
	}
	return exists && digest(current) == s.OriginalSHA256
}

func readHook(path string) ([]byte, os.FileInfo, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, info, true, ErrHookSymlink
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, info, true, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxHookBytes+1))
	if err != nil {
		return nil, info, true, err
	}
	if len(data) > maxHookBytes {
		return nil, info, true, errors.New("existing hook exceeds the 1 MiB preservation limit")
	}
	return data, info, true, nil
}

func readState(path string) (state, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return state{}, err
	}
	var s state
	if err := json.Unmarshal(b, &s); err != nil || s.Version != 1 || s.Repository == "" || s.HookPath == "" || s.InstalledSHA256 == "" {
		return state{}, errors.New("invalid Verity hook state")
	}
	return s, nil
}

func writeState(path string, s state) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(b, '\n'), 0600)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".verity-hook-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
