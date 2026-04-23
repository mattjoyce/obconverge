package skills_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mattjoyce/obconverge/internal/secrets"
	"github.com/mattjoyce/obconverge/internal/skills"
)

func TestJSON_IsValid(t *testing.T) {
	var raw any
	if err := json.Unmarshal(skills.JSON(), &raw); err != nil {
		t.Fatalf("embedded JSON is not valid: %v", err)
	}
}

func TestMarkdown_IsNonEmpty(t *testing.T) {
	md := string(skills.Markdown())
	if len(md) < 500 {
		t.Errorf("markdown descriptor too short: %d bytes", len(md))
	}
	// Must declare the tool and mention at least the implemented subcommands.
	for _, want := range []string{"obconverge", "## Subcommands", "scan", "classify", "plan"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
}

func TestParse_HasRequiredFields(t *testing.T) {
	d, err := skills.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d.Tool != "obconverge" {
		t.Errorf("tool = %q", d.Tool)
	}
	if d.Version == "" {
		t.Errorf("version is empty")
	}
	if len(d.Subcommands) == 0 {
		t.Errorf("subcommands empty")
	}
	if len(d.Buckets) == 0 {
		t.Errorf("buckets empty")
	}
	if len(d.ExitCodes) == 0 {
		t.Errorf("exit codes empty")
	}
}

func TestParse_ImplementedBucketsPresent(t *testing.T) {
	d, err := skills.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := map[string]bool{
		"EXACT":             true,
		"CRLF-ONLY":         true,
		"FRONTMATTER-ONLY":  true,
		"FRONTMATTER-EQUAL": true,
		"DIVERGED":          true,
		"SECRETS":           true,
		"UNIQUE":            true,
	}
	got := map[string]bool{}
	for _, b := range d.Buckets {
		if b.Implemented {
			got[b.Name] = true
		}
	}
	for name := range want {
		if !got[name] {
			t.Errorf("descriptor missing implemented bucket %q", name)
		}
	}
}

func TestParse_ExitCodesCoverFullRange(t *testing.T) {
	d, err := skills.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Codes 0..5 per SPEC.md.
	seen := map[int]bool{}
	for _, c := range d.ExitCodes {
		seen[c.Code] = true
	}
	for i := 0; i <= 5; i++ {
		if !seen[i] {
			t.Errorf("exit code %d missing from descriptor", i)
		}
	}
}

func TestParse_SecretPatternNamesMatchSecretsPackage(t *testing.T) {
	// Drift guard: the descriptor's secret_patterns list must match the
	// built-in names in internal/secrets. Adding a pattern to patterns.json
	// but forgetting the descriptor (or vice versa) fails this test.
	d, err := skills.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	descNames := map[string]bool{}
	for _, p := range d.SecretPatterns {
		descNames[p.Name] = true
	}
	secretsNames := map[string]bool{}
	for _, n := range secrets.BuiltinNames() {
		secretsNames[n] = true
	}
	for n := range secretsNames {
		if !descNames[n] {
			t.Errorf("descriptor missing secret pattern %q (present in internal/secrets)", n)
		}
	}
	for n := range descNames {
		if !secretsNames[n] {
			t.Errorf("descriptor has secret pattern %q not in internal/secrets", n)
		}
	}
}
