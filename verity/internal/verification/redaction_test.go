package verification

import (
	"strings"
	"testing"
)

func TestRedactKnownSecrets(t *testing.T) {
	input := strings.Join([]string{
		`{"token":"super-secret"}`,
		"GITHUB_TOKEN=github-secret-value",
		"aws_secret_access_key: hidden-access-key",
		"password=hunter2",
		"Authorization: Bearer opaque-bearer-value",
		"ghp_123456789012345678901234567890123456",
		"AKIAABCDEFGHIJKLMNOP",
		"https://user:password@example.test/path",
		"-----BEGIN RSA PRIVATE KEY-----\nprivate-material\n-----END RSA PRIVATE KEY-----",
		"ordinary verification output",
	}, "\n")
	got := redactKnownSecrets(input)
	for _, secret := range []string{"super-secret", "github-secret-value", "hidden-access-key", "hunter2", "opaque-bearer-value", "ghp_123456789012345678901234567890123456", "AKIAABCDEFGHIJKLMNOP", "user:password", "private-material"} {
		if strings.Contains(got, secret) {
			t.Errorf("secret %q remains in redacted output: %s", secret, got)
		}
	}
	if !strings.Contains(got, "ordinary verification output") || !strings.Contains(got, `"token":"[REDACTED]"`) {
		t.Fatalf("redaction lost safe output or JSON shape: %s", got)
	}
}
