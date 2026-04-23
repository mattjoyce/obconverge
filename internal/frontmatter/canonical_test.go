package frontmatter

import (
	"strings"
	"testing"
)

func TestStripTags_EmptyInputPassesThrough(t *testing.T) {
	got, err := StripTags(nil)
	if err != nil || got != nil {
		t.Errorf("nil input: got %q, err=%v", got, err)
	}
	got, err = StripTags([]byte(""))
	if err != nil || len(got) != 0 {
		t.Errorf("empty input: got %q, err=%v", got, err)
	}
}

func TestStripTags_RemovesTopLevelTagsKey(t *testing.T) {
	fm := []byte("title: Hello\ntags:\n  - alpha\n  - beta\nsource: https://example.com\n")
	got, err := StripTags(fm)
	if err != nil {
		t.Fatalf("StripTags: %v", err)
	}
	s := string(got)
	if strings.Contains(s, "tags") {
		t.Errorf("output still contains tags key:\n%s", s)
	}
	if !strings.Contains(s, "title: Hello") {
		t.Errorf("title should survive:\n%s", s)
	}
	if !strings.Contains(s, "source:") {
		t.Errorf("source should survive:\n%s", s)
	}
}

func TestStripTags_TagsOnlyReturnsEmpty(t *testing.T) {
	fm := []byte("tags:\n  - a\n  - b\n")
	got, err := StripTags(fm)
	if err != nil {
		t.Fatalf("StripTags: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("tags-only input should strip to empty, got: %q", got)
	}
}

func TestStripTags_NoTagsKeyIsIdentityShape(t *testing.T) {
	fm := []byte("title: Hello\nsource: https://example.com\n")
	got, err := StripTags(fm)
	if err != nil {
		t.Fatalf("StripTags: %v", err)
	}
	// Re-emission may reformat whitespace; check content substantively.
	s := string(got)
	if !strings.Contains(s, "title: Hello") || !strings.Contains(s, "source:") {
		t.Errorf("shape lost:\n%s", s)
	}
}

// TestStripTags_DoesNotTouchNestedTagsKeys proves the stripping is
// top-level only. A nested "tags" key inside a sub-map is preserved.
func TestStripTags_DoesNotTouchNestedTagsKeys(t *testing.T) {
	fm := []byte("title: Hello\nmeta:\n  tags:\n    - inner\n  year: 2026\n")
	got, err := StripTags(fm)
	if err != nil {
		t.Fatalf("StripTags: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, "inner") {
		t.Errorf("nested tags value should survive; got:\n%s", s)
	}
}

// TestStripTags_IdempotentHashesEqualWhenOnlyTagsDiffer is the
// essential property: the hash of StripTags(fmA) must equal the hash of
// StripTags(fmB) when fmA and fmB differ only in their tags list. This
// is what classify relies on to detect TAG-DELTA.
func TestStripTags_IdempotentHashesEqualWhenOnlyTagsDiffer(t *testing.T) {
	fmA := []byte("title: Hello\ntags:\n  - alpha\nsource: https://example.com\n")
	fmB := []byte("title: Hello\ntags:\n  - beta\n  - gamma\nsource: https://example.com\n")

	strippedA, err := StripTags(fmA)
	if err != nil {
		t.Fatalf("StripTags A: %v", err)
	}
	strippedB, err := StripTags(fmB)
	if err != nil {
		t.Fatalf("StripTags B: %v", err)
	}
	if string(strippedA) != string(strippedB) {
		t.Errorf("stripped results should be equal when only tags differ:\nA=%q\nB=%q",
			strippedA, strippedB)
	}
}

func TestStripTags_DifferInNonTagsKey(t *testing.T) {
	fmA := []byte("title: Hello\ntags:\n  - alpha\n")
	fmB := []byte("title: Goodbye\ntags:\n  - alpha\n")
	strippedA, _ := StripTags(fmA)
	strippedB, _ := StripTags(fmB)
	if string(strippedA) == string(strippedB) {
		t.Errorf("non-tags difference must survive stripping:\nA=%q\nB=%q",
			strippedA, strippedB)
	}
}
