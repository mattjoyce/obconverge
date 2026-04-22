package secrets

import (
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
