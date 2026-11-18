package verification

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"
)

func validateCredentialCheck(plan Plan, check PlannedCheck) error {
	if _, err := checkEgressAllowlist(plan, check); err != nil {
		return err
	}
	if len(check.Credentials) == 0 {
		return nil
	}
	if plan.TrustLevel != TrustUntrusted || plan.IsolationPolicy != IsolationRootlessContainer || check.Adapter != "playwright" || check.BaseURL == "" || len(check.NetworkAllowlist) != 1 {
		return fmt.Errorf("test credentials are restricted to one rootless browser destination")
	}
	u, err := url.ParseRequestURI(check.BaseURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return fmt.Errorf("test credentials require an HTTPS browser base URL")
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	if check.NetworkAllowlist[0] != net.JoinHostPort(strings.ToLower(u.Hostname()), port) {
		return fmt.Errorf("test credential browser destination is not scoped to the base URL")
	}
	return nil
}

func checkEgressAllowlist(plan Plan, check PlannedCheck) ([]string, error) {
	if len(check.NetworkAllowlist) == 0 {
		return plan.NetworkAllowlist, nil
	}
	planned := make(map[string]bool, len(plan.NetworkAllowlist))
	for _, destination := range plan.NetworkAllowlist {
		planned[destination] = true
	}
	for _, destination := range check.NetworkAllowlist {
		if !planned[destination] {
			return nil, fmt.Errorf("check egress destination is outside the approved plan")
		}
	}
	return check.NetworkAllowlist, nil
}

type resolvedCredential struct {
	Name  string
	Value string
	Until time.Time
}

func resolveCredentials(check PlannedCheck, now time.Time) ([]resolvedCredential, time.Duration, error) {
	resolved := make([]resolvedCredential, 0, len(check.Credentials))
	timeout := check.Timeout
	for _, binding := range check.Credentials {
		if !validEnvironmentName(binding.Name) || !strings.HasPrefix(binding.Name, "VERITY_TEST_") || !validEnvironmentName(binding.SourceEnv) || !strings.HasPrefix(binding.SourceEnv, "VERITY_TEST_") || !validEnvironmentName(binding.ExpiresAtEnv) || !strings.HasPrefix(binding.ExpiresAtEnv, "VERITY_TEST_") || binding.Name == binding.SourceEnv || binding.Name == binding.ExpiresAtEnv || binding.SourceEnv == binding.ExpiresAtEnv || binding.TTL < time.Second || binding.TTL > 600*time.Second {
			return nil, 0, fmt.Errorf("invalid test credential policy")
		}
		value, ok := os.LookupEnv(binding.SourceEnv)
		if !ok || value == "" {
			return nil, 0, fmt.Errorf("test credential source %s is unavailable", binding.SourceEnv)
		}
		expiryRaw, ok := os.LookupEnv(binding.ExpiresAtEnv)
		if !ok || expiryRaw == "" {
			return nil, 0, fmt.Errorf("test credential expiry %s is unavailable", binding.ExpiresAtEnv)
		}
		expiresAt, err := time.Parse(time.RFC3339, expiryRaw)
		if err != nil {
			return nil, 0, fmt.Errorf("test credential expiry %s must be RFC3339", binding.ExpiresAtEnv)
		}
		remaining := expiresAt.Sub(now)
		if remaining <= 0 || remaining > binding.TTL {
			return nil, 0, fmt.Errorf("test credential %s is expired or exceeds its declared lifetime", binding.Name)
		}
		if remaining < timeout {
			timeout = remaining
		}
		resolved = append(resolved, resolvedCredential{Name: binding.Name, Value: value, Until: expiresAt})
	}
	return resolved, timeout, nil
}

func credentialsEnvironment(base []string, credentials []resolvedCredential) []string {
	env := append([]string(nil), base...)
	for _, credential := range credentials {
		env = append(env, credential.Name+"="+credential.Value)
	}
	return env
}

func redactCredentialValues(output string, credentials []resolvedCredential) string {
	for _, credential := range credentials {
		if credential.Value != "" {
			output = strings.ReplaceAll(output, credential.Value, "[REDACTED TEST CREDENTIAL]")
		}
	}
	return output
}
