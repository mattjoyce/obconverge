package policy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattjoyce/obconverge/internal/classify"
	"github.com/mattjoyce/obconverge/internal/policy"
)

func TestDefault_ConservativeMapping(t *testing.T) {
	p := policy.Default()
	cases := map[classify.Bucket]policy.Action{
		classify.BucketExact:            policy.ActionDrop,
		classify.BucketCRLFOnly:         policy.ActionDrop,
		classify.BucketFrontmatterOnly:  policy.ActionMergeFrontmatter,
		classify.BucketFrontmatterEqual: policy.ActionReview,
		classify.BucketDiverged:         policy.ActionReview,
		classify.BucketSecrets:          policy.ActionQuarantine,
		classify.BucketUnique:           policy.ActionKeep,
	}
	for b, want := range cases {
		if got := p.ActionFor(b); got != want {
			t.Errorf("Default.ActionFor(%s) = %s, want %s", b, got, want)
		}
	}
}

func TestLoad_MissingFileReturnsDefault(t *testing.T) {
	p, err := policy.Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.ActionFor(classify.BucketExact) != policy.ActionDrop {
		t.Errorf("expected default mapping for EXACT")
	}
}

func TestLoad_OverridesDefault(t *testing.T) {
	// Operator wants EXACT to require review, not auto-drop.
	yaml := `rules:
  EXACT: review
  FRONTMATTER-ONLY: drop
`
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	p, err := policy.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := p.ActionFor(classify.BucketExact); got != policy.ActionReview {
		t.Errorf("EXACT = %s, want review (override)", got)
	}
	if got := p.ActionFor(classify.BucketFrontmatterOnly); got != policy.ActionDrop {
		t.Errorf("FRONTMATTER-ONLY = %s, want drop (override)", got)
	}
	// Unmentioned bucket keeps its default.
	if got := p.ActionFor(classify.BucketSecrets); got != policy.ActionQuarantine {
		t.Errorf("SECRETS = %s, want quarantine (default)", got)
	}
}

func TestLoad_UnknownBucketIsError(t *testing.T) {
	yaml := "rules:\n  NOT-A-BUCKET: drop\n"
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	_, err := policy.Load(path)
	if err == nil {
		t.Fatal("expected error for unknown bucket")
	}
	if !strings.Contains(err.Error(), "NOT-A-BUCKET") {
		t.Errorf("error should mention the bad bucket: %v", err)
	}
}

func TestLoad_UnknownActionIsError(t *testing.T) {
	yaml := "rules:\n  EXACT: nuke\n"
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	_, err := policy.Load(path)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
	if !strings.Contains(err.Error(), "nuke") {
		t.Errorf("error should mention the bad action: %v", err)
	}
}

func TestLoad_AcceptsForwardLookingBuckets(t *testing.T) {
	// TAG-DELTA, APPEND-ONLY, WHITESPACE-ONLY aren't emitted yet but policy
	// files should accept them so operators can write forward-looking configs.
	yaml := `rules:
  TAG-DELTA: merge-frontmatter
  APPEND-ONLY: drop
  WHITESPACE-ONLY: drop
`
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	if _, err := policy.Load(path); err != nil {
		t.Errorf("forward-looking policy should load, got: %v", err)
	}
}

func TestActionFor_UnknownBucketFallsBackToReview(t *testing.T) {
	p := policy.Policy{Rules: map[classify.Bucket]policy.Action{}}
	// A bucket not in defaults either.
	got := p.ActionFor(classify.Bucket("SPECULATIVE"))
	if got != policy.ActionReview {
		t.Errorf("ActionFor unknown bucket = %s, want review (safest)", got)
	}
}
