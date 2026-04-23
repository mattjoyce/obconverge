package links_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mattjoyce/obconverge/internal/links"
	"github.com/mattjoyce/obconverge/internal/testvault"
)

func TestBuild_SimpleWikilink(t *testing.T) {
	root := testvault.Build(t,
		testvault.File{Path: "Alpha.md", Content: "content about [[Beta]].\n"},
		testvault.File{Path: "Beta.md", Content: "standalone\n"},
	)
	g := mustBuild(t, root)

	refs := g.Referrers("Beta")
	if len(refs) != 1 {
		t.Fatalf("Beta referrers = %d, want 1: %+v", len(refs), refs)
	}
	r := refs[0]
	if r.From != "Alpha.md" {
		t.Errorf("From = %q, want Alpha.md", r.From)
	}
	if r.Kind != links.KindWikilink {
		t.Errorf("Kind = %q, want wikilink", r.Kind)
	}
	if r.Raw != "[[Beta]]" {
		t.Errorf("Raw = %q, want [[Beta]]", r.Raw)
	}
}

func TestBuild_Embed(t *testing.T) {
	root := testvault.Build(t,
		testvault.File{Path: "Host.md", Content: "see image: ![[Pic]]\n"},
		testvault.File{Path: "Pic.md", Content: "just the pic file\n"},
	)
	g := mustBuild(t, root)
	refs := g.Referrers("Pic")
	if len(refs) != 1 || refs[0].Kind != links.KindEmbed {
		t.Fatalf("expected one embed, got %+v", refs)
	}
}

func TestBuild_HeadingRefVsBlockRef(t *testing.T) {
	root := testvault.Build(t,
		testvault.File{Path: "A.md", Content: "heading: [[B#Methods]], block: [[B#^id123]]\n"},
		testvault.File{Path: "B.md", Content: "body\n"},
	)
	g := mustBuild(t, root)
	refs := g.Referrers("B")
	if len(refs) != 2 {
		t.Fatalf("len(refs) = %d, want 2: %+v", len(refs), refs)
	}
	kinds := []links.Kind{refs[0].Kind, refs[1].Kind}
	if !contains(kinds, links.KindHeadingRef) || !contains(kinds, links.KindBlockRef) {
		t.Errorf("expected heading+blockref kinds, got %+v", kinds)
	}
}

func TestBuild_DisplayTextStripped(t *testing.T) {
	root := testvault.Build(t,
		testvault.File{Path: "A.md", Content: "pointer: [[B|Display Label]]\n"},
		testvault.File{Path: "B.md", Content: "body\n"},
	)
	g := mustBuild(t, root)
	refs := g.Referrers("B")
	if len(refs) != 1 {
		t.Fatalf("len(refs) = %d, want 1", len(refs))
	}
	if refs[0].Kind != links.KindWikilink {
		t.Errorf("display-text link should still be wikilink, got %q", refs[0].Kind)
	}
}

func TestBuild_AliasResolution(t *testing.T) {
	root := testvault.Build(t,
		testvault.File{Path: "Glossary.md", Content: "---\naliases:\n  - Terms\n  - Dictionary\n---\n\nglossary body\n"},
		testvault.File{Path: "A.md", Content: "see [[Terms]] and [[Dictionary]]\n"},
	)
	g := mustBuild(t, root)
	refs := g.Referrers("Glossary")
	if len(refs) != 2 {
		t.Fatalf("alias resolution failed; referrers = %+v", refs)
	}
}

func TestBuild_UnresolvedTargetsIgnored(t *testing.T) {
	root := testvault.Build(t,
		testvault.File{Path: "A.md", Content: "dangling: [[DoesNotExist]]\n"},
	)
	g := mustBuild(t, root)
	if got := g.Referrers("DoesNotExist"); len(got) != 0 {
		t.Errorf("unresolved target should have no referrers, got %+v", got)
	}
}

func TestBuild_SkipsProtectedPaths(t *testing.T) {
	root := testvault.Build(t,
		testvault.File{Path: "A.md", Content: "real: [[B]]\n"},
		testvault.File{Path: "B.md", Content: "body\n"},
		testvault.File{Path: ".trash/Ghost.md", Content: "spurious: [[B]]\n"},
		testvault.File{Path: ".obsidian/workspace.json", Content: "{}"},
		testvault.File{Path: ".obconverge/stale.md", Content: "stale: [[B]]\n"},
	)
	g := mustBuild(t, root)
	refs := g.Referrers("B")
	if len(refs) != 1 {
		t.Fatalf("expected 1 referrer (from A), got %d: %+v", len(refs), refs)
	}
	if refs[0].From != "A.md" {
		t.Errorf("From = %q, want A.md", refs[0].From)
	}
}

func TestBuild_FrontmatterLinksIgnored(t *testing.T) {
	// Links inside YAML frontmatter (e.g. in a string value) should not be
	// counted — the body is what represents note content to obsidian.
	root := testvault.Build(t,
		testvault.File{Path: "A.md", Content: "---\nnote: \"see [[B]]\"\n---\n\nbody without link\n"},
		testvault.File{Path: "B.md", Content: "body\n"},
	)
	g := mustBuild(t, root)
	if got := g.Referrers("B"); len(got) != 0 {
		t.Errorf("frontmatter link should not count; got %+v", got)
	}
}

func TestBuild_MultipleReferrersToSameTarget(t *testing.T) {
	root := testvault.Build(t,
		testvault.File{Path: "A.md", Content: "[[Target]]\n"},
		testvault.File{Path: "B.md", Content: "[[Target]] and [[Target]]\n"},
		testvault.File{Path: "Target.md", Content: "hub\n"},
	)
	g := mustBuild(t, root)
	if got := g.Count("Target"); got != 3 {
		t.Errorf("Count = %d, want 3 (A once, B twice)", got)
	}
}

func TestBuild_CountZeroForOrphan(t *testing.T) {
	root := testvault.Build(t,
		testvault.File{Path: "Lonely.md", Content: "no incoming links\n"},
	)
	g := mustBuild(t, root)
	if got := g.Count("Lonely"); got != 0 {
		t.Errorf("orphan Count = %d, want 0", got)
	}
}

func TestBuild_NameLookupHandlesExtension(t *testing.T) {
	// Referrers("Foo.md") should resolve same as Referrers("Foo").
	root := testvault.Build(t,
		testvault.File{Path: "A.md", Content: "[[Foo]]\n"},
		testvault.File{Path: "Foo.md", Content: "body\n"},
	)
	g := mustBuild(t, root)
	if g.Count("Foo.md") != g.Count("Foo") {
		t.Errorf("lookup with .md extension mismatch: %d vs %d",
			g.Count("Foo.md"), g.Count("Foo"))
	}
	if g.Count("Foo.md") != 1 {
		t.Errorf("Count = %d, want 1", g.Count("Foo.md"))
	}
}

func TestBuild_NoMutation(t *testing.T) {
	root := testvault.Build(t,
		testvault.File{Path: "A.md", Content: "[[B]]\n"},
		testvault.File{Path: "B.md", Content: "body\n"},
	)
	before := snapshot(t, root)
	_ = mustBuild(t, root)
	after := snapshot(t, root)
	for k, v := range before {
		if after[k] != v {
			t.Errorf("file %s mutated: %q -> %q", k, v, after[k])
		}
	}
}

// helpers

func mustBuild(t *testing.T, vault string) *links.Graph {
	t.Helper()
	g, err := links.Build(links.Options{VaultRoot: vault})
	if err != nil {
		t.Fatalf("links.Build: %v", err)
	}
	return g
}

func contains[T comparable](s []T, v T) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// snapshot reads every regular file under root into a path -> content map
// so tests can assert the filesystem wasn't mutated.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return out
}
