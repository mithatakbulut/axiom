package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"verity/internal/store/sqlite"
)

func dashboardCommand(opts options) error {
	if !opts.json {
		return fmt.Errorf("dashboard summary requires --json")
	}
	path, err := filepath.Abs(opts.configPath())
	if err != nil {
		return err
	}
	c, err := loadConfig(path)
	if err != nil {
		return err
	}
	root := filepath.Dir(filepath.Dir(path))
	summary, err := sqlite.Dashboard(context.Background(), filepath.Join(root, c.Database), path, filepath.Base(root))
	if err != nil {
		return fmt.Errorf("dashboard summary: %w", err)
	}
	return json.NewEncoder(os.Stdout).Encode(summary)
}

// Recognize the command even when option parsing fails, so stderr stays JSON.
func isDashboardSummary(args []string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "dashboard" && args[i+1] == "summary" {
			return true
		}
	}
	return false
}
