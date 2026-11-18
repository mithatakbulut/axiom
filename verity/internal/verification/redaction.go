package verification

import "regexp"

var knownSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?s)-----BEGIN [^-]*PRIVATE KEY-----.*?-----END [^-]*PRIVATE KEY-----`),
	regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`),
	regexp.MustCompile(`\bAKIA[A-Z0-9]{16}\b`),
	regexp.MustCompile(`(?i)(authorization\s*:\s*bearer\s+)[^\s,;]+`),
	regexp.MustCompile(`(?i)(\b[a-z0-9_-]*(?:password|passwd|token|secret|api[_-]?key)[a-z0-9_-]*\b"?)(\s*[:=]\s*)(["']?)([^"'\s,;}&]+)`),
	regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s:]+(?::[^/@\s]*)?@`),
}

// redactKnownSecrets masks common credential formats before command output is
// persisted as verification evidence. It is defense in depth; checks receive
// a reduced environment and must not be given provider credentials.
func redactKnownSecrets(output string) string {
	output = knownSecretPatterns[0].ReplaceAllString(output, "[REDACTED PRIVATE KEY]")
	output = knownSecretPatterns[1].ReplaceAllString(output, "[REDACTED]")
	output = knownSecretPatterns[2].ReplaceAllString(output, "[REDACTED]")
	output = knownSecretPatterns[3].ReplaceAllString(output, `${1}[REDACTED]`)
	output = knownSecretPatterns[4].ReplaceAllString(output, `${1}${2}${3}[REDACTED]`)
	output = knownSecretPatterns[5].ReplaceAllString(output, `${1}[REDACTED]@`)
	return output
}
