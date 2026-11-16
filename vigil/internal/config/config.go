package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Models     Models     `yaml:"models" json:"models"`
	Escalation Escalation `yaml:"escalation" json:"escalation"`
	Progress   Progress   `yaml:"progress" json:"progress"`
	Budget     Budget     `yaml:"budget" json:"budget"`
	Resources  Resources  `yaml:"resources" json:"resources"`
	Version    int        `yaml:"version"`
	Mode       string     `yaml:"mode"`
	Storage    struct {
		Path          string `yaml:"path"`
		RetentionDays int    `yaml:"retention_days"`
	} `yaml:"storage"`
}

type Models struct {
	Advisor         string `yaml:"advisor" json:"advisor"`
	ReasoningEffort string `yaml:"reasoning_effort" json:"reasoning_effort"`
}
type Escalation struct {
	Enabled         bool `yaml:"enabled" json:"enabled"`
	Shadow          bool `yaml:"shadow" json:"shadow"`
	RequireApproval bool `yaml:"require_approval" json:"require_approval"`
	MaxPerTurn      int  `yaml:"max_per_turn" json:"max_per_turn"`
	MaxPerTask      int  `yaml:"max_per_task" json:"max_per_task"`
	CooldownSeconds int  `yaml:"cooldown_seconds" json:"cooldown_seconds"`
}
type Progress struct {
	RepeatedErrorThreshold  int  `yaml:"repeated_error_threshold" json:"repeated_error_threshold"`
	MinimumDistinctAttempts int  `yaml:"minimum_distinct_attempts" json:"minimum_distinct_attempts"`
	IgnoreImprovingTests    bool `yaml:"ignore_improving_tests" json:"ignore_improving_tests"`
}
type Budget struct {
	MaxParallelAgents   int  `yaml:"max_parallel_agents" json:"max_parallel_agents"`
	MaxSubagentsPerTask int  `yaml:"max_subagents_per_task" json:"max_subagents_per_task"`
	MaxRuntimeMinutes   int  `yaml:"max_runtime_minutes" json:"max_runtime_minutes"`
	MaxModelRequests    int  `yaml:"max_model_requests" json:"max_model_requests"`
	RequireModelOnSpawn bool `yaml:"require_model_on_spawn" json:"require_model_on_spawn"`
}
type Resources struct {
	MonitoringEnabled  bool `yaml:"monitoring_enabled" json:"monitoring_enabled"`
	MaxCPUPercent      int  `yaml:"max_cpu_percent" json:"max_cpu_percent"`
	MaxProcessMiB      int  `yaml:"max_process_mib" json:"max_process_mib"`
	MinFreeDiskMiB     int  `yaml:"min_free_disk_mib" json:"min_free_disk_mib"`
	LongCommandSeconds int  `yaml:"long_command_seconds" json:"long_command_seconds"`
}

func ValidMode(mode string) bool {
	return mode == "observe" || mode == "assist" || mode == "autonomous"
}

func Default() (Config, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return Config{}, err
	}
	var c Config
	c.Version = 1
	c.Mode = "observe"
	c.Models = Models{Advisor: "gpt-6.1-sol", ReasoningEffort: "high"}
	c.Escalation = Escalation{Enabled: true, Shadow: true, RequireApproval: true, MaxPerTurn: 1, MaxPerTask: 2, CooldownSeconds: 300}
	c.Progress = Progress{RepeatedErrorThreshold: 2, MinimumDistinctAttempts: 2, IgnoreImprovingTests: true}
	c.Budget = Budget{MaxParallelAgents: 2, MaxSubagentsPerTask: 4, MaxRuntimeMinutes: 60, MaxModelRequests: 150, RequireModelOnSpawn: true}
	c.Resources = Resources{MonitoringEnabled: true, MaxCPUPercent: 150, MaxProcessMiB: 2048, MinFreeDiskMiB: 1024, LongCommandSeconds: 300}
	c.Storage.Path = filepath.Join(dir, "Vigil", "vigil.db")
	c.Storage.RetentionDays = 30
	return c, nil
}

// Load uses defaults when the optional configuration file does not exist.
func Load(path string) (Config, error) {
	c, err := Default()
	if err != nil {
		return c, err
	}
	optional := path == ""
	if optional {
		dir, err := os.UserConfigDir()
		if err != nil {
			return c, err
		}
		path = filepath.Join(dir, "Vigil", "config.yaml")
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) && optional {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if len(b) > 64*1024 {
		return c, fmt.Errorf("configuration exceeds size limit")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(b))
	decoder.KnownFields(true)
	if err = decoder.Decode(&c); err != nil {
		return c, fmt.Errorf("invalid configuration: %w", err)
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return c, fmt.Errorf("invalid configuration: only one YAML document is supported")
	}
	if c.Version != 1 || !ValidMode(c.Mode) || c.Storage.Path == "" || c.Storage.RetentionDays < 1 || c.Storage.RetentionDays > 3650 {
		return c, fmt.Errorf("invalid configuration: version must be 1, valid mode, storage path nonempty and retention 1–3650 days")
	}
	if !modelID.MatchString(c.Models.Advisor) || !slices.Contains([]string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}, c.Models.ReasoningEffort) {
		return c, fmt.Errorf("invalid advisor model or reasoning effort")
	}
	e, p, limits, r := c.Escalation, c.Progress, c.Budget, c.Resources
	if e.MaxPerTurn < 1 || e.MaxPerTurn > 10 || e.MaxPerTask < e.MaxPerTurn || e.MaxPerTask > 100 || e.CooldownSeconds < 0 || e.CooldownSeconds > 86400 ||
		p.RepeatedErrorThreshold < 2 || p.RepeatedErrorThreshold > 100 || p.MinimumDistinctAttempts < 2 || p.MinimumDistinctAttempts > 100 ||
		limits.MaxParallelAgents < 1 || limits.MaxParallelAgents > 100 || limits.MaxSubagentsPerTask < 1 || limits.MaxSubagentsPerTask > 1000 ||
		limits.MaxRuntimeMinutes < 1 || limits.MaxRuntimeMinutes > 1440 || limits.MaxModelRequests < 1 || limits.MaxModelRequests > 100000 ||
		r.MaxCPUPercent < 1 || r.MaxCPUPercent > 10000 || r.MaxProcessMiB < 1 || r.MinFreeDiskMiB < 1 || r.LongCommandSeconds < 1 || r.LongCommandSeconds > 86400 {
		return c, fmt.Errorf("progress, resource or budget limit outside supported range")
	}
	if !filepath.IsAbs(c.Storage.Path) {
		c.Storage.Path, err = filepath.Abs(filepath.Join(filepath.Dir(path), c.Storage.Path))
		if err != nil {
			return c, fmt.Errorf("resolve storage path: %w", err)
		}
	}
	return c, nil
}

var modelID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
