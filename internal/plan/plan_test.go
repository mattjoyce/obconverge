package plan_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mattjoyce/obconverge/internal/classify"
	"github.com/mattjoyce/obconverge/internal/plan"
	"github.com/mattjoyce/obconverge/internal/scan"
	"github.com/mattjoyce/obconverge/internal/testvault"
)

// runPipeline builds a real faux vault, runs scan + classify + plan, and
// returns (planPath, planBytes). No mocks.
func runPipeline(t *testing.T, files []testvault.File, policyYAML string) (string, []byte) {
	t.Helper()
	root := testvault.Build(t, files...)
	work := t.TempDir()
	indexPath := filepath.Join(work, "index.jsonl")
	classPath := filepath.Join(work, "classification.jsonl")
	planPath := filepath.Join(work, "plan.md")
	var policyPath string
	if policyYAML != "" {
		policyPath = filepath.Join(work, "policy.yaml")
		if err := os.WriteFile(policyPath, []byte(policyYAML), 0o644); err != nil {
			t.Fatalf("write policy: %v", err)
		}
	}

	if err := scan.Run(scan.Options{VaultRoot: root, OutputPath: indexPath}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}
	if err := classify.Run(classify.Options{IndexPath: indexPath, ClassificationPath: classPath}); err != nil {
		t.Fatalf("classify.Run: %v", err)
	}
	if err := plan.Run(plan.Options{
		ClassificationPath: classPath,
		PolicyPath:         policyPath,
		OutputPath:         planPath,
		Now:                time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("plan.Run: %v", err)
	}

	data, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatalf("read plan.md: %v", err)
	}
	return planPath, data
}

func TestPlan_RendersAllBuckets(t *testing.T) {
	_, body := runPipeline(t, []testvault.File{
		{Path: "Notes/A.md", Content: "body\n"},
		{Path: "Prod/A.md", Content: "body\n"}, // EXACT
		{Path: "Notes/B.md", Content: "---\ntags: [x]\n---\n\nshared\n"},
		{Path: "Prod/B.md", Content: "---\ntags: [y]\n---\n\nshared\n"}, // FRONTMATTER-ONLY
		{Path: "Notes/C.md", Content: "---\ntags: [x]\n---\n\none\n"},
		{Path: "Prod/C.md", Content: "---\ntags: [x]\n---\n\ntwo\n"}, // FRONTMATTER-EQUAL
		{Path: "Notes/Keys.md", Content: "AKIAIOSFODNN7EXAMPLE\n"},   // SECRETS unique
		{Path: "Solo.md", Content: "alone\n"},                        // UNIQUE
	}, "")

	s := string(body)
	for _, want := range []string{
		"## SECRETS — `#quarantine`",
		"## EXACT — `#drop`",
		"## FRONTMATTER-ONLY — `#merge-frontmatter`",
		"## FRONTMATTER-EQUAL — `#review`",
		"## UNIQUE — `#keep`",
		"#drop `",
		"#merge-frontmatter `",
		"#review `",
		"#quarantine `",
		"#keep `",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("plan missing %q\n---\n%s", want, s)
		}
	}
}

func TestPlan_SecretsOrderedFirst(t *testing.T) {
	_, body := runPipeline(t, []testvault.File{
		{Path: "Notes/A.md", Content: "body\n"},
		{Path: "Prod/A.md", Content: "body\n"},
		{Path: "Notes/Keys.md", Content: "token: AKIAIOSFODNN7EXAMPLE\n"},
	}, "")
	s := string(body)
	secretsIdx := strings.Index(s, "## SECRETS")
	exactIdx := strings.Index(s, "## EXACT")
	if secretsIdx < 0 || exactIdx < 0 {
		t.Fatalf("both sections must be present\n%s", s)
	}
	if secretsIdx > exactIdx {
		t.Errorf("SECRETS section should appear before EXACT; got secretsIdx=%d exactIdx=%d", secretsIdx, exactIdx)
	}
}

func TestPlan_SecretsNeverLeakContent(t *testing.T) {
	secret := "AKIAIOSFODNN7EXAMPLE"
	_, body := runPipeline(t, []testvault.File{
		{Path: "Keys.md", Content: "api key: " + secret + "\n"},
	}, "")
	if strings.Contains(string(body), secret) {
		t.Errorf("plan.md leaked the secret:\n%s", body)
	}
	// And it should include the pattern name, not the secret.
	if !strings.Contains(string(body), "aws-access-key") {
		t.Errorf("plan.md missing pattern name; body=\n%s", body)
	}
}

func TestPlan_ActionIDsStableAcrossRuns(t *testing.T) {
	files := []testvault.File{
		{Path: "Notes/A.md", Content: "body\n"},
		{Path: "Prod/A.md", Content: "body\n"},
		{Path: "Notes/B.md", Content: "---\ntags: [x]\n---\n\ndifferent\n"},
		{Path: "Prod/B.md", Content: "---\ntags: [x]\n---\n\nanother\n"},
	}
	_, first := runPipeline(t, files, "")
	_, second := runPipeline(t, files, "")

	firstIDs := extractActionIDs(string(first))
	secondIDs := extractActionIDs(string(second))
	if len(firstIDs) == 0 {
		t.Fatalf("first run produced no action IDs\n%s", first)
	}
	if len(firstIDs) != len(secondIDs) {
		t.Fatalf("ID count differs: %d vs %d", len(firstIDs), len(secondIDs))
	}
	for i := range firstIDs {
		if firstIDs[i] != secondIDs[i] {
			t.Errorf("ID[%d] differs: %s vs %s", i, firstIDs[i], secondIDs[i])
		}
	}
}

func TestPlan_PreservesCheckStateOnRerun(t *testing.T) {
	// Run pipeline once, tick a checkbox, run plan again, assert the tick
	// survived.
	root := testvault.Build(t,
		testvault.File{Path: "Notes/A.md", Content: "body\n"},
		testvault.File{Path: "Prod/A.md", Content: "body\n"},
	)
	work := t.TempDir()
	indexPath := filepath.Join(work, "index.jsonl")
	classPath := filepath.Join(work, "classification.jsonl")
	planPath := filepath.Join(work, "plan.md")

	if err := scan.Run(scan.Options{VaultRoot: root, OutputPath: indexPath}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if err := classify.Run(classify.Options{IndexPath: indexPath, ClassificationPath: classPath}); err != nil {
		t.Fatalf("classify: %v", err)
	}
	firstOpts := plan.Options{
		ClassificationPath: classPath,
		OutputPath:         planPath,
		Now:                time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC),
	}
	if err := plan.Run(firstOpts); err != nil {
		t.Fatalf("plan (first): %v", err)
	}

	// Operator ticks the checkbox.
	body, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	tick := strings.Replace(string(body), "- [ ]", "- [x]", 1)
	if tick == string(body) {
		t.Fatalf("no checkbox to tick in plan:\n%s", body)
	}
	if err := os.WriteFile(planPath, []byte(tick), 0o644); err != nil {
		t.Fatalf("write ticked plan: %v", err)
	}

	// Re-run plan.
	if err := plan.Run(firstOpts); err != nil {
		t.Fatalf("plan (rerun): %v", err)
	}
	after, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatalf("read plan after rerun: %v", err)
	}
	if !strings.Contains(string(after), "- [x]") {
		t.Errorf("checkbox tick not preserved across rerun:\n%s", after)
	}
}

func TestPlan_EmptyClassificationProducesConvergedPlan(t *testing.T) {
	_, body := runPipeline(t, []testvault.File{}, "")
	s := string(body)
	if !strings.Contains(s, "vault is converged") {
		t.Errorf("expected converged message; got:\n%s", s)
	}
	if !strings.Contains(s, "**Total actions**: 0") {
		t.Errorf("expected zero-action summary:\n%s", s)
	}
}

func TestPlan_PolicyOverrideChangesAction(t *testing.T) {
	policyYAML := "rules:\n  EXACT: review\n"
	_, body := runPipeline(t, []testvault.File{
		{Path: "Notes/A.md", Content: "body\n"},
		{Path: "Prod/A.md", Content: "body\n"},
	}, policyYAML)
	s := string(body)
	if !strings.Contains(s, "## EXACT — `#review`") {
		t.Errorf("EXACT section should use #review per policy override; got:\n%s", s)
	}
	if strings.Contains(s, "#drop `") {
		t.Errorf("default #drop should not appear for EXACT when override says review:\n%s", s)
	}
}

func TestPlan_WritesValidObsidianFrontmatter(t *testing.T) {
	_, body := runPipeline(t, []testvault.File{
		{Path: "Solo.md", Content: "alone\n"},
	}, "")
	s := string(body)
	if !strings.HasPrefix(s, "---\n") {
		t.Errorf("plan must begin with --- frontmatter delimiter:\n%s", s)
	}
	if !strings.Contains(s, "\nschema: plan/1\n") {
		t.Errorf("plan frontmatter must declare schema plan/1:\n%s", s)
	}
	if !strings.Contains(s, "\ntool: obconverge\n") {
		t.Errorf("plan frontmatter must declare tool:\n%s", s)
	}
}

// extractActionIDs pulls out the 12-hex IDs in order of appearance.
func extractActionIDs(plan string) []string {
	re := regexp.MustCompile("`([a-f0-9]{12})`")
	var out []string
	for _, m := range re.FindAllStringSubmatch(plan, -1) {
		out = append(out, m[1])
	}
	return out
}
