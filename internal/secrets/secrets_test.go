package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Synthesized test fixtures. None of these are real secrets — they match the
// shape but not any allocated credential. Do not commit real keys.
//
// revive:disable:line-length-limit
var positiveFixtures = []struct {
	name    string
	content string
	pattern string
}{
	{
		name:    "anthropic",
		content: "key: sk-ant-api03-abcDEF_1234567890xyzXYZ0987654321fakefakefake",
		pattern: "anthropic",
	},
	{
		name:    "openai",
		content: "const key = 'sk-abc123XYZ789defGHI456jkl'",
		pattern: "openai",
	},
	{
		name:    "aws",
		content: "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
		pattern: "aws-access-key",
	},
	{
		name: "google",
		// Google API keys are AIza + 35 chars, 39 total.
		content: "export GOOGLE_API_KEY=AIzaSyDxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		pattern: "google-api",
	},
	{
		name:    "github-pat",
		content: "# token\nghp_0123456789abcdefghijklmnopqrstuvwxyzAB\n",
		pattern: "github-pat",
	},
	{
		name:    "github-fine",
		content: "TOKEN=github_pat_11AABCDEFG0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ012",
		pattern: "github-fine",
	},
	{
		name:    "jwt",
		content: "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.fakeSignatureXYZ123",
		pattern: "jwt",
	},
	{
		name:    "slack",
		content: "token: xoxb-TESTFIXTURE-NOTAREALSLACKTOKEN",
		pattern: "slack",
	},
	{
		name:    "pem",
		content: "-----BEGIN RSA PRIVATE KEY-----\nMIIEogIBAAKCAQEA...",
		pattern: "pem",
	},
}

func TestDetect_MatchesPositiveFixtures(t *testing.T) {
	for _, tc := range positiveFixtures {
		t.Run(tc.name, func(t *testing.T) {
			got, name := Detect([]byte(tc.content))
			if !got {
				t.Errorf("Detect returned false for %s fixture\ncontent: %s", tc.name, tc.content)
			}
			if name != tc.pattern {
				t.Errorf("pattern = %q, want %q", name, tc.pattern)
			}
		})
	}
}

func TestDetect_NegativeFixtures(t *testing.T) {
	negatives := []string{
		"just some plain markdown\nnothing interesting",
		"# A note\n\n- thought 1\n- thought 2\n",
		"random short string sk-abc",             // too short to match openai
		"mentions sk and sk-ant in prose only",   // not credential-shaped
		"url: https://github.com/owner/repo.git", // contains github but not a token
	}
	for i, content := range negatives {
		if got, _ := Detect([]byte(content)); got {
			t.Errorf("negatives[%d] unexpectedly matched: %q", i, content)
		}
	}
}

func TestDetect_ReturnsFirstPatternName(t *testing.T) {
	// Anthropic pattern should win over OpenAI when the content matches both
	// (anthropic has sk-ant- prefix that would otherwise match openai's sk-).
	content := "token: sk-ant-api03-abcDEF_1234567890xyzXYZ0987654321fakefakefake"
	_, name := Detect([]byte(content))
	if name != "anthropic" {
		t.Errorf("pattern = %q, want anthropic (it is ordered first)", name)
	}
}

func TestContains_Convenience(t *testing.T) {
	if !Contains([]byte("key=AKIAIOSFODNN7EXAMPLE")) {
		t.Error("Contains should match the AWS fixture")
	}
	if Contains([]byte("plain text only")) {
		t.Error("Contains should not match plain text")
	}
}

// TestNoContentLeakage guards the spec's core invariant for this package:
// the matched credential must never appear in the returned value. Detect
// returns only a bool and a pattern name — content is never in scope.
func TestNoContentLeakage(t *testing.T) {
	secret := "sk-ant-api03-abcDEF_1234567890xyzXYZ0987654321fakefakefake"
	_, name := Detect([]byte("some prose " + secret + " more prose"))
	if strings.Contains(name, secret) {
		t.Error("pattern name leaked secret content")
	}
}

func TestBuiltinNames_MatchesShippedPatterns(t *testing.T) {
	want := []string{
		"anthropic", "openai", "aws-access-key", "google-api",
		"github-pat", "github-fine", "jwt", "slack", "pem",
	}
	got := BuiltinNames()
	if len(got) != len(want) {
		t.Fatalf("BuiltinNames count = %d, want %d: got %v", len(got), len(want), got)
	}
	seen := map[string]bool{}
	for _, n := range got {
		seen[n] = true
	}
	for _, w := range want {
		if !seen[w] {
			t.Errorf("missing built-in pattern %q", w)
		}
	}
}

func TestLoadUserExtensions_AddsNewPattern(t *testing.T) {
	t.Cleanup(Reset)

	path := writeJSON(t, t.TempDir(), `{
		"patterns": [
			{"name": "corp-token", "regex": "CORP-[A-Z0-9]{16}", "description": "Internal corp tokens"}
		]
	}`)

	if err := LoadUserExtensions(path); err != nil {
		t.Fatalf("LoadUserExtensions: %v", err)
	}
	if !Contains([]byte("AKIAIOSFODNN7EXAMPLE")) {
		t.Error("built-in aws pattern no longer detects after user extension loaded")
	}
	matched, name := Detect([]byte("key: CORP-ABC123XYZ9876QRST"))
	if !matched {
		t.Error("user extension pattern should match its fixture")
	}
	if name != "corp-token" {
		t.Errorf("name = %q, want corp-token", name)
	}
}

func TestLoadUserExtensions_MissingFileIsNoOp(t *testing.T) {
	t.Cleanup(Reset)
	if err := LoadUserExtensions(filepath.Join(t.TempDir(), "does-not-exist.json")); err != nil {
		t.Errorf("missing file should not error, got %v", err)
	}
	if !Contains([]byte("AKIAIOSFODNN7EXAMPLE")) {
		t.Error("built-ins broken after no-op extension load")
	}
}

func TestLoadUserExtensions_DuplicateNameIsError(t *testing.T) {
	t.Cleanup(Reset)
	path := writeJSON(t, t.TempDir(), `{
		"patterns": [
			{"name": "openai", "regex": "anything", "description": "tries to shadow built-in"}
		]
	}`)
	err := LoadUserExtensions(path)
	if err == nil {
		t.Fatal("expected error for name collision with built-in")
	}
	if !strings.Contains(err.Error(), "openai") {
		t.Errorf("error should name the colliding pattern: %v", err)
	}
}

func TestLoadUserExtensions_InvalidRegexIsError(t *testing.T) {
	t.Cleanup(Reset)
	path := writeJSON(t, t.TempDir(), `{
		"patterns": [
			{"name": "bad", "regex": "(unclosed", "description": "bad regex"}
		]
	}`)
	if err := LoadUserExtensions(path); err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestLoadUserExtensions_EmptyPathIsNoOp(t *testing.T) {
	t.Cleanup(Reset)
	if err := LoadUserExtensions(""); err != nil {
		t.Errorf("empty path should not error, got %v", err)
	}
}

func TestReset_DiscardsUserExtensions(t *testing.T) {
	t.Cleanup(Reset)
	path := writeJSON(t, t.TempDir(), `{
		"patterns": [
			{"name": "one-shot", "regex": "ONESHOT-[A-Z]+", "description": "ephemeral"}
		]
	}`)
	if err := LoadUserExtensions(path); err != nil {
		t.Fatalf("LoadUserExtensions: %v", err)
	}
	if !Contains([]byte("ONESHOT-ABC")) {
		t.Fatal("extension didn't load in the first place")
	}
	Reset()
	if Contains([]byte("ONESHOT-ABC")) {
		t.Error("Reset should discard user extensions")
	}
	if !Contains([]byte("AKIAIOSFODNN7EXAMPLE")) {
		t.Error("Reset clobbered built-ins")
	}
}

func TestDefaultUserExtensionPath(t *testing.T) {
	got, err := DefaultUserExtensionPath()
	if err != nil {
		t.Fatalf("DefaultUserExtensionPath: %v", err)
	}
	if !strings.HasSuffix(got, "/.config/obconverge/secret_patterns.json") {
		t.Errorf("path = %q, want suffix /.config/obconverge/secret_patterns.json", got)
	}
}

func writeJSON(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "secret_patterns.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}
