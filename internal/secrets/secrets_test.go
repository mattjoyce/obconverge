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
		name:    "google",
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
		name: "slack",
		// Deliberately non-token-shaped: matches our regex but not GitHub's
		// Slack-token secret scanner (which expects xoxb-<int>-<int>-<alnum>).
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
	d := NewBuiltins()
	for _, tc := range positiveFixtures {
		t.Run(tc.name, func(t *testing.T) {
			got, name := d.Detect([]byte(tc.content))
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
	d := NewBuiltins()
	negatives := []string{
		"just some plain markdown\nnothing interesting",
		"# A note\n\n- thought 1\n- thought 2\n",
		"random short string sk-abc",
		"mentions sk and sk-ant in prose only",
		"url: https://github.com/owner/repo.git",
	}
	for i, content := range negatives {
		if got, _ := d.Detect([]byte(content)); got {
			t.Errorf("negatives[%d] unexpectedly matched: %q", i, content)
		}
	}
}

func TestDetect_ReturnsFirstPatternName(t *testing.T) {
	// Anthropic pattern should win over OpenAI when the content matches both
	// (anthropic has sk-ant- prefix that would otherwise match openai's sk-).
	d := NewBuiltins()
	content := "token: sk-ant-api03-abcDEF_1234567890xyzXYZ0987654321fakefakefake"
	_, name := d.Detect([]byte(content))
	if name != "anthropic" {
		t.Errorf("pattern = %q, want anthropic (it is ordered first)", name)
	}
}

func TestContains_Convenience(t *testing.T) {
	d := NewBuiltins()
	if !d.Contains([]byte("key=AKIAIOSFODNN7EXAMPLE")) {
		t.Error("Contains should match the AWS fixture")
	}
	if d.Contains([]byte("plain text only")) {
		t.Error("Contains should not match plain text")
	}
}

func TestDetect_NoContentLeakage(t *testing.T) {
	d := NewBuiltins()
	secret := "sk-ant-api03-abcDEF_1234567890xyzXYZ0987654321fakefakefake"
	_, name := d.Detect([]byte("some prose " + secret + " more prose"))
	if strings.Contains(name, secret) {
		t.Error("pattern name leaked secret content")
	}
}

func TestBuiltins_MatchesShippedNames(t *testing.T) {
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

func TestParseFile_ReadsUserExtension(t *testing.T) {
	path := writeJSON(t, t.TempDir(), `{
		"patterns": [
			{"name": "corp-token", "regex": "CORP-[A-Z0-9]{16}", "description": "Internal corp tokens"}
		]
	}`)
	got, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(got) != 1 || got[0].Name != "corp-token" {
		t.Errorf("unexpected patterns: %+v", got)
	}
}

func TestParseFile_MissingReturnsNilNoError(t *testing.T) {
	got, err := ParseFile(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Errorf("missing file should not error, got %v", err)
	}
	if got != nil {
		t.Errorf("missing file should return nil, got %+v", got)
	}
}

func TestParseFile_EmptyPathReturnsNilNoError(t *testing.T) {
	got, err := ParseFile("")
	if err != nil {
		t.Errorf("empty path should not error, got %v", err)
	}
	if got != nil {
		t.Errorf("empty path should return nil, got %+v", got)
	}
}

func TestParseFile_InvalidRegexIsError(t *testing.T) {
	path := writeJSON(t, t.TempDir(), `{
		"patterns": [
			{"name": "bad", "regex": "(unclosed", "description": "bad regex"}
		]
	}`)
	if _, err := ParseFile(path); err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestCombine_AppendsAndPreservesBase(t *testing.T) {
	base, err := Builtins()
	if err != nil {
		t.Fatalf("Builtins: %v", err)
	}
	extra, err := ParseFile(writeJSON(t, t.TempDir(), `{
		"patterns": [
			{"name": "corp-token", "regex": "CORP-[A-Z0-9]{16}"}
		]
	}`))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	merged, err := Combine(base, extra)
	if err != nil {
		t.Fatalf("Combine: %v", err)
	}
	if len(merged) != len(base)+1 {
		t.Errorf("merged len = %d, want %d", len(merged), len(base)+1)
	}

	d := New(merged)
	// Built-in still fires.
	if !d.Contains([]byte("AKIAIOSFODNN7EXAMPLE")) {
		t.Error("built-in aws pattern missing from combined detector")
	}
	// User extension fires.
	matched, name := d.Detect([]byte("key: CORP-ABC123XYZ9876QRST"))
	if !matched || name != "corp-token" {
		t.Errorf("corp-token not detected: matched=%v name=%q", matched, name)
	}
}

func TestCombine_UserNameCollidingWithBuiltinIsError(t *testing.T) {
	base, err := Builtins()
	if err != nil {
		t.Fatalf("Builtins: %v", err)
	}
	extra := []Pattern{{Name: "openai", Description: "shadow attempt"}}
	_, err = Combine(base, extra)
	if err == nil {
		t.Fatal("expected error for name collision with built-in")
	}
	if !strings.Contains(err.Error(), "openai") {
		t.Errorf("error should name the colliding pattern: %v", err)
	}
}

func TestCombine_DuplicateWithinExtraIsError(t *testing.T) {
	extra := []Pattern{
		{Name: "corp-token"},
		{Name: "corp-token"},
	}
	if _, err := Combine(nil, extra); err == nil {
		t.Fatal("expected error for duplicate name within extras")
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

// TestNew_DefensiveCopy verifies mutating the caller's slice after
// construction doesn't corrupt the detector.
func TestNew_DefensiveCopy(t *testing.T) {
	built, err := Builtins()
	if err != nil {
		t.Fatalf("Builtins: %v", err)
	}
	d := New(built)
	// Mutate the caller's slice — clobber every pattern.
	for i := range built {
		built[i] = Pattern{}
	}
	// Detector must still work.
	if !d.Contains([]byte("AKIAIOSFODNN7EXAMPLE")) {
		t.Error("detector was affected by caller's slice mutation")
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
