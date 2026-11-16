// Package workspace manages isolated Git worktrees owned by Verity.
package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var ErrUnsafe = errors.New("unsafe workspace operation")

const marker = ".verity-workspace-owner"

type Git struct{ Binary string }
type Workspace struct{ Path, Branch, BaseSHA string }

func (g Git) bin() string {
	if g.Binary != "" {
		return g.Binary
	}
	return "git"
}
func (g Git) command(ctx context.Context, dir string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, g.bin(), args...)
	c.Dir = dir
	var out, stderr bytes.Buffer
	c.Stdout = &out
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(out.String()), nil
}
func (g Git) Root(ctx context.Context, path string) (string, error) {
	root, err := g.command(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(root)
}
func (g Git) Head(ctx context.Context, path string) (string, error) {
	return g.command(ctx, path, "rev-parse", "HEAD")
}
func (g Git) Status(ctx context.Context, path string) (string, error) {
	return g.command(ctx, path, "status", "--porcelain", "--untracked-files=all")
}

// Create adds a detached worktree at the exact current HEAD. The destination must not exist.
func (g Git) Create(ctx context.Context, repo, path string) (Workspace, error) {
	root, err := g.Root(ctx, repo)
	if err != nil {
		return Workspace{}, err
	}
	base, err := g.Head(ctx, root)
	if err != nil {
		return Workspace{}, err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return Workspace{}, err
	}
	if _, err = os.Lstat(path); err == nil {
		return Workspace{}, fmt.Errorf("%w: destination already exists", ErrUnsafe)
	} else if !os.IsNotExist(err) {
		return Workspace{}, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return Workspace{}, err
	}
	if strings.HasPrefix(path, root+string(filepath.Separator)) || path == root {
		return Workspace{}, fmt.Errorf("%w: workspace must be outside repository", ErrUnsafe)
	}
	if _, err = g.command(ctx, root, "worktree", "add", "--detach", "--", path, base); err != nil {
		return Workspace{}, err
	}
	if err = os.WriteFile(path+marker, []byte("verity\n"), 0600); err != nil {
		_, _ = g.command(ctx, root, "worktree", "remove", "--force", "--", path)
		return Workspace{}, err
	}
	return Workspace{Path: path, BaseSHA: base}, nil
}

// Cleanup removes only a marked, clean worktree whose git metadata resolves to repo.
// Dirty changes are preserved and cause a refusal.
func (g Git) Cleanup(ctx context.Context, repo, path string) error {
	root, err := g.Root(ctx, repo)
	if err != nil {
		return err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	if resolved == root || strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return ErrUnsafe
	}
	b, err := os.ReadFile(path + marker)
	if err != nil || string(b) != "verity\n" {
		return ErrUnsafe
	}
	common, err := g.command(ctx, resolved, "rev-parse", "--git-common-dir")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(resolved, common)
	}
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return err
	}
	rootCommon, err := g.command(ctx, root, "rev-parse", "--git-common-dir")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(rootCommon) {
		rootCommon = filepath.Join(root, rootCommon)
	}
	rootCommon, err = filepath.EvalSymlinks(rootCommon)
	if err != nil {
		return err
	}
	if common != rootCommon {
		return ErrUnsafe
	}
	status, err := g.Status(ctx, resolved)
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("%w: worktree has user changes", ErrUnsafe)
	}
	if _, err = g.command(ctx, root, "worktree", "remove", "--", resolved); err != nil {
		return err
	}
	return os.Remove(path + marker)
}
