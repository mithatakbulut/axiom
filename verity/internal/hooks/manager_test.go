package hooks

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func hookGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func hookRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "global.gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	hookGit(t, root, "init")
	hookGit(t, root, "config", "user.email", "test@example.com")
	hookGit(t, root, "config", "user.name", "Test")
	return root
}

func fakeVerity(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	binary := filepath.Join(dir, "verity")
	body := "#!/bin/sh\nprintf 'verity:%s:%s\\n' \"$1\" \"$2\" >> " + shellQuote(log) + "\n"
	if err := os.WriteFile(binary, []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
	return binary, log
}

func TestInstallStatusAndUninstallDefaultHookPath(t *testing.T) {
	repo := hookRepo(t)
	binary, _ := fakeVerity(t)
	ctx := context.Background()
	status, err := Install(ctx, repo, binary, false)
	if err != nil || !status.Installed || !status.Active {
		t.Fatalf("install: %#v %v", status, err)
	}
	if _, err := Install(ctx, repo, binary, false); err != nil {
		t.Fatalf("install should be idempotent: %v", err)
	}
	status, err = Inspect(ctx, repo)
	if err != nil || !status.Installed || !status.Active {
		t.Fatalf("status: %#v %v", status, err)
	}
	data, err := os.ReadFile(status.HookPath)
	if err != nil || !strings.Contains(string(data), managedMarker) {
		t.Fatalf("managed hook missing marker: %q %v", data, err)
	}
	if err := exec.Command("git", "-C", repo, "config", "--get", "core.hooksPath").Run(); err == nil {
		t.Fatal("installer unexpectedly changed core.hooksPath")
	}
	status, err = Uninstall(ctx, repo)
	if err != nil || status.Installed {
		t.Fatalf("uninstall: %#v %v", status, err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "hooks", "pre-commit")); !os.IsNotExist(err) {
		t.Fatalf("managed hook remains: %v", err)
	}
}

func TestExistingHookRequiresExplicitChainAndRestoresExactly(t *testing.T) {
	repo := hookRepo(t)
	hookDir := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hookDir, 0755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "calls.log")
	original := []byte("#!/bin/sh\nprintf 'original:%s\\n' \"$1\" >> " + shellQuote(log) + "\n")
	hookPath := filepath.Join(hookDir, "pre-commit")
	if err := os.WriteFile(hookPath, original, 0750); err != nil {
		t.Fatal(err)
	}
	binary, verityLog := fakeVerity(t)
	if _, err := Install(context.Background(), repo, binary, false); !errors.Is(err, ErrExistingHook) {
		t.Fatalf("existing hook install error = %v", err)
	}
	status, err := Install(context.Background(), repo, binary, true)
	if err != nil || !status.Active {
		t.Fatalf("chain install: %#v %v", status, err)
	}
	cmd := exec.Command("/bin/sh", hookPath, "commit-arg")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run chain: %v: %s", err, out)
	}
	calls, err := os.ReadFile(log)
	if err != nil || string(calls) != "original:commit-arg\n" {
		t.Fatalf("previous hook call = %q %v", calls, err)
	}
	calls, err = os.ReadFile(verityLog)
	if err != nil || string(calls) != "verity:hooks:run\n" {
		t.Fatalf("Verity hook call = %q %v", calls, err)
	}
	if _, err := Uninstall(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(hookPath)
	if err != nil || string(restored) != string(original) {
		t.Fatalf("previous hook bytes not restored: %q %v", restored, err)
	}
	info, err := os.Stat(hookPath)
	if err != nil || info.Mode().Perm() != 0750 {
		t.Fatalf("previous hook mode not restored: %v %v", info, err)
	}
}

func TestSymlinkHookIsNeverReplacedEvenWithChain(t *testing.T) {
	repo := hookRepo(t)
	hookDir := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hookDir, 0755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "shared-hook")
	original := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(target, original, 0755); err != nil {
		t.Fatal(err)
	}
	hookPath := filepath.Join(hookDir, "pre-commit")
	if err := os.Symlink(target, hookPath); err != nil {
		t.Fatal(err)
	}
	binary, _ := fakeVerity(t)
	if _, err := Install(context.Background(), repo, binary, true); !errors.Is(err, ErrHookSymlink) {
		t.Fatalf("symlink hook install error = %v, want ErrHookSymlink", err)
	}
	link, err := os.Readlink(hookPath)
	if err != nil || link != target {
		t.Fatalf("existing hook symlink changed: target=%q err=%v", link, err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != string(original) {
		t.Fatalf("symlink target changed: %q err=%v", got, err)
	}
}

func TestCustomHooksPathAndModifiedHookRefusal(t *testing.T) {
	repo := hookRepo(t)
	hookGit(t, repo, "config", "core.hooksPath", ".custom-hooks")
	binary, _ := fakeVerity(t)
	status, err := Install(context.Background(), repo, binary, false)
	if err != nil {
		t.Fatal(err)
	}
	canonicalRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canonicalRepo, ".custom-hooks", "pre-commit")
	if status.HookPath != want {
		t.Fatalf("hook path %q, want %q", status.HookPath, want)
	}
	if err := os.WriteFile(want, []byte("changed by user\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Uninstall(context.Background(), repo); !errors.Is(err, ErrHookDrift) {
		t.Fatalf("modified hook should be preserved: %v", err)
	}
	data, err := os.ReadFile(want)
	if err != nil || string(data) != "changed by user\n" {
		t.Fatalf("modified hook was overwritten: %q %v", data, err)
	}
}

func TestPostCommitHookInstallsAndInvokesBindingCommand(t *testing.T) {
	repo := hookRepo(t)
	binary, log := fakeVerity(t)
	status, err := InstallPostCommit(context.Background(), repo, binary, false)
	if err != nil || !status.Active || !strings.HasSuffix(status.HookPath, "post-commit") {
		t.Fatalf("install post-commit: %#v %v", status, err)
	}
	cmd := exec.Command("/bin/sh", status.HookPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run post-commit: %v: %s", err, out)
	}
	calls, err := os.ReadFile(log)
	if err != nil || string(calls) != "verity:hooks:post-commit\n" {
		t.Fatalf("post-commit invocation: %q %v", calls, err)
	}
	if _, err := UninstallPostCommit(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
}
