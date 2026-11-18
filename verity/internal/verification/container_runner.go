package verification

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ContainerRunner executes checks in a rootless Docker container. The only
// host path exposed to the container is the disposable candidate snapshot.
type ContainerRunner struct {
	RuntimeBinary string
	Snapshotter   Snapshotter
	OutputLimit   int
	RelayBinary   string
	hostOS        string
	hostUID       int
}

func (r ContainerRunner) Execute(ctx context.Context, repository string, plan Plan, approved bool) ([]CheckResult, error) {
	if !approved {
		return nil, ErrNotApproved
	}
	if plan.IsolationPolicy != IsolationRootlessContainer {
		return nil, ErrUnsupportedIsolation
	}
	if !validRevision(plan.CandidateSHA) {
		return nil, ErrInvalidCandidate
	}
	if !validContainerImage(plan.ContainerImage) {
		return blockedChecks(plan, "container image must be pinned by sha256 digest"), nil
	}
	hostOS := r.hostOS
	if hostOS == "" {
		hostOS = runtime.GOOS
	}
	if hostOS != "linux" {
		return blockedChecks(plan, "rootless container verification requires a Linux host"), nil
	}
	hostUID := r.hostUID
	if hostUID == 0 {
		hostUID = os.Getuid()
	}
	if hostUID == 0 {
		return blockedChecks(plan, "container verification refuses to run as host root"), nil
	}
	runtimeBinary := r.RuntimeBinary
	if runtimeBinary == "" {
		runtimeBinary = "docker"
	}
	info, err := r.runtimeInfo(ctx, runtimeBinary)
	if err != nil {
		return blockedChecks(plan, "container runtime unavailable: "+err.Error()), nil
	}
	if !strings.Contains(strings.ToLower(info), "rootless") && !strings.Contains(strings.ToLower(info), "userns") {
		return blockedChecks(plan, "container runtime is not rootless and does not report user namespaces"), nil
	}
	relayBinary := r.RelayBinary
	if len(plan.NetworkAllowlist) > 0 {
		if relayBinary == "" {
			relayBinary, err = os.Executable()
		}
		if err != nil {
			return blockedChecks(plan, "egress relay executable unavailable"), nil
		}
		info, err := os.Stat(relayBinary)
		if err != nil || !info.Mode().IsRegular() {
			return blockedChecks(plan, "egress relay executable unavailable"), nil
		}
	}
	snapshotter := r.Snapshotter
	if snapshotter == nil {
		snapshotter = GitSnapshotter{}
	}
	limit := r.OutputLimit
	if limit <= 0 {
		limit = defaultOutputLimit
	}
	results := make([]CheckResult, 0, len(plan.Checks))
	for _, check := range plan.Checks {
		resultAllowlist, egressErr := checkEgressAllowlist(plan, check)
		if egressErr != nil {
			resultAllowlist = nil
		}
		result := CheckResult{ID: check.ID, Name: check.Name, Required: check.Required, CriteriaIDs: append([]string(nil), check.CriteriaIDs...), NetworkAllowlist: append([]string(nil), resultAllowlist...), StartedAt: time.Now().UTC()}
		credentialErr := egressErr
		if credentialErr == nil {
			credentialErr = validateCredentialCheck(plan, check)
		}
		var resolvedCredentials []resolvedCredential
		timeout := check.Timeout
		if credentialErr == nil {
			resolvedCredentials, timeout, credentialErr = resolveCredentials(check, result.StartedAt)
		}
		if credentialErr != nil {
			result.Outcome, result.Diagnostic, result.FinishedAt = Blocked, credentialErr.Error(), time.Now().UTC()
			results = append(results, result)
			continue
		}
		result.CredentialBindings = append([]PlannedCredential(nil), check.Credentials...)
		path, cleanup, err := snapshotter.Snapshot(ctx, repository, plan.CandidateSHA)
		if err != nil {
			return nil, err
		}
		argv := check.Argv
		binary := ""
		if check.Adapter == "playwright" {
			binary = "playwright"
			argv = []string{"test", "--reporter=line", "--", check.Argv[0]}
		} else if len(argv) > 0 {
			binary, argv = argv[0], argv[1:]
		}
		if binary == "" || check.Timeout <= 0 {
			result.Outcome = Blocked
			result.Diagnostic = "invalid planned command or timeout"
			result.FinishedAt = time.Now().UTC()
			results = append(results, result)
			cleanup()
			continue
		}
		checkCtx, cancel := context.WithTimeout(ctx, timeout)
		broker, brokerErr := startEgressBroker(checkCtx, resultAllowlist)
		if brokerErr != nil {
			cancel()
			cleanup()
			return nil, brokerErr
		}
		name, nameErr := newContainerName()
		if nameErr != nil {
			broker.close()
			cancel()
			cleanup()
			return nil, nameErr
		}
		archiveFile, archiveErr := candidateArchive(path)
		if archiveErr != nil {
			broker.close()
			cancel()
			cleanup()
			return nil, archiveErr
		}
		args := containerArgs(plan.ContainerImage, binary, argv, name, check.BaseURL)
		if broker != nil {
			credentialNames := make([]string, 0, len(resolvedCredentials))
			for _, credential := range resolvedCredentials {
				credentialNames = append(credentialNames, credential.Name)
			}
			args = containerArgsWithEgress(plan.ContainerImage, binary, argv, name, check.BaseURL, relayBinary, broker.dir, credentialNames...)
		}
		cmd := exec.CommandContext(checkCtx, runtimeBinary, args...)
		cmd.Env = credentialsEnvironment(runtimeEnvironment(), resolvedCredentials)
		cmd.Stdin = archiveFile
		var output limitedBuffer
		output.limit = limit
		cmd.Stdout, cmd.Stderr = &output, &output
		err = cmd.Run()
		broker.close()
		_ = archiveFile.Close()
		_ = os.Remove(archiveFile.Name())
		cancel()
		removeContainer(runtimeBinary, name)
		result.Output = redactKnownSecrets(redactCredentialValues(output.String(), resolvedCredentials))
		result.FinishedAt = time.Now().UTC()
		if err == nil {
			result.Outcome = Passed
			zero := 0
			result.ExitCode = &zero
		} else if errors.Is(checkCtx.Err(), context.DeadlineExceeded) {
			result.Outcome = TimedOut
			result.Diagnostic = "container check timed out"
		} else if ctx.Err() != nil {
			result.Outcome = Blocked
			result.Diagnostic = "verification cancelled"
		} else {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				code := exit.ExitCode()
				result.ExitCode = &code
				result.Outcome = Failed
				result.Diagnostic = fmt.Sprintf("container check exited with status %d", code)
			} else {
				result.Outcome = Blocked
				result.Diagnostic = err.Error()
			}
		}
		results = append(results, result)
		cleanup()
		if ctx.Err() != nil {
			for _, pending := range plan.Checks[len(results):] {
				results = append(results, CheckResult{ID: pending.ID, Name: pending.Name, Required: pending.Required, Outcome: Blocked, Diagnostic: "verification cancelled", CriteriaIDs: append([]string(nil), pending.CriteriaIDs...)})
			}
			break
		}
	}
	return results, nil
}

func (r ContainerRunner) runtimeInfo(ctx context.Context, binary string) (string, error) {
	cmd := exec.CommandContext(ctx, binary, "info", "--format", "{{json .SecurityOptions}}")
	cmd.Env = runtimeEnvironment()
	data, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("inspect Docker security options: %w: %s", err, strings.TrimSpace(string(data)))
	}
	return string(data), nil
}

func containerArgs(image, binary string, argv []string, name, baseURL string) []string {
	uid := strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())
	args := []string{
		"run", "--rm", "--init", "--interactive", "--name", name, "--pull=never", "--network=none", "--read-only",
		"--cap-drop=ALL", "--security-opt=no-new-privileges:true",
		"--pids-limit=128", "--memory=1g", "--cpus=2", "--user", uid,
		"--tmpfs", "/workspace:rw,nosuid,nodev,size=512m,mode=1777",
		"--tmpfs", "/tmp:rw,nosuid,nodev,size=128m", "--workdir", "/workspace",
		"--env", "HOME=/tmp", "--env", "TMPDIR=/tmp", "--env", "CI=true",
		"--entrypoint", "/bin/sh", image,
		"-c", "tar -x -C /workspace && exec \"$@\"", "verity-check", binary,
	}
	if baseURL != "" {
		index := len(args) - 4
		withEnv := make([]string, 0, len(args)+2)
		withEnv = append(withEnv, args[:index]...)
		withEnv = append(withEnv, "--env", "PLAYWRIGHT_TEST_BASE_URL="+baseURL)
		args = append(withEnv, args[index:]...)
	}
	return append(args, argv...)
}

func containerArgsWithEgress(image, binary string, argv []string, name, baseURL, relayBinary, brokerDir string, credentialNames ...string) []string {
	args := containerArgs(image, binary, argv, name, baseURL)
	for i, arg := range args {
		if arg == "-c" && i+1 < len(args) {
			args[i+1] = "tar -x -C /workspace || exit $?; " + containerRelayBinary + " internal egress-relay --socket " + containerBrokerSocket + " --listen " + containerProxyAddress + " --ready-file /tmp/verity-egress-ready & i=0; while [ ! -f /tmp/verity-egress-ready ]; do i=$((i+1)); [ \"$i\" -lt 100 ] || exit 1; sleep 0.05; done; exec \"$@\""
			break
		}
	}
	proxy := "http://" + containerProxyAddress
	flags := []string{"--volume", brokerDir + ":/verity-egress:ro", "--volume", relayBinary + ":" + containerRelayBinary + ":ro", "--env", "HTTP_PROXY=" + proxy, "--env", "HTTPS_PROXY=" + proxy, "--env", "ALL_PROXY=" + proxy, "--env", "http_proxy=" + proxy, "--env", "https_proxy=" + proxy, "--env", "all_proxy=" + proxy, "--env", "NO_PROXY=", "--env", "no_proxy="}
	for _, name := range credentialNames {
		flags = append(flags, "--env", name)
	}
	return append(args[:1], append(flags, args[1:]...)...)
}

func candidateArchive(root string) (*os.File, error) {
	return candidateArchiveWithLimits(root, defaultArchiveLimit, defaultSnapshotEntryLimit)
}

func candidateArchiveWithLimits(root string, maxBytes int64, maxEntries int) (*os.File, error) {
	if maxBytes <= 0 || maxEntries <= 0 {
		return nil, errors.New("candidate archive limits must be positive")
	}
	f, err := os.CreateTemp("", "verity-candidate-*.tar")
	if err != nil {
		return nil, err
	}
	writer := &boundedArchiveWriter{writer: f, remaining: maxBytes}
	tw := tar.NewWriter(writer)
	entries := 0
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		entries++
		if entries > maxEntries {
			return ErrSnapshotQuotaExceeded
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ErrUnsafeArchive
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if entry.IsDir() {
			header.Name += "/"
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			if info.Mode()&os.ModeSymlink != 0 || entry.IsDir() {
				return nil
			}
			return ErrUnsafeArchive
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(tw, file, info.Size())
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err == nil {
		err = tw.Close()
	}
	if err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, err
	}
	return f, nil
}

type boundedArchiveWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *boundedArchiveWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.remaining {
		return 0, ErrArchiveTooLarge
	}
	n, err := w.writer.Write(data)
	w.remaining -= int64(n)
	return n, err
}

func newContainerName() (string, error) {
	f, err := os.CreateTemp("", "verity-check-")
	if err != nil {
		return "", err
	}
	name := filepath.Base(f.Name())
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	if err := os.Remove(f.Name()); err != nil {
		return "", err
	}
	return name, nil
}

func removeContainer(binary, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "rm", "--force", name)
	cmd.Env = runtimeEnvironment()
	_ = cmd.Run()
}

func runtimeEnvironment() []string {
	allow := map[string]bool{"PATH": true, "HOME": true, "DOCKER_HOST": true, "DOCKER_CONTEXT": true, "XDG_RUNTIME_DIR": true}
	var env []string
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if allow[key] {
			env = append(env, item)
		}
	}
	if os.Getenv("PATH") == "" {
		env = append(env, "PATH=/usr/bin:/bin")
	}
	return env
}

func blockedChecks(plan Plan, message string) []CheckResult {
	results := make([]CheckResult, 0, len(plan.Checks))
	now := time.Now().UTC()
	for _, check := range plan.Checks {
		results = append(results, CheckResult{ID: check.ID, Name: check.Name, Required: check.Required, Outcome: Blocked, Diagnostic: message, CriteriaIDs: append([]string(nil), check.CriteriaIDs...), StartedAt: now, FinishedAt: now})
	}
	return results
}
