package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"vigil/internal/advisor"
	"vigil/internal/config"
	"vigil/internal/control"
	"vigil/internal/install"
	"vigil/internal/mcp"
	"vigil/internal/recorder"
)

func main() { os.Exit(run(os.Args[1:])) }
func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: vigil <install|uninstall|rollback|status|doctor|config|history|summary|progress|explain|budget|resources|advisor|task|eval|backup|init|pause|resume|mode|hook|mcp> [options]")
		return 2
	}
	cmd := args[0]
	var sub string
	if (cmd == "mode" || cmd == "config" || cmd == "advisor" || cmd == "task") && len(args) > 1 && len(args[1]) > 0 && args[1][0] != '-' {
		sub = args[1]
		args = append(args[:1], args[2:]...)
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	path := fs.String("config", "", "configuration file")
	limit := fs.Int("last", 20, "number of history events")
	repo := fs.String("repo", "", "optional repository directory for progress")
	session := fs.String("session", "", "session identifier")
	turn := fs.String("turn", "", "Codex turn identifier")
	task := fs.String("task", "", "task contract identifier")
	requirement := fs.String("requirement", "", "requirement identifier")
	approved := fs.Bool("approved", false, "explicitly authorize this terminal advisor request")
	output := fs.String("output", "", "path for a new SQLite backup")
	if err := fs.Parse(args[1:]); err != nil {
		if cmd == "hook" {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		if cmd == "hook" {
			return 0
		}
		return 2
	}
	fail := func(message string) int {
		_ = json.NewEncoder(os.Stderr).Encode(map[string]string{"level": "error", "message": message})
		if cmd == "hook" {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"systemMessage": "Vigil monitoring unavailable; Codex will continue."})
			return 0
		}
		return 1
	}
	switch cmd {
	case "install", "uninstall", "rollback":
		return runInstallation(cmd, *path, fail)
	case "hook", "status", "doctor", "summary", "progress", "mcp", "history", "explain", "pause", "resume", "mode", "config", "init", "budget", "resources", "advisor", "task", "eval", "backup":
	default:
		return fail("unknown command")
	}
	configPath := *path
	if configPath != "" {
		var err error
		if configPath, err = filepath.Abs(configPath); err != nil {
			return fail("could not resolve the Vigil configuration path")
		}
	}
	c, err := config.Load(configPath)
	if err != nil {
		return fail("configuration unavailable or invalid")
	}
	if cmd == "config" {
		if sub == "show" {
			_ = json.NewEncoder(os.Stdout).Encode(c)
			return 0
		}
		if sub == "validate" {
			fmt.Println("configuration valid")
			return 0
		}
		return fail("config requires show or validate")
	}
	if cmd == "hook" && os.Getenv("VIGIL_ADVISOR_CHILD") == "1" {
		return 0
	}
	if cmd == "advisor" && sub == "check" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err = (advisor.Runner{}).Check(ctx, c.Models); err != nil {
			return fail(err.Error())
		}
		if err = json.NewEncoder(os.Stdout).Encode(c.Models); err != nil {
			return fail("could not write response")
		}
		return 0
	}
	var e recorder.Event
	if cmd == "hook" {
		e, err = recorder.Parse(os.Stdin, time.Now())
		if err != nil {
			return fail("hook payload rejected")
		}
	}
	s, err := recorder.Open(c.Storage.Path)
	if err != nil {
		return fail("event storage unavailable")
	}
	defer func() { _ = s.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	svc := control.Service{Config: c, ConfigPath: configPath, Store: s}
	write := func(v any, err error) int {
		if err != nil {
			return fail(err.Error())
		}
		if err = json.NewEncoder(os.Stdout).Encode(v); err != nil {
			return fail("could not write response")
		}
		return 0
	}
	switch cmd {
	case "init":
		return write(svc.Status(ctx))
	case "budget":
		return write(svc.Budget(ctx, *session, *turn, time.Now()))
	case "resources":
		return write(svc.Resources(ctx, *session, *repo, time.Now()))
	case "eval":
		report, err := svc.Eval(ctx)
		if code := write(report, err); code != 0 {
			return code
		}
		if report.Passed != report.Total {
			return 1
		}
		return 0
	case "backup":
		if err = s.Backup(ctx, *output); err != nil {
			return fail(err.Error())
		}
		return write(map[string]string{"backup": *output}, nil)
	case "advisor":
		switch sub {
		case "request":
			return write(svc.AdvisorPreview(ctx, *session, *turn))
		case "run":
			if !*approved {
				return fail("advisor run requires explicit --approved")
			}
			return write(svc.RequestAdvisor(context.Background(), *session, *turn, *repo, false, true))
		default:
			return fail("advisor requires check, request or run")
		}
	case "task":
		now := time.Now()
		switch sub {
		case "create":
			var contract recorder.Contract
			if err = readJSON(os.Stdin, &contract); err != nil {
				return fail("invalid task contract JSON")
			}
			return write(svc.CreateTask(ctx, *session, contract, now))
		case "evidence":
			var ev recorder.Evidence
			if err = readJSON(os.Stdin, &ev); err != nil {
				return fail("invalid evidence JSON")
			}
			return write(svc.Evidence(ctx, *session, *task, *requirement, ev, *approved, now))
		case "verify":
			return write(svc.Verification(ctx, *session, *task))
		default:
			return fail("task requires create, evidence or verify")
		}
	case "mcp":
		if err = mcp.Serve(context.Background(), os.Stdin, os.Stdout, svc); err != nil {
			return fail("MCP server failed")
		}
		return 0
	case "pause", "resume":
		if err = svc.SetPaused(ctx, cmd == "pause"); err != nil {
			return fail("could not update pause state")
		}
		if cmd == "pause" {
			fmt.Println("paused")
		} else {
			fmt.Println("resumed")
		}
		return 0
	case "mode":
		if err = svc.SetMode(ctx, sub); err != nil {
			return fail("mode must be observe, assist or autonomous")
		}
		fmt.Println("mode:", sub)
		return 0
	case "history":
		events, err := svc.History(ctx, *limit)
		if err != nil {
			return fail("history unavailable or invalid limit")
		}
		_ = json.NewEncoder(os.Stdout).Encode(events)
		return 0
	case "explain":
		return write(svc.Explain(ctx, *session))
	}
	if cmd == "hook" {
		if err = s.Record(ctx, e, c.Storage.RetentionDays); err != nil {
			return fail("event persistence failed")
		}
		if e.Type == "Stop" {
			hookCtx, done := context.WithTimeout(context.Background(), advisor.Timeout+6*time.Second)
			defer done()
			result, err := svc.OnStop(hookCtx, e)
			if err != nil {
				return fail("progress evaluation unavailable")
			}
			if result != (control.StopResult{}) {
				return write(result, nil)
			}
		}
		return 0
	}
	if cmd == "progress" {
		if *session == "" {
			return fail("progress requires --session")
		}
		progress, err := s.ProgressFor(ctx, *session, *turn, *repo, c.Progress)
		if err != nil {
			return fail("progress unavailable")
		}
		if err = json.NewEncoder(os.Stdout).Encode(progress); err != nil {
			return fail("could not write progress")
		}
		return 0
	}
	if cmd == "summary" {
		if *session == "" {
			return fail("summary requires --session")
		}
		summary, err := s.Summary(ctx, *session)
		if err != nil {
			return fail("session summary unavailable")
		}
		if err = json.NewEncoder(os.Stdout).Encode(summary); err != nil {
			return fail("could not write summary")
		}
		return 0
	}
	st, err := svc.Status(ctx)
	if err != nil {
		return fail("status unavailable")
	}
	fmt.Printf("mode: %s\npaused: %t\ninterventions available: %t\ndatabase: %s\nevents: %d\nintegrity: ok\n", st.Mode, st.Paused, st.InterventionAvailable, c.Storage.Path, st.Events)
	if cmd == "doctor" {
		fmt.Println("hook coverage: best effort; installation and trust are not checked")
	}
	return 0
}

func runInstallation(action, path string, fail func(string) int) int {
	home, err := install.Home()
	if err != nil {
		return fail("could not locate the Codex configuration directory")
	}
	var binary string
	if action == "install" {
		if _, err := config.Load(path); err != nil {
			return fail("configuration unavailable or invalid")
		}
		binary, err = os.Executable()
		if err == nil {
			binary, err = filepath.EvalSymlinks(binary)
		}
		if err != nil {
			return fail("could not locate the Vigil executable")
		}
		if path != "" {
			path, err = filepath.Abs(path)
			if err != nil {
				return fail("could not resolve the Vigil configuration path")
			}
		}
	}
	message, err := install.Run(action, home, binary, path)
	if err != nil {
		return fail(err.Error())
	}
	fmt.Println(message)
	return 0
}

func readJSON(r io.Reader, dst any) error {
	b, err := io.ReadAll(io.LimitReader(r, recorder.MaxPayload+1))
	if err != nil || len(b) > recorder.MaxPayload {
		return fmt.Errorf("JSON input exceeds limit or cannot be read")
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err = d.Decode(dst); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("expected one JSON object")
	}
	return nil
}
