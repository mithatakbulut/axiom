package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	githubbridge "verity/internal/integrations/github"
	"verity/internal/store/sqlite"
)

type webhookResponse struct {
	header http.Header
	status int
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
	(githubbridge.Webhook{Secret: []byte(secret), Handler: githubbridge.PullWorkflowHandler{Repository: args[2], Store: db}}).ServeHTTP(out, req)
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
