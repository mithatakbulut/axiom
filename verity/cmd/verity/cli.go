package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"verity/internal/config"
)

type options struct {
	config     string
	json, help bool
}

func (o options) configPath() string {
	if o.config != "" {
		return o.config
	}
	return ".verity/config.yaml"
}

// Options after -- belong to the external Vigil process.
func parseOptions(args []string) ([]string, options, error) {
	var o options
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			out = append(out, args[i+1:]...)
			break
		}
		switch {
		case a == "--help" || a == "-h":
			o.help = true
		case a == "--json":
			o.json = true
		case a == "--config":
			if i+1 == len(args) || strings.HasPrefix(args[i+1], "-") {
				return nil, o, errors.New("--config requires a path")
			}
			i++
			o.config = args[i]
		case strings.HasPrefix(a, "--config="):
			o.config = strings.TrimPrefix(a, "--config=")
			if o.config == "" {
				return nil, o, errors.New("--config requires a path")
			}
		default:
			out = append(out, a)
		}
	}
	return out, o, nil
}

var help = map[string]string{
	"": `Usage: verity [--config PATH] COMMAND [ARGS]

Manage local workflows and verification evidence.

Commands:
  init [DIR]                 Initialize a project
  version                    Print version
  config validate            Validate configuration
  doctor                     Check configuration and database
  dashboard summary          Read a minimal dashboard snapshot
  workflow create|list|show  Create or inspect workflow runs
  task list                  List tasks in a run
  proof show|export          Inspect or export verification evidence
  verify plan|run            Plan or run candidate verification
  work run                   Run a managed Codex job and verification
  chat start|begin|finish    Track work performed in the current chat
  vigil inspect|attach       Inspect or attach a Vigil session
  github receive|workflows   Process deliveries or inspect PR associations

Options:
  --config PATH  Configuration file (default: .verity/config.yaml)
  --json         JSON output for workflow, task, and proof inspection
  -h, --help     Show help for a command

Run 'verity COMMAND --help' for details.`,
	"dashboard":         "Usage: verity dashboard summary --json [--config PATH]",
	"dashboard summary": "Usage: verity dashboard summary --json [--config PATH]\n\nRead a minimal, read-only snapshot.",
	"github":            "Usage: verity github receive <owner/repo> <delivery-id> <sha256-signature> < payload.json",
	"github workflows":  "Usage: verity github workflows <owner/repo>\n\nList exact-SHA pull request associations as JSON.",
	"github receive":    "Usage: verity github receive <owner/repo> <delivery-id> <sha256-signature> < payload.json\n\nSet VERITY_GITHUB_WEBHOOK_SECRET in the environment. Reads a raw pull_request body from stdin.",
	"init":              "Usage: verity init [DIR]\n\nInitialize a project in DIR (default: current directory).",
	"version":           "Usage: verity version\n\nPrint the version.",
	"config":            "Usage: verity config validate [config-path]\n\nValidate configuration. Prefer --config PATH.",
	"config validate":   "Usage: verity config validate [config-path]\n\nValidate configuration. Prefer --config PATH.",
	"doctor":            "Usage: verity doctor [config-path]\n\nCheck configuration and database. Prefer --config PATH.",
	"workflow":          "Usage: verity workflow create|list|show [ARGS]\n\nRun 'verity workflow COMMAND --help' for details.",
	"workflow create":   "Usage: verity workflow create <feature|bugfix|documentation> <definition-id> <run-id> [config-path]\n\nExample: verity workflow create feature feature-definition feature-1",
	"workflow list":     "Usage: verity workflow list [config-path] [--json]\n\nList workflow runs.",
	"workflow show":     "Usage: verity workflow show <run-id> [config-path]\n\nShow a workflow run (use --json for full details).",
	"task":              "Usage: verity task list <run-id> [config-path] [--json]",
	"task list":         "Usage: verity task list <run-id> [config-path] [--json]\n\nList tasks in a workflow run.",
	"proof":             "Usage: verity proof show|export [ARGS]\n\nRun 'verity proof COMMAND --help' for details.",
	"proof show":        "Usage: verity proof show <verification-run-id> [config-path]\n\nShow a verification run (use --json for full details).",
	"proof export":      "Usage: verity proof export <verification-run-id> [output.json] [config-path]\n\nExport a proof bundle.",
	"verify":            "Usage: verity verify plan|run <workflow-id> <candidate-sha> <profile> [approval-digest]",
	"verify plan":       "Usage: verity verify plan <workflow-id> <candidate-sha> <profile>\n\nInspect the plan and its approval digest.",
	"verify run":        "Usage: verity verify run <workflow-id> <candidate-sha> <profile> <approval-digest>\n\nRun the exact approved plan and persist evidence.",
	"verify snapshot":   "Usage: verity verify snapshot <project-relative-path>...\n\nCapture the selected source files without Git.",
	"work":              "Usage: verity work run <instruction-file> <source-path>...",
	"work run":          "Usage: verity work run <instruction-file> <source-path>...\n\nRun a Codex turn, snapshot its output, and verify it.",
	"chat":              "Usage: verity chat start|begin|finish ...",
	"chat start":        "Usage: verity chat start <feature|bugfix|documentation>",
	"chat begin":        "Usage: verity chat begin <workflow-id> <task-id>",
	"chat finish":       "Usage: verity chat finish <workflow-id> <task-id>",
	"chat verify":       "Usage: verity chat verify <workflow-id> <source-path>...",
	"vigil":             "Usage: verity vigil inspect|attach [ARGS]\n\nRun 'verity vigil COMMAND --help' for details. Use -- before options passed to Vigil.",
	"vigil inspect":     "Usage: verity vigil inspect <vigil-binary> <session-id> [turn-id] [-- vigil-args...]",
	"vigil attach":      "Usage: verity vigil attach <link.json> <vigil-binary> [-- vigil-args...]",
}

func showHelp(args []string) error {
	key := strings.Join(args, " ")
	if text, ok := help[key]; ok {
		fmt.Println(text)
		return nil
	}
	return fmt.Errorf("unknown command %q; run 'verity --help'", key)
}
func validateArgs(args []string) error {
	key := args[0]
	if len(args) > 1 && (key == "dashboard" || key == "workflow" || key == "task" || key == "proof" || key == "verify" || key == "work" || key == "chat" || key == "vigil" || key == "config" || key == "github") {
		key += " " + args[1]
	}
	bounds := map[string][2]int{"dashboard summary": {2, 2}, "github receive": {5, 5}, "github workflows": {3, 3}, "version": {1, 1}, "init": {1, 2}, "doctor": {1, 2}, "config validate": {2, 3}, "workflow create": {5, 6}, "workflow list": {2, 3}, "workflow show": {3, 4}, "task list": {3, 4}, "proof show": {3, 4}, "proof export": {3, 5}, "vigil inspect": {4, -1}, "vigil attach": {4, -1}}
	bounds["verify plan"] = [2]int{5, 5}
	bounds["verify run"] = [2]int{6, 6}
	bounds["verify snapshot"] = [2]int{3, -1}
	bounds["work run"] = [2]int{4, -1}
	bounds["chat start"] = [2]int{3, 3}
	bounds["chat begin"] = [2]int{4, 4}
	bounds["chat finish"] = [2]int{4, 4}
	bounds["chat verify"] = [2]int{4, -1}
	b, ok := bounds[key]
	if !ok {
		return fmt.Errorf("unknown or incomplete command %q; run 'verity --help'", key)
	}
	if len(args) < b[0] || (b[1] >= 0 && len(args) > b[1]) {
		return fmt.Errorf("%s; run 'verity %s --help'", strings.Split(help[key], "\n")[0], key)
	}
	if key == "workflow create" && args[2] != "feature" && args[2] != "bugfix" && args[2] != "documentation" {
		return fmt.Errorf("unknown workflow template %q; choose feature, bugfix, or documentation", args[2])
	}
	return nil
}
func loadConfig(path string) (config.Config, error) {
	c, err := config.Load(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, fmt.Errorf("configuration %q not found; run 'verity init' or specify --config PATH: %w", path, err)
	}
	return c, err
}
