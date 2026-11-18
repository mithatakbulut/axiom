package verification

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runIndexGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func initIndexRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runIndexGit(t, repo, "init")
	runIndexGit(t, repo, "config", "user.email", "test@example.com")
	runIndexGit(t, repo, "config", "user.name", "Test")
	return repo
}

func writeIndexFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestIndexSnapshotUsesStagedTree(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	name := filepath.Join(repo, "file with space")
	if err := os.WriteFile(name, []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "base")
	if err := os.WriteFile(name, []byte("staged"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	index := filepath.Join(repo, ".git", "index")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte("unstaged"), 0600); err != nil {
		t.Fatal(err)
	}
	candidate, err := CaptureIndex(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	dir, cleanup, err := (IndexSnapshotter{}).Snapshot(context.Background(), repo, candidate.Tree)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	data, err := os.ReadFile(filepath.Join(dir, "file with space"))
	if err != nil || string(data) != "staged" {
		t.Fatalf("snapshot %q: %v", data, err)
	}
	after, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("capture modified index")
	}
	if err := CheckIndexCurrent(context.Background(), repo, candidate); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	if err := CheckIndexCurrent(context.Background(), repo, candidate); err != ErrCandidateChanged {
		t.Fatalf("changed index: %v", err)
	}
}

func TestIndexSnapshotModesAndSplitIndex(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "script"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("script", filepath.Join(repo, "link")); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "base")
	git("update-index", "--split-index")
	index := filepath.Join(repo, ".git", "index")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := CaptureIndex(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	dir, cleanup, err := (IndexSnapshotter{}).Snapshot(context.Background(), repo, candidate.Tree)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	info, err := os.Stat(filepath.Join(dir, "script"))
	if err != nil || info.Mode()&0111 == 0 {
		t.Fatalf("executable mode: %v %v", info, err)
	}
	link, err := os.Readlink(filepath.Join(dir, "link"))
	if err != nil || link != "script" {
		t.Fatalf("link %q: %v", link, err)
	}
	after, err := os.ReadFile(index)
	if err != nil || string(before) != string(after) {
		t.Fatalf("index changed: %v", err)
	}
}

func TestCaptureIndexResolvesNestedRepositoryAndCompleteTreeSemantics(t *testing.T) {
	repo := initIndexRepo(t)
	writeIndexFile(t, filepath.Join(repo, "old name.txt"), "rename me\n")
	writeIndexFile(t, filepath.Join(repo, "delete.txt"), "delete me\n")
	runIndexGit(t, repo, "add", ".")
	runIndexGit(t, repo, "commit", "-m", "base")
	base := runIndexGit(t, repo, "rev-parse", "HEAD")
	if err := os.Rename(filepath.Join(repo, "old name.txt"), filepath.Join(repo, "renamed ü file.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, "delete.txt")); err != nil {
		t.Fatal(err)
	}
	runIndexGit(t, repo, "add", "-A")
	writeIndexFile(t, filepath.Join(repo, "nested dir", "new staged ü.txt"), "staged addition\n")
	runIndexGit(t, repo, "add", "nested dir/new staged ü.txt")
	writeIndexFile(t, filepath.Join(repo, "untracked.txt"), "not in candidate\n")
	index := filepath.Join(repo, ".git", "index")
	indexBefore, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(repo, "nested")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	statusBefore := runIndexGit(t, repo, "status", "--porcelain=v1", "-z")
	candidate, err := CaptureIndex(context.Background(), nested)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := CaptureIndex(context.Background(), repo)
	if err != nil || repeated != candidate {
		t.Fatalf("same index identity changed: %#v %#v %v", candidate, repeated, err)
	}
	dir, cleanup, err := (IndexSnapshotter{}).Snapshot(context.Background(), repo, candidate.Tree)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if data, err := os.ReadFile(filepath.Join(dir, "renamed ü file.txt")); err != nil || string(data) != "rename me\n" {
		t.Fatalf("renamed staged file: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "old name.txt")); !os.IsNotExist(err) {
		t.Fatalf("old path remains in candidate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "delete.txt")); !os.IsNotExist(err) {
		t.Fatalf("staged deletion remains in candidate: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "nested dir", "new staged ü.txt")); err != nil || string(data) != "staged addition\n" {
		t.Fatalf("staged addition: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "untracked.txt")); !os.IsNotExist(err) {
		t.Fatalf("untracked file included in candidate: %v", err)
	}
	if candidate.Base != base {
		t.Fatalf("base %s, want %s", candidate.Base, base)
	}
	indexAfter, err := os.ReadFile(index)
	if err != nil || string(indexBefore) != string(indexAfter) {
		t.Fatalf("index changed: %v", err)
	}
	if statusAfter := runIndexGit(t, repo, "status", "--porcelain=v1", "-z"); statusAfter != statusBefore {
		t.Fatalf("working-tree/index status changed: %q != %q", statusAfter, statusBefore)
	}
}

func TestCaptureIndexSupportsLinkedWorktreeAndCommonDirectory(t *testing.T) {
	repo := initIndexRepo(t)
	writeIndexFile(t, filepath.Join(repo, "tracked.txt"), "base\n")
	runIndexGit(t, repo, "add", "tracked.txt")
	runIndexGit(t, repo, "commit", "-m", "base")
	worktree := filepath.Join(t.TempDir(), "linked worktree")
	runIndexGit(t, repo, "worktree", "add", "-b", "linked", worktree, "HEAD")
	writeIndexFile(t, filepath.Join(worktree, "tracked.txt"), "staged in linked tree\n")
	runIndexGit(t, worktree, "add", "tracked.txt")
	runIndexGit(t, worktree, "update-index", "--split-index")
	indexPath := runIndexGit(t, worktree, "rev-parse", "--path-format=absolute", "--git-path", "index")
	indexBefore, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := CaptureIndex(context.Background(), worktree)
	if err != nil {
		t.Fatal(err)
	}
	dir, cleanup, err := (IndexSnapshotter{}).Snapshot(context.Background(), worktree, candidate.Tree)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "tracked.txt"))
	cleanup()
	if err != nil || string(data) != "staged in linked tree\n" {
		t.Fatalf("linked worktree snapshot: %q %v", data, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("snapshot cleanup did not remove %s: %v", dir, err)
	}
	indexAfter, err := os.ReadFile(indexPath)
	if err != nil || string(indexBefore) != string(indexAfter) {
		t.Fatalf("linked worktree index changed: %v", err)
	}
}

func TestCaptureIndexCleansTemporaryCopyAfterGitReadFailure(t *testing.T) {
	repo := initIndexRepo(t)
	writeIndexFile(t, filepath.Join(repo, "tracked.txt"), "base\n")
	runIndexGit(t, repo, "add", "tracked.txt")
	runIndexGit(t, repo, "commit", "-m", "base")
	index := filepath.Join(repo, ".git", "index")
	original, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	tempRoot := t.TempDir()
	t.Setenv("TMPDIR", tempRoot)
	if err := os.WriteFile(index, []byte("broken index"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureIndex(context.Background(), repo); err == nil {
		t.Fatal("expected malformed copied index to fail")
	}
	if err := os.WriteFile(index, original, 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(tempRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "verity-index-copy-") || strings.HasPrefix(entry.Name(), "index-") {
			t.Fatalf("temporary index copies were not cleaned: %v (%v)", entries, err)
		}
	}
}

func TestCaptureIndexPreservesPartialStaging(t *testing.T) {
	repo := initIndexRepo(t)
	file := filepath.Join(repo, "partial.txt")
	writeIndexFile(t, file, "base\nkeep\n")
	runIndexGit(t, repo, "add", "partial.txt")
	runIndexGit(t, repo, "commit", "-m", "base")
	writeIndexFile(t, file, "staged\nworking tree only\n")
	patch := "diff --git a/partial.txt b/partial.txt\n--- a/partial.txt\n+++ b/partial.txt\n@@ -1,2 +1,2 @@\n-base\n+staged\n keep\n"
	cmd := exec.Command("git", "-C", repo, "apply", "--cached", "-")
	cmd.Stdin = strings.NewReader(patch)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("apply partial staged patch: %v: %s", err, out)
	}
	candidate, err := CaptureIndex(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	dir, cleanup, err := (IndexSnapshotter{}).Snapshot(context.Background(), repo, candidate.Tree)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	data, err := os.ReadFile(filepath.Join(dir, "partial.txt"))
	if err != nil || string(data) != "staged\nkeep\n" {
		t.Fatalf("partial staging candidate = %q, %v", data, err)
	}
	working, err := os.ReadFile(file)
	if err != nil || string(working) != "staged\nworking tree only\n" {
		t.Fatalf("working file changed = %q, %v", working, err)
	}
}

func TestCaptureIndexRejectsUnmergedEntries(t *testing.T) {
	repo := initIndexRepo(t)
	file := filepath.Join(repo, "conflict.txt")
	writeIndexFile(t, file, "base\n")
	runIndexGit(t, repo, "add", "conflict.txt")
	runIndexGit(t, repo, "commit", "-m", "base")
	baseBranch := runIndexGit(t, repo, "branch", "--show-current")
	runIndexGit(t, repo, "checkout", "-b", "feature")
	writeIndexFile(t, file, "feature\n")
	runIndexGit(t, repo, "add", "conflict.txt")
	runIndexGit(t, repo, "commit", "-m", "feature")
	runIndexGit(t, repo, "checkout", baseBranch)
	writeIndexFile(t, file, "main\n")
	runIndexGit(t, repo, "add", "conflict.txt")
	runIndexGit(t, repo, "commit", "-m", "main")
	cmd := exec.Command("git", "-C", repo, "merge", "feature")
	if err := cmd.Run(); err == nil {
		t.Fatal("expected merge conflict")
	}
	if _, err := CaptureIndex(context.Background(), repo); !errors.Is(err, ErrUnmergedIndex) {
		t.Fatalf("unmerged index error = %v", err)
	}
}

func TestCandidateCannotSupplyItsVerificationPolicy(t *testing.T) {
	repo := initIndexRepo(t)
	writeIndexFile(t, filepath.Join(repo, "base.txt"), "base\n")
	runIndexGit(t, repo, "add", "base.txt")
	runIndexGit(t, repo, "commit", "-m", "base")
	policy := filepath.Join(repo, filepath.FromSlash(ManifestPath))
	writeIndexFile(t, policy, "version: 1\ncommands: {}\n")
	runIndexGit(t, repo, "add", "-f", filepath.ToSlash(filepath.Join(".verity", "verification.yaml")))
	candidate, err := CaptureIndex(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := RejectCandidatePolicyPath(context.Background(), repo, candidate.Tree, ManifestPath); !errors.Is(err, ErrCandidatePolicy) {
		t.Fatalf("candidate-controlled manifest should be rejected: %v", err)
	}
	if err := RejectCandidatePolicyPath(context.Background(), repo, candidate.Tree, "../outside.yaml"); !errors.Is(err, ErrUnsafeArchive) {
		t.Fatalf("unsafe policy path should be rejected: %v", err)
	}
}

func TestIndexSnapshotRejectsSubmodulesAndLFS(t *testing.T) {
	repo := initIndexRepo(t)
	writeIndexFile(t, filepath.Join(repo, "root.txt"), "root\n")
	runIndexGit(t, repo, "add", "root.txt")
	runIndexGit(t, repo, "commit", "-m", "root")
	nested := initIndexRepo(t)
	writeIndexFile(t, filepath.Join(nested, "nested.txt"), "nested\n")
	runIndexGit(t, nested, "add", ".")
	runIndexGit(t, nested, "commit", "-m", "nested")
	cmd := exec.Command("git", "-C", repo, "-c", "protocol.file.allow=always", "submodule", "add", nested, "module")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("add test submodule: %v: %s", err, out)
	}
	candidate, err := CaptureIndex(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := (IndexSnapshotter{}).Snapshot(context.Background(), repo, candidate.Tree); err == nil || !strings.Contains(err.Error(), "unsupported Git tree entry") {
		t.Fatalf("submodule snapshot error = %v", err)
	}
	remove := exec.Command("git", "-C", repo, "rm", "-f", "module")
	if out, err := remove.CombinedOutput(); err != nil {
		t.Fatalf("remove test submodule: %v: %s", err, out)
	}
	writeIndexFile(t, filepath.Join(repo, "large.bin"), "version https://git-lfs.github.com/spec/v1\noid sha256:abc\nsize 3\n")
	runIndexGit(t, repo, "add", "large.bin")
	candidate, err = CaptureIndex(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := (IndexSnapshotter{}).Snapshot(context.Background(), repo, candidate.Tree); err == nil || !strings.Contains(err.Error(), "Git LFS pointer unsupported") {
		t.Fatalf("LFS snapshot error = %v", err)
	}
}
