package verification

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const ManifestPath = ".verity/verification.yaml"

type Profile string
type IsolationPolicy string

const IsolationLocalProcess IsolationPolicy = "local_process_reduced_env"
const IsolationRootlessContainer IsolationPolicy = "rootless_container_no_network"
const TrustUntrusted = "untrusted"
const TrustTrusted = "trusted"

const (
	Minimal   Profile = "minimal"
	PreCommit Profile = "pre-commit"
	PrePush   Profile = "pre-push"
	Standard  Profile = "standard"
	Strict    Profile = "strict"
	Full      Profile = "full"
	Release   Profile = "release"
	Custom    Profile = "custom"
)

type Manifest struct {
	Version          int                     `yaml:"version" json:"version"`
	TrustLevel       string                  `yaml:"trust_level,omitempty" json:"trust_level,omitempty"`
	Isolation        IsolationPolicy         `yaml:"isolation,omitempty" json:"isolation,omitempty"`
	ContainerImage   string                  `yaml:"container_image,omitempty" json:"container_image,omitempty"`
	NetworkAllowlist []string                `yaml:"network_allowlist,omitempty" json:"network_allowlist,omitempty"`
	Commands         map[string]CommandCheck `yaml:"commands" json:"commands"`
	Criteria         []Criterion             `yaml:"criteria,omitempty" json:"criteria,omitempty"`
	Profiles         map[Profile][]string    `yaml:"profiles,omitempty" json:"profiles,omitempty"`
	Review           bool                    `yaml:"review,omitempty" json:"review,omitempty"`
	Browser          *BrowserConfig          `yaml:"browser,omitempty" json:"browser,omitempty"`
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
	ID               string           `yaml:"id" json:"id"`
	Description      string           `yaml:"description" json:"description"`
	Required         bool             `yaml:"required" json:"required"`
	Spec             string           `yaml:"spec" json:"spec"`
	Criteria         []string         `yaml:"criteria,omitempty" json:"criteria,omitempty"`
	TestCredentials  []TestCredential `yaml:"test_credentials,omitempty" json:"test_credentials,omitempty"`
	ExposeToTestCode bool             `yaml:"expose_to_test_code,omitempty" json:"expose_to_test_code,omitempty"`
}

// TestCredential names a short-lived host secret made available only to one
// explicitly authorized browser scenario. Secret values never enter the plan.
type TestCredential struct {
	Name         string `yaml:"name" json:"name"`
	SourceEnv    string `yaml:"source_env" json:"source_env"`
	ExpiresAtEnv string `yaml:"expires_at_env" json:"expires_at_env"`
	TTLSeconds   int    `yaml:"ttl_seconds" json:"ttl_seconds"`
}

type PlannedCredential struct {
	Name         string        `json:"name"`
	SourceEnv    string        `json:"source_env"`
	ExpiresAtEnv string        `json:"expires_at_env"`
	TTL          time.Duration `json:"ttl"`
}

type PlannedCheck struct {
	ID               string              `json:"id"`
	Name             string              `json:"name"`
	Adapter          string              `json:"adapter,omitempty"`
	Argv             []string            `json:"argv"`
	Required         bool                `json:"required"`
	Timeout          time.Duration       `json:"timeout"`
	CriteriaIDs      []string            `json:"criteria_ids,omitempty"`
	BaseURL          string              `json:"base_url,omitempty"`
	Credentials      []PlannedCredential `json:"credentials,omitempty"`
	NetworkAllowlist []string            `json:"network_allowlist,omitempty"`
}
type Plan struct {
	CandidateSHA     string          `json:"candidate_sha"`
	TrustLevel       string          `json:"trust_level"`
	IsolationPolicy  IsolationPolicy `json:"isolation_policy"`
	ContainerImage   string          `json:"container_image,omitempty"`
	NetworkAllowlist []string        `json:"network_allowlist,omitempty"`
	Profile          Profile         `json:"profile"`
	ManifestVersion  int             `json:"manifest_version"`
	Checks           []PlannedCheck  `json:"checks"`
	RequiresReview   bool            `json:"requires_review"`
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
	if m.Isolation != "" && m.Isolation != IsolationLocalProcess && m.Isolation != IsolationRootlessContainer {
		return ErrInvalidManifest
	}
	if m.TrustLevel != "" && m.TrustLevel != TrustTrusted && m.TrustLevel != TrustUntrusted {
		return ErrInvalidManifest
	}
	if m.TrustLevel == TrustUntrusted && m.Isolation != IsolationRootlessContainer {
		return fmt.Errorf("%w: untrusted checks require rootless container isolation", ErrInvalidManifest)
	}
	if len(m.NetworkAllowlist) > 0 && (m.TrustLevel != TrustUntrusted || m.Isolation != IsolationRootlessContainer) {
		return fmt.Errorf("%w: network allowlist requires untrusted rootless container isolation", ErrInvalidManifest)
	}
	seenDestinations := map[string]bool{}
	for _, destination := range m.NetworkAllowlist {
		if !validNetworkDestination(destination) || seenDestinations[destination] {
			return fmt.Errorf("%w: network allowlist entries must be unique exact public DNS host:port destinations", ErrInvalidManifest)
		}
		seenDestinations[destination] = true
	}
	if m.Isolation == IsolationRootlessContainer && m.ContainerImage != "" && !validContainerImage(m.ContainerImage) {
		return ErrInvalidManifest
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
			if err := validateBrowserBaseURL(m.Browser.BaseURL); err != nil {
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
			if len(scenario.TestCredentials) > 0 {
				if !scenario.ExposeToTestCode || m.TrustLevel != TrustUntrusted || m.Isolation != IsolationRootlessContainer || m.Browser.BaseURL == "" {
					return fmt.Errorf("%w: test credentials require explicit test-code exposure and untrusted rootless browser isolation", ErrInvalidManifest)
				}
				u, _ := url.Parse(m.Browser.BaseURL)
				if u.Scheme != "https" {
					return fmt.Errorf("%w: test credentials require an HTTPS browser base URL", ErrInvalidManifest)
				}
				port := u.Port()
				if port == "" {
					port = "443"
				}
				destination := net.JoinHostPort(strings.ToLower(u.Hostname()), port)
				allowed := false
				for _, candidate := range m.NetworkAllowlist {
					if candidate == destination {
						allowed = true
					}
				}
				if !allowed {
					return fmt.Errorf("%w: credential browser destination must be explicitly allowlisted", ErrInvalidManifest)
				}
				seenNames, seenSources := map[string]bool{}, map[string]bool{}
				for _, credential := range scenario.TestCredentials {
					if !validEnvironmentName(credential.Name) || !strings.HasPrefix(credential.Name, "VERITY_TEST_") || !validEnvironmentName(credential.SourceEnv) || !strings.HasPrefix(credential.SourceEnv, "VERITY_TEST_") || !validEnvironmentName(credential.ExpiresAtEnv) || !strings.HasPrefix(credential.ExpiresAtEnv, "VERITY_TEST_") || credential.Name == credential.SourceEnv || credential.Name == credential.ExpiresAtEnv || credential.SourceEnv == credential.ExpiresAtEnv || credential.TTLSeconds < 1 || credential.TTLSeconds > 600 || seenNames[credential.Name] || seenSources[credential.SourceEnv] || seenSources[credential.ExpiresAtEnv] {
						return fmt.Errorf("%w: invalid or duplicate browser test credential binding", ErrInvalidManifest)
					}
					seenNames[credential.Name], seenSources[credential.SourceEnv] = true, true
					seenSources[credential.ExpiresAtEnv] = true
				}
			} else if scenario.ExposeToTestCode {
				return fmt.Errorf("%w: test-code credential exposure requires at least one credential binding", ErrInvalidManifest)
			}
		}
	}
	return nil
}

func validateBrowserBaseURL(raw string) error {
	u, err := url.ParseRequestURI(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ErrInvalidManifest
	}
	for key := range u.Query() {
		normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
		for _, suffix := range []string{"token", "secret", "password", "passwd", "apikey", "credential", "authorization"} {
			if normalized == suffix || strings.HasSuffix(normalized, suffix) {
				return ErrInvalidManifest
			}
		}
		if normalized == "auth" || normalized == "key" {
			return ErrInvalidManifest
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
	policy := m.Isolation
	if policy == "" {
		policy = IsolationLocalProcess
	}
	trustLevel := m.TrustLevel
	if trustLevel == "" {
		trustLevel = TrustTrusted
	}
	allowlist := append([]string(nil), m.NetworkAllowlist...)
	sort.Strings(allowlist)
	plan := Plan{CandidateSHA: candidate, TrustLevel: trustLevel, IsolationPolicy: policy, ContainerImage: m.ContainerImage, NetworkAllowlist: allowlist, Profile: profile, ManifestVersion: m.Version, RequiresReview: m.Review}
	for _, id := range ids {
		c := m.Commands[id]
		argv, _ := commandArgv(c.Command)
		plan.Checks = append(plan.Checks, PlannedCheck{ID: id, Name: id, Argv: argv, Required: c.Required, Timeout: time.Duration(c.TimeoutSeconds) * time.Second, CriteriaIDs: append([]string(nil), c.Criteria...)})
	}
	if m.Browser != nil {
		for _, scenario := range m.Browser.Scenarios {
			check := PlannedCheck{ID: "browser-" + scenario.ID, Name: scenario.Description, Adapter: "playwright", Argv: []string{scenario.Spec}, Required: scenario.Required, Timeout: 5 * time.Minute, BaseURL: m.Browser.BaseURL, CriteriaIDs: append([]string(nil), scenario.Criteria...)}
			if len(scenario.TestCredentials) > 0 {
				u, _ := url.Parse(m.Browser.BaseURL)
				port := u.Port()
				if port == "" {
					port = "443"
				}
				check.NetworkAllowlist = []string{net.JoinHostPort(strings.ToLower(u.Hostname()), port)}
				for _, credential := range scenario.TestCredentials {
					check.Credentials = append(check.Credentials, PlannedCredential{Name: credential.Name, SourceEnv: credential.SourceEnv, ExpiresAtEnv: credential.ExpiresAtEnv, TTL: time.Duration(credential.TTLSeconds) * time.Second})
				}
			}
			plan.Checks = append(plan.Checks, check)
		}
	}
	return plan, nil
}

func validEnvironmentName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if !(r == '_' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func validNetworkDestination(value string) bool {
	host, port, err := net.SplitHostPort(value)
	if err != nil || host != strings.ToLower(host) || strings.HasSuffix(host, ".") || net.ParseIP(host) != nil || host == "localhost" || strings.Contains(host, "..") || !strings.Contains(host, ".") {
		return false
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return false
	}
	if strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".localhost") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return len(host) <= 253
}

func validContainerImage(image string) bool {
	marker := "@sha256:"
	i := strings.LastIndex(image, marker)
	if i <= 0 || len(image)-i-len(marker) != 64 {
		return false
	}
	if _, err := hex.DecodeString(image[i+len(marker):]); err != nil {
		return false
	}
	return !strings.ContainsAny(image, " \t\n\r")
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
	return p == Minimal || p == PreCommit || p == PrePush || p == Standard || p == Strict || p == Full || p == Release || p == Custom
}
