// Package secrets detects credential-shaped strings in note content.
//
// The detector is intentionally pattern-based and conservative. False
// positives are cheap (a file routes into the SECRETS bucket and the
// operator reviews it in Obsidian); false negatives leak credentials into
// terminal scrollback and plan files. Err toward over-matching.
//
// Per SPEC.md "Secret protection": a file in the SECRETS bucket must NEVER
// have its content printed to stdout, stderr, log, or plan. Callers must
// only ever expose the bucket verdict and a redacted fingerprint.
package secrets

import (
	"regexp"
)

// Pattern names a detector.
type Pattern struct {
	Name  string
	Regex *regexp.Regexp
}

// Patterns is the ordered list of credential detectors. Order matters only
// when we report *which* pattern matched first.
var Patterns = []Pattern{
	// Anthropic API keys: "sk-ant-api03-..." and similar variants.
	{Name: "anthropic", Regex: regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{20,}`)},
	// OpenAI API keys: "sk-..." (must not swallow sk-ant- above because the
	// anthropic pattern is checked first).
	{Name: "openai", Regex: regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}\b`)},
	// AWS access key IDs.
	{Name: "aws-access-key", Regex: regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	// Google API keys.
	{Name: "google-api", Regex: regexp.MustCompile(`\bAIza[A-Za-z0-9_\-]{35}\b`)},
	// GitHub personal access tokens (classic ghp_, fine-grained github_pat_).
	{Name: "github-pat", Regex: regexp.MustCompile(`\bghp_[A-Za-z0-9]{30,}\b`)},
	{Name: "github-fine", Regex: regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{30,}\b`)},
	// JSON Web Tokens: three base64url segments separated by dots, typically
	// starting with "eyJ" (which is `{"` base64-url-encoded).
	{Name: "jwt", Regex: regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{10,}\.eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\b`)},
	// Slack tokens.
	{Name: "slack", Regex: regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9\-]{10,}\b`)},
	// Private-key PEM headers.
	{Name: "pem", Regex: regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |ENCRYPTED |PGP )?PRIVATE KEY-----`)},
}

// Detect scans content and returns (matched, patternName). patternName is the
// name of the first matching pattern (useful for logging / bucket metadata);
// the content itself is never returned.
func Detect(content []byte) (bool, string) {
	for _, p := range Patterns {
		if p.Regex.Match(content) {
			return true, p.Name
		}
	}
	return false, ""
}

// Contains is a convenience predicate for callers that only need the verdict.
func Contains(content []byte) bool {
	matched, _ := Detect(content)
	return matched
}
