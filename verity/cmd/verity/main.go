package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	vigilbridge "verity/internal/integrations/vigil"
	"verity/internal/process/control"
	"verity/internal/store/sqlite"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		if isDashboardSummary(os.Args[1:]) {
			_ = json.NewEncoder(os.Stderr).Encode(map[string]string{"error": "dashboard_summary_failed", "message": err.Error()})
		} else {
			fmt.Fprintln(os.Stderr, "verity:", err)
		}
		os.Exit(1)
	}
}
func run(args []string) error {
	args, opts, err := parseOptions(args)
	if err != nil {
		return err
	}
	if opts.help || len(args) == 0 {
		return showHelp(args)
	}
	if err := validateArgs(args); err != nil {
		return err
	}
	switch args[0] {
	case "version":
		fmt.Println("verity", version)
		return nil
	case "init":
		root := "."
		if len(args) > 1 {
			root = args[1]
		}
		root, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		dir := filepath.Join(root, ".verity")
		if err = os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		cfg := filepath.Join(dir, "config.yaml")
		if _, err = os.Stat(cfg); err == nil {
			return fmt.Errorf("config already exists: %s", cfg)
		}
		if err = os.WriteFile(cfg, []byte("schema_version: 1\ndatabase: .verity/verity.db\nlog_level: info\ndata_dir: .verity\n"), 0600); err != nil {
			return err
		}
		db, err := sqlite.Open(context.Background(), filepath.Join(root, ".verity", "verity.db"))
		if err != nil {
			return err
		}
		defer db.Close()
		fmt.Printf("initialized Verity in %s\n", dir)
		return nil
	case "config":
		if len(args) < 2 || args[1] != "validate" {
			return errors.New("usage: verity config validate [config-path]")
		}
		path := opts.configPath()
		if opts.config == "" && len(args) > 2 {
			path = args[2]
		}
		c, err := loadConfig(path)
		if err != nil {
			return err
		}
		fmt.Printf("configuration valid (schema %d)\n", c.SchemaVersion)
		return nil
	case "doctor":
		path := opts.configPath()
		if opts.config == "" && len(args) > 1 {
			path = args[1]
		}
		c, err := loadConfig(path)
		if err != nil {
			return err
		}
		dbPath := c.Database
		if !filepath.IsAbs(dbPath) {
			dbPath = filepath.Join(filepath.Dir(filepath.Dir(path)), dbPath)
		}
		if err = sqlite.CheckReadOnly(context.Background(), dbPath); err != nil {
			return err
		}
		fmt.Println("configuration: ok\ndatabase: ok")
		return nil
	case "dashboard":
		return dashboardCommand(opts)
	case "workflow", "task":
		return inspect(args, opts)
	case "proof":
		return proofCommand(args, opts)
	case "verify":
		return verifyCommand(args, opts)
	case "work":
		return workCommand(args, opts)
	case "chat":
		return chatCommand(args, opts)
	case "vigil":
		return vigilCommand(args, opts)
	case "github":
		if args[1] == "workflows" {
			return githubWorkflows(args, opts)
		}
		return githubReceive(args, opts)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func vigilCommand(args []string, opts options) error {
	if len(args) >= 2 && args[1] == "attach" {
		return vigilAttach(args, opts)
	}
	if len(args) < 4 || args[1] != "inspect" {
		return errors.New("usage: verity vigil inspect <vigil-binary> <session-id> [turn-id] [vigil-args...]")
	}
	turn := ""
	start := 4
	if len(args) > 4 && !strings.HasPrefix(args[4], "-") {
		turn = args[4]
		start = 5
	}
	snapshot, err := (vigilbridge.MCPClient{Command: args[2], Args: append([]string{"mcp"}, args[start:]...)}).Inspect(context.Background(), args[3], turn)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(snapshot)
}

func vigilAttach(args []string, opts options) error {
	if len(args) < 4 {
		return errors.New("usage: verity vigil attach <link.json> <vigil-binary> [vigil-args...]")
	}
	f, err := os.Open(args[2])
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if err != nil {
		return err
	}
	if len(b) > 64*1024 {
		return errors.New("Vigil link file exceeds 64 KiB")
	}
	var link vigilbridge.Link
	if err = json.Unmarshal(b, &link); err != nil {
		return errors.New("invalid Vigil bridge JSON")
	}
	if link.SessionID == "" {
		return errors.New("Vigil link JSON must include an explicit session_id")
	}
	verityConfig := opts.configPath()
	vigilArgs := []string{"mcp"}
	for i := 4; i < len(args); i++ {
		if args[i] == "--verity-config" {
			if i+1 >= len(args) {
				return errors.New("--verity-config requires a path")
			}
			verityConfig = args[i+1]
			i++
			continue
		}
		vigilArgs = append(vigilArgs, args[i])
	}
	snapshot, err := (vigilbridge.MCPClient{Command: args[3], Args: vigilArgs}).Inspect(context.Background(), link.SessionID, "")
	if err != nil {
		return err
	}
	link, err = snapshot.Attach(link)
	if err != nil {
		return err
	}
	if link.CandidateSHA == "" {
		return errors.New("Vigil link must be candidate-bound before it can be attached to proof")
	}
	c, err := loadConfig(verityConfig)
	if err != nil {
		return err
	}
	dbPath := c.Database
	if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(filepath.Dir(filepath.Dir(verityConfig)), dbPath)
	}
	db, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	run, err := db.GetRun(context.Background(), link.WorkflowID)
	if err != nil {
		return fmt.Errorf("load Verity workflow run: %w", err)
	}
	foundTask := false
	for _, task := range run.Tasks {
		if task.ID == link.TaskID {
			foundTask = true
			break
		}
	}
	if !foundTask {
		return fmt.Errorf("task %q is not part of Verity workflow run %q", link.TaskID, link.WorkflowID)
	}
	if err = db.SaveVigilLink(context.Background(), link); err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(link)
}

func inspect(args []string, opts options) error {
	if len(args) < 2 {
		return errors.New("usage: verity workflow create|list|show ... | task list <run-id>")
	}
	path := opts.configPath()
	if opts.config == "" {
		switch {
		case args[1] == "create" && len(args) > 5:
			path = args[5]
		case args[1] == "list" && args[0] == "workflow" && len(args) > 2:
			path = args[2]
		case len(args) > 3:
			path = args[3]
		}
	}
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
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if args[0] == "workflow" {
		switch args[1] {
		case "create":
			if len(args) < 5 {
				return errors.New("usage: verity workflow create <feature|bugfix|documentation> <definition-id> <run-id> [config-path]")
			}
			d := control.Template(args[2], args[3])
			if err = db.SaveDefinition(context.Background(), d); err != nil {
				return err
			}
			r, err := control.NewRun(args[4], d, time.Now())
			if err != nil {
				return err
			}
			if err = db.CreateRun(context.Background(), r); err != nil {
				return err
			}
			if opts.json {
				return enc.Encode(r)
			}
			fmt.Printf("Created workflow %s (state: %s).\nRun 'verity workflow show %s' for details.\n", r.ID, r.State, r.ID)
			return nil
		case "list":
			runs, err := db.ListRuns(context.Background())
			if err != nil {
				return err
			}
			if opts.json {
				return enc.Encode(runs)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "RUN ID\tSTATE\tCREATED (UTC)")
			for _, run := range runs {
				fmt.Fprintf(w, "%s\t%s\t%s\n", run.ID, run.State, run.CreatedAt.UTC().Format(time.RFC3339))
			}
			return w.Flush()
		case "show":
			if len(args) < 3 {
				return errors.New("usage: verity workflow show <run-id> [config-path]")
			}
			r, err := db.GetRun(context.Background(), args[2])
			if err != nil {
				return err
			}
			if opts.json {
				return enc.Encode(r)
			}
			fmt.Printf("Run: %s\nDefinition: %s (version %d)\nState: %s\nCreated: %s\nTasks: %d\n", r.ID, r.DefinitionID, r.DefinitionVersion, r.State, r.CreatedAt.UTC().Format(time.RFC3339), len(r.Tasks))
			fmt.Printf("Run 'verity task list %s' to see tasks.\n", r.ID)
			return nil
		default:
			return fmt.Errorf("unknown workflow command %q", args[1])
		}
	}
	if args[1] != "list" || len(args) < 3 {
		return errors.New("usage: verity task list <run-id> [config-path]")
	}
	r, err := db.GetRun(context.Background(), args[2])
	if err != nil {
		return err
	}
	if opts.json {
		return enc.Encode(r.Tasks)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "TASK ID\tSTATE\tTITLE")
	for _, task := range r.Tasks {
		fmt.Fprintf(w, "%s\t%s\t%s\n", task.ID, task.State, task.Title)
	}
	return w.Flush()
}
