package verification

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const ManifestPath = ".verity/verification.yaml"

type Profile string

const (
	Minimal  Profile = "minimal"
	Standard Profile = "standard"
	Strict   Profile = "strict"
	Release  Profile = "release"
	Custom   Profile = "custom"
)

type Manifest struct {
	Version  int                     `yaml:"version" json:"version"`
	Commands map[string]CommandCheck `yaml:"commands" json:"commands"`
	Criteria []Criterion             `yaml:"criteria,omitempty" json:"criteria,omitempty"`
	Profiles map[Profile][]string    `yaml:"profiles,omitempty" json:"profiles,omitempty"`
	Review   bool                    `yaml:"review,omitempty" json:"review,omitempty"`
	Browser  *BrowserConfig          `yaml:"browser,omitempty" json:"browser,omitempty"`
}

type CommandCheck struct {
	Command        any      `yaml:"command" json:"command"` // string (argv words only) or []string; never a shell expression.
	Required       bool     `yaml:"required" json:"required"`
	TimeoutSeconds int      `yaml:"timeout_seconds" json:"timeout_seconds"`
	Criteria       []string `yaml:"criteria,omitempty" json:"criteria,omitempty"`
}

type Criterion struct {
	ID          string `yaml:"id" json:"id"`
	Description string `yaml:"description" json:"description"`
}
type BrowserConfig struct {
	Adapter   string            `yaml:"adapter" json:"adapter"`
	BaseURL   string            `yaml:"base_url" json:"base_url"`
	Scenarios []BrowserScenario `yaml:"scenarios,omitempty" json:"scenarios,omitempty"`
}
type BrowserScenario struct {
	ID          string   `yaml:"id" json:"id"`
	Description string   `yaml:"description" json:"description"`
	Required    bool     `yaml:"required" json:"required"`
	Spec        string   `yaml:"spec" json:"spec"`
	Criteria    []string `yaml:"criteria,omitempty" json:"criteria,omitempty"`
}

type PlannedCheck struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Adapter     string        `json:"adapter,omitempty"`
	Argv        []string      `json:"argv"`
	Required    bool          `json:"required"`
	Timeout     time.Duration `json:"timeout"`
	CriteriaIDs []string      `json:"criteria_ids,omitempty"`
	BaseURL     string        `json:"base_url,omitempty"`
}
type Plan struct {
	CandidateSHA    string         `json:"candidate_sha"`
	Profile         Profile        `json:"profile"`
	ManifestVersion int            `json:"manifest_version"`
	Checks          []PlannedCheck `json:"checks"`
	RequiresReview  bool           `json:"requires_review"`
}

func LoadManifest(path string) (Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	return ParseManifest(b)
}
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("decode verification manifest: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return Manifest{}, fmt.Errorf("verification manifest must contain one document")
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}
func (m Manifest) Validate() error {
	if m.Version != 1 {
		return ErrUnsupportedManifest
	}
	if len(m.Commands) == 0 && m.Browser == nil && !m.Review {
		return ErrEmptyManifest
	}
	criteria := map[string]bool{}
	for _, c := range m.Criteria {
		if c.ID == "" || strings.TrimSpace(c.Description) == "" || criteria[c.ID] {
			return ErrInvalidManifest
		}
		criteria[c.ID] = true
	}
	for id, c := range m.Commands {
		if !validName(id) || c.TimeoutSeconds < 1 || c.TimeoutSeconds > 3600 {
			return ErrInvalidManifest
		}
		if _, err := commandArgv(c.Command); err != nil {
			return fmt.Errorf("check %s: %w", id, err)
		}
		for _, criterion := range c.Criteria {
			if !criteria[criterion] {
				return fmt.Errorf("%w: check %s references unknown criterion %s", ErrInvalidManifest, id, criterion)
			}
		}
	}
	for p, checks := range m.Profiles {
		if !validProfile(p) {
			return ErrInvalidManifest
		}
		for _, id := range checks {
			if _, ok := m.Commands[id]; !ok {
				return fmt.Errorf("%w: profile %s references unknown check %s", ErrInvalidManifest, p, id)
			}
		}
	}
	if m.Browser != nil {
		if m.Browser.Adapter != "playwright" {
			return ErrInvalidManifest
		}
		if m.Browser.BaseURL != "" {
			u, err := url.ParseRequestURI(m.Browser.BaseURL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return ErrInvalidManifest
			}
		}
		for _, scenario := range m.Browser.Scenarios {
			if !validName(scenario.ID) || strings.TrimSpace(scenario.Description) == "" || scenario.Spec == "" || filepath.IsAbs(scenario.Spec) || strings.HasPrefix(filepath.Clean(scenario.Spec), "..") {
				return ErrInvalidManifest
			}
			for _, criterion := range scenario.Criteria {
				if !criteria[criterion] {
					return fmt.Errorf("%w: browser scenario %s references unknown criterion %s", ErrInvalidManifest, scenario.ID, criterion)
				}
			}
		}
	}
	return nil
}

func (m Manifest) Plan(candidate string, profile Profile) (Plan, error) {
	if err := m.Validate(); err != nil {
		return Plan{}, err
	}
	if !validRevision(candidate) {
		return Plan{}, ErrInvalidCandidate
	}
	if !validProfile(profile) {
		return Plan{}, ErrInvalidProfile
	}
	ids, ok := m.Profiles[profile]
	if !ok {
		ids = make([]string, 0, len(m.Commands))
		for id := range m.Commands {
			ids = append(ids, id)
		}
		sort.Strings(ids)
	}
	plan := Plan{CandidateSHA: candidate, Profile: profile, ManifestVersion: m.Version, RequiresReview: m.Review}
	for _, id := range ids {
		c := m.Commands[id]
		argv, _ := commandArgv(c.Command)
		plan.Checks = append(plan.Checks, PlannedCheck{ID: id, Name: id, Argv: argv, Required: c.Required, Timeout: time.Duration(c.TimeoutSeconds) * time.Second, CriteriaIDs: append([]string(nil), c.Criteria...)})
	}
	if m.Browser != nil {
		for _, scenario := range m.Browser.Scenarios {
			plan.Checks = append(plan.Checks, PlannedCheck{ID: "browser-" + scenario.ID, Name: scenario.Description, Adapter: "playwright", Argv: []string{scenario.Spec}, Required: scenario.Required, Timeout: 5 * time.Minute, BaseURL: m.Browser.BaseURL, CriteriaIDs: append([]string(nil), scenario.Criteria...)})
		}
	}
	return plan, nil
}
func commandArgv(value any) ([]string, error) {
	var argv []string
	switch v := value.(type) {
	case string:
		if strings.ContainsAny(v, "|;&<>\n\r") || strings.Contains(v, "$()") || strings.Contains(v, "`") {
			return nil, ErrUnsafeCommand
		}
		argv = strings.Fields(v)
	case []any:
		for _, a := range v {
			s, ok := a.(string)
			if !ok {
				return nil, ErrUnsafeCommand
			}
			argv = append(argv, s)
		}
	case []string:
		argv = append(argv, v...)
	default:
		return nil, ErrUnsafeCommand
	}
	if len(argv) == 0 || filepath.IsAbs(argv[0]) || strings.Contains(argv[0], "/") {
		return nil, ErrUnsafeCommand
	}
	for _, a := range argv {
		if strings.ContainsAny(a, "\x00\n\r") {
			return nil, ErrUnsafeCommand
		}
	}
	return argv, nil
}
func validName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
func validProfile(p Profile) bool {
	return p == Minimal || p == Standard || p == Strict || p == Release || p == Custom
}
