package frontmatter

import (
	"errors"
	"strings"
	"testing"
)

func TestMergeUnion_IdenticalIsIdentity(t *testing.T) {
	fm := []byte("title: Hello\ntags:\n  - a\n  - b\n")
	got, err := MergeUnion(fm, fm)
	if err != nil {
		t.Fatalf("MergeUnion: %v", err)
	}
	if !strings.Contains(string(got), "title: Hello") {
		t.Errorf("missing title in merged output:\n%s", got)
	}
	// Tag list shouldn't duplicate.
	if strings.Count(string(got), "- a") != 1 {
		t.Errorf("tag 'a' should appear once, got:\n%s", got)
	}
}

func TestMergeUnion_AddsLoserOnlyKey(t *testing.T) {
	winner := []byte("title: Win\ntags: [alpha]\n")
	loser := []byte("title: Win\nsource: https://example.com\n")
	got, err := MergeUnion(winner, loser)
	if err != nil {
		t.Fatalf("MergeUnion: %v", err)
	}
	if !strings.Contains(string(got), "source: https://example.com") {
		t.Errorf("expected loser-only key 'source' in output:\n%s", got)
	}
	// Winner's key order preserved: title before source.
	titleIdx := strings.Index(string(got), "title:")
	sourceIdx := strings.Index(string(got), "source:")
	if titleIdx < 0 || sourceIdx < 0 || titleIdx > sourceIdx {
		t.Errorf("key order not preserved: title=%d source=%d\n%s", titleIdx, sourceIdx, got)
	}
}

func TestMergeUnion_TagSetUnion(t *testing.T) {
	winner := []byte("tags:\n  - a\n  - b\n")
	loser := []byte("tags:\n  - b\n  - c\n  - d\n")
	got, err := MergeUnion(winner, loser)
	if err != nil {
		t.Fatalf("MergeUnion: %v", err)
	}
	s := string(got)
	for _, tag := range []string{"a", "b", "c", "d"} {
		if strings.Count(s, "- "+tag) != 1 {
			t.Errorf("tag %q should appear exactly once in:\n%s", tag, s)
		}
	}
	// Order: winner first (a, b), then loser-new (c, d).
	aIdx := strings.Index(s, "- a")
	bIdx := strings.Index(s, "- b")
	cIdx := strings.Index(s, "- c")
	dIdx := strings.Index(s, "- d")
	if aIdx >= bIdx || bIdx >= cIdx || cIdx >= dIdx {
		t.Errorf("union order wrong (want a<b<c<d): got a=%d b=%d c=%d d=%d", aIdx, bIdx, cIdx, dIdx)
	}
}

func TestMergeUnion_AliasSetUnion(t *testing.T) {
	winner := []byte("aliases:\n  - Primary\n")
	loser := []byte("aliases:\n  - Secondary\n  - Primary\n")
	got, err := MergeUnion(winner, loser)
	if err != nil {
		t.Fatalf("MergeUnion: %v", err)
	}
	s := string(got)
	if strings.Count(s, "- Primary") != 1 {
		t.Errorf("Primary should appear once:\n%s", s)
	}
	if !strings.Contains(s, "- Secondary") {
		t.Errorf("Secondary should be added:\n%s", s)
	}
}

func TestMergeUnion_ScalarConflictRefuses(t *testing.T) {
	winner := []byte("title: Win\n")
	loser := []byte("title: Other\n")
	_, err := MergeUnion(winner, loser)
	if err == nil {
		t.Fatal("expected scalar conflict error")
	}
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error should be ConflictError, got %T: %v", err, err)
	}
	if conflict.Kind != ConflictScalar {
		t.Errorf("Kind = %s, want %s", conflict.Kind, ConflictScalar)
	}
	if conflict.Key != "title" {
		t.Errorf("Key = %q, want title", conflict.Key)
	}
}

func TestMergeUnion_TypeConflictRefuses(t *testing.T) {
	winner := []byte("source: https://example.com\n") // scalar
	loser := []byte("source:\n  - alpha\n  - beta\n") // list
	_, err := MergeUnion(winner, loser)
	if err == nil {
		t.Fatal("expected type conflict error")
	}
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error should be ConflictError, got %T: %v", err, err)
	}
	if conflict.Kind != ConflictType {
		t.Errorf("Kind = %s, want %s", conflict.Kind, ConflictType)
	}
}

func TestMergeUnion_SharedScalarEqualIsNoConflict(t *testing.T) {
	winner := []byte("status: published\ntags:\n  - a\n")
	loser := []byte("status: published\ntags:\n  - b\n")
	got, err := MergeUnion(winner, loser)
	if err != nil {
		t.Fatalf("equal scalar should not conflict: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, "status: published") {
		t.Errorf("status should survive:\n%s", s)
	}
	if !strings.Contains(s, "- a") || !strings.Contains(s, "- b") {
		t.Errorf("tag union should include both a and b:\n%s", s)
	}
}

func TestMergeUnion_EmptyLoserIsIdentity(t *testing.T) {
	// Block-style input should be preserved as block.
	winner := []byte("tags:\n  - a\n  - b\n")
	got, err := MergeUnion(winner, []byte(""))
	if err != nil {
		t.Fatalf("empty loser: %v", err)
	}
	if !strings.Contains(string(got), "- a") {
		t.Errorf("winner should pass through:\n%s", got)
	}
}

func TestMergeUnion_EmptyWinnerReturnsLoser(t *testing.T) {
	loser := []byte("tags:\n  - a\n")
	got, err := MergeUnion([]byte(""), loser)
	if err != nil {
		t.Fatalf("empty winner: %v", err)
	}
	if !strings.Contains(string(got), "- a") {
		t.Errorf("loser should pass through:\n%s", got)
	}
}

// TestMergeUnion_FlowStylePreserved documents that yaml.v3 preserves
// input style — a flow-style list in the winner stays flow-style on
// output. Operators who want consistent block-style should author their
// frontmatter that way from the start.
func TestMergeUnion_FlowStylePreserved(t *testing.T) {
	winner := []byte("tags: [a, b]\n")
	loser := []byte("tags: [c]\n")
	got, err := MergeUnion(winner, loser)
	if err != nil {
		t.Fatalf("MergeUnion: %v", err)
	}
	s := string(got)
	// Flow-style includes all three, comma-separated.
	for _, want := range []string{"a", "b", "c"} {
		if !strings.Contains(s, want) {
			t.Errorf("tag %q missing from flow output:\n%s", want, s)
		}
	}
}

func TestMergeUnion_NestedMapMerges(t *testing.T) {
	winner := []byte("meta:\n  author: matt\n  year: 2026\n")
	loser := []byte("meta:\n  reviewer: jane\n  year: 2026\n")
	got, err := MergeUnion(winner, loser)
	if err != nil {
		t.Fatalf("MergeUnion: %v", err)
	}
	s := string(got)
	for _, want := range []string{"author: matt", "reviewer: jane", "year: 2026"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

func TestMergeUnion_NestedMapConflictPropagates(t *testing.T) {
	winner := []byte("meta:\n  year: 2025\n")
	loser := []byte("meta:\n  year: 2026\n")
	_, err := MergeUnion(winner, loser)
	if err == nil {
		t.Fatal("nested scalar conflict should propagate")
	}
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error should be ConflictError, got %T: %v", err, err)
	}
	if conflict.Key != "meta.year" {
		t.Errorf("nested key path wrong: %q, want meta.year", conflict.Key)
	}
}
