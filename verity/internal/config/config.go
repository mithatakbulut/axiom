package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	SchemaVersion int    `yaml:"schema_version" json:"schema_version"`
	Database      string `yaml:"database" json:"database"`
	LogLevel      string `yaml:"log_level" json:"log_level"`
	DataDir       string `yaml:"data_dir" json:"data_dir"`
	CodexModel    string `yaml:"codex_model,omitempty" json:"codex_model,omitempty"`
}

func Defaults() Config {
	return Config{SchemaVersion: 1, Database: ".verity/verity.db", LogLevel: "info", DataDir: ".verity"}
}
func Load(path string) (Config, error) {
	c := Defaults()
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err = yaml.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("decode config: %w", err)
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	if c.SchemaVersion != 1 {
		return errors.New("schema_version must be 1")
	}
	if strings.TrimSpace(c.Database) == "" || strings.TrimSpace(c.DataDir) == "" {
		return errors.New("database and data_dir are required")
	}
	if len(c.CodexModel) > 128 || strings.HasPrefix(c.CodexModel, "-") || strings.ContainsAny(c.CodexModel, " \t\n\r") {
		return errors.New("codex_model must be a model identifier")
	}
	switch strings.ToLower(c.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("unsupported log_level %q", c.LogLevel)
	}
	if filepath.IsAbs(c.Database) {
		return errors.New("database path must be relative")
	}
	return nil
}
