package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	githubbridge "verity/internal/integrations/github"
	"verity/internal/proof"
	"verity/internal/store/sqlite"
	"verity/internal/verification"
)

type webhookResponse struct {
	header http.Header
	status int
}

var newGitHubRetriggerClient = func(baseURL, token string) githubbridge.Client {
	return githubbridge.Client{BaseURL: baseURL, Token: token}
}

func (w *webhookResponse) Header() http.Header { return w.header }
func (w *webhookResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *webhookResponse) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return len(b), nil
}

// githubReceive processes one forwarded GitHub request; the caller must provide
// the raw body on stdin and preserve the original delivery and signature headers.
func githubReceive(args []string, opts options) error {
	secret := os.Getenv("VERITY_GITHUB_WEBHOOK_SECRET")
	if secret == "" {
		return fmt.Errorf("VERITY_GITHUB_WEBHOOK_SECRET is required")
	}
	installationID, err := githubInstallationID()
	if err != nil {
		return err
	}
	path := opts.configPath()
	c, err := loadConfig(path)
	if err != nil {
		return err
	}
	dbPath := c.Database
	if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(filepath.Dir(filepath.Dir(path)), dbPath)
	}
	db, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	req, err := http.NewRequest(http.MethodPost, "http://localhost/", os.Stdin)
	if err != nil {
		return err
	}
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-GitHub-Delivery", args[3])
	req.Header.Set("X-Hub-Signature-256", args[4])
	out := &webhookResponse{header: make(http.Header)}
	(githubbridge.Webhook{Secret: []byte(secret), Handler: githubbridge.PullWorkflowHandler{Repository: args[2], InstallationID: installationID, Store: db}}).ServeHTTP(out, req)
	if out.status != http.StatusAccepted {
		return fmt.Errorf("GitHub delivery rejected (HTTP %d)", out.status)
	}
	fmt.Println("GitHub delivery accepted; inspect workflow list for the revision-bound run.")
	return nil
}

func githubWorkflows(args []string, opts options) error {
	path := opts.configPath()
	c, err := loadConfig(path)
	if err != nil {
		return err
	}
	dbPath := c.Database
	if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(filepath.Dir(filepath.Dir(path)), dbPath)
	}
	db, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	links, err := db.PullWorkflows(context.Background(), args[2])
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(links)
}

func githubRetrigger(args []string, opts options) error {
	if !normalizeGitHubRepo(args[2]) {
		return errors.New("repository must be owner/repo")
	}
	number, err := strconv.Atoi(args[3])
	if err != nil || number <= 0 {
		return errors.New("pull number must be a positive integer")
	}
	token := os.Getenv("VERITY_GITHUB_TOKEN")
	if token == "" {
		return errors.New("VERITY_GITHUB_TOKEN is required for user-authorized retrigger")
	}
	parts := strings.Split(args[2], "/")
	client := newGitHubRetriggerClient(os.Getenv("VERITY_GITHUB_API_URL"), token)
	actor, head, err := client.AuthorizePullRequestRetrigger(context.Background(), parts[0], parts[1], number)
	if err != nil {
		return err
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return err
	}
	delivery := "manual-" + hex.EncodeToString(random[:])
	store, closeStore, _, _, err := githubLocalServices(opts)
	if err != nil {
		return err
	}
	defer closeStore()
	runID, err := (githubbridge.PullWorkflowHandler{Repository: args[2], Store: store}).ManualRetrigger(context.Background(), delivery, actor, number, head)
	if err != nil {
		return err
	}
	fmt.Printf("queued authorized manual verification %s for %s pull %d at %s (actor %s)\n", runID, args[2], number, head, actor)
	return nil
}

func githubServe(args []string, opts options) error {
	secret := os.Getenv("VERITY_GITHUB_WEBHOOK_SECRET")
	if secret == "" {
		return errors.New("VERITY_GITHUB_WEBHOOK_SECRET is required")
	}
	installationID, err := githubInstallationID()
	if err != nil {
		return err
	}
	store, closeStore, _, _, err := githubLocalServices(opts)
	if err != nil {
		return err
	}
	defer closeStore()
	addr := args[3]
	if addr == "" {
		return errors.New("webhook listen address is required")
	}
	handler := githubbridge.Webhook{Secret: []byte(secret), Handler: githubbridge.PullWorkflowHandler{Repository: args[2], InstallationID: installationID, Store: store}}
	server := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	fmt.Fprintf(os.Stderr, "Verity GitHub webhook receiver listening on %s for %s\n", addr, args[2])
	return server.ListenAndServe()
}

func githubInstallationID() (int64, error) {
	value := strings.TrimSpace(os.Getenv("VERITY_GITHUB_INSTALLATION_ID"))
	if value == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("VERITY_GITHUB_INSTALLATION_ID must be a positive integer")
	}
	return id, nil
}

func githubWorkerCommand(args []string, opts options) error {
	if args[3] != "once" && args[3] != "loop" {
		return errors.New("worker mode must be once or loop")
	}
	token := os.Getenv("VERITY_GITHUB_TOKEN")
	appID, keyPath := os.Getenv("VERITY_GITHUB_APP_ID"), os.Getenv("VERITY_GITHUB_APP_PRIVATE_KEY_PATH")
	var tokenSource interface {
		AccessToken(context.Context) (string, error)
	}
	if (appID == "") != (keyPath == "") {
		return errors.New("VERITY_GITHUB_APP_ID and VERITY_GITHUB_APP_PRIVATE_KEY_PATH must be set together")
	}
	if appID != "" {
		if token != "" {
			return errors.New("configure either VERITY_GITHUB_TOKEN or GitHub App credentials, not both")
		}
		privateKey, readErr := os.ReadFile(keyPath)
		if readErr != nil {
			return fmt.Errorf("read GitHub App private key: %w", readErr)
		}
		parts := strings.Split(args[2], "/")
		tokenSource = &githubbridge.AppTokenSource{BaseURL: os.Getenv("VERITY_GITHUB_API_URL"), AppID: appID, Owner: parts[0], Repository: parts[1], PrivateKeyPEM: privateKey}
	} else if token == "" {
		return errors.New("VERITY_GITHUB_TOKEN or GitHub App credentials are required")
	}
	store, closeStore, root, dataDir, err := githubLocalServices(opts)
	if err != nil {
		return err
	}
	defer closeStore()
	manifest, err := verification.LoadManifest(filepath.Join(root, verification.ManifestPath))
	if err != nil {
		return err
	}
	baseURL := os.Getenv("VERITY_GITHUB_API_URL")
	worker := githubbridge.PullWorker{Store: store, Repository: args[2], GitBaseURL: os.Getenv("VERITY_GITHUB_GIT_URL"), GitHub: githubbridge.Client{BaseURL: baseURL, Token: token, TokenSource: tokenSource}, Manifest: manifest, Artifacts: proof.LocalArtifactStore{Root: filepath.Join(dataDir, "artifacts")}}
	if args[3] == "once" {
		return worker.RunOnce(context.Background())
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	for ctx.Err() == nil {
		err = worker.RunOnce(ctx)
		if err == nil {
			continue
		}
		if errors.Is(err, sqlite.ErrNoPullJob) {
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
			continue
		}
		fmt.Fprintln(os.Stderr, "verity github worker:", err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
	return nil
}

func githubLocalServices(opts options) (*sqlite.Store, func(), string, string, error) {
	path, err := filepath.Abs(opts.configPath())
	if err != nil {
		return nil, nil, "", "", err
	}
	c, err := loadConfig(path)
	if err != nil {
		return nil, nil, "", "", err
	}
	root := filepath.Dir(filepath.Dir(path))
	dbPath := c.Database
	if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(root, dbPath)
	}
	db, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		return nil, nil, "", "", err
	}
	dataDir := c.DataDir
	if !filepath.IsAbs(dataDir) {
		dataDir = filepath.Join(root, dataDir)
	}
	return db, func() { _ = db.Close() }, root, dataDir, nil
}

func normalizeGitHubRepo(repo string) bool {
	parts := strings.Split(repo, "/")
	return len(parts) == 2 && strings.TrimSpace(parts[0]) == parts[0] && strings.TrimSpace(parts[1]) == parts[1] && parts[0] != "" && parts[1] != ""
}
