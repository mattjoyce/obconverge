package scan_test

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/mattjoyce/obconverge/internal/artifact"
	"github.com/mattjoyce/obconverge/internal/scan"
	"github.com/mattjoyce/obconverge/internal/testvault"
)

func TestRun_IndexesRegularFiles(t *testing.T) {
	root := testvault.Build(t,
		testvault.File{Path: "Alpha.md", Content: "# alpha\n"},
		testvault.File{Path: "Nested/Beta.md", Content: "beta body\n"},
		testvault.File{Path: "Attachments/image.png", Content: "\x89PNG fake"},
	)
	out := filepath.Join(t.TempDir(), "index.jsonl")

	if err := scan.Run(scan.Options{VaultRoot: root, OutputPath: out}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	entries := readAllEntries(t, out)
	wantPaths := []string{"Alpha.md", "Attachments/image.png", "Nested/Beta.md"}
	gotPaths := pathsOf(entries)
	sort.Strings(gotPaths)
	if !equalSlices(gotPaths, wantPaths) {
		t.Fatalf("paths = %v, want %v", gotPaths, wantPaths)
	}

	// Basename is set.
	for _, e := range entries {
		wantBase := filepath.Base(e.Path)
		if e.Basename != wantBase {
			t.Errorf("entry %q: Basename = %q, want %q", e.Path, e.Basename, wantBase)
		}
		if e.Size <= 0 {
			t.Errorf("entry %q: Size = %d, want > 0", e.Path, e.Size)
		}
		if len(e.ByteHash) != 64 {
			t.Errorf("entry %q: ByteHash len = %d, want 64 hex chars", e.Path, len(e.ByteHash))
		}
		if len(e.ContentHash) != 64 {
			t.Errorf("entry %q: ContentHash len = %d", e.Path, len(e.ContentHash))
		}
	}
}

func TestRun_SkipsProtectedDirs(t *testing.T) {
	root := testvault.Build(t,
		testvault.File{Path: "Keep.md", Content: "keep\n"},
		testvault.File{Path: ".obsidian/workspace.json", Content: "{}"},
		testvault.File{Path: ".trash/old.md", Content: "old\n"},
		testvault.File{Path: ".git/HEAD", Content: "ref: refs/heads/main\n"},
		testvault.File{Path: ".obconverge/stale.jsonl", Content: `{"stale":true}` + "\n"},
	)
	out := filepath.Join(t.TempDir(), "index.jsonl")
	if err := scan.Run(scan.Options{VaultRoot: root, OutputPath: out}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	entries := readAllEntries(t, out)
	if len(entries) != 1 || entries[0].Path != "Keep.md" {
		t.Fatalf("expected only Keep.md, got %v", pathsOf(entries))
	}
}

func TestRun_CRLFAndLFShareContentHashNotByteHash(t *testing.T) {
	root := testvault.Build(t,
		testvault.File{Path: "lf.md", Content: "line one\nline two\n"},
		testvault.File{Path: "crlf.md", Content: "line one\r\nline two\r\n"},
	)
	out := filepath.Join(t.TempDir(), "index.jsonl")
	if err := scan.Run(scan.Options{VaultRoot: root, OutputPath: out}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	entries := readAllEntries(t, out)
	byPath := map[string]scan.Entry{}
	for _, e := range entries {
		byPath[e.Path] = e
	}
	lf, crlf := byPath["lf.md"], byPath["crlf.md"]
	if lf.ByteHash == crlf.ByteHash {
		t.Errorf("ByteHash should differ for CRLF vs LF files; got %s == %s", lf.ByteHash, crlf.ByteHash)
	}
	if lf.ContentHash != crlf.ContentHash {
		t.Errorf("ContentHash should match after CRLF normalization; got %s != %s", lf.ContentHash, crlf.ContentHash)
	}
}

func TestRun_DoesNotMutateVault(t *testing.T) {
	root := testvault.Build(t,
		testvault.File{Path: "A.md", Content: "a\n"},
		testvault.File{Path: "B.md", Content: "b\n"},
	)
	before := snapshotVault(t, root)

	out := filepath.Join(t.TempDir(), "index.jsonl")
	if err := scan.Run(scan.Options{VaultRoot: root, OutputPath: out}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	after := snapshotVault(t, root)
	if !equalStringMaps(before, after) {
		t.Fatalf("scan mutated vault: before=%v after=%v", before, after)
	}
}

// helpers

func readAllEntries(t *testing.T, path string) []scan.Entry {
	t.Helper()
	r, err := artifact.NewReader(path, scan.Schema)
	if err != nil {
		t.Fatalf("artifact.NewReader: %v", err)
	}
	defer r.Close()

	var entries []scan.Entry
	for {
		var e scan.Entry
		err := r.Next(&e)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		entries = append(entries, e)
	}
	return entries
}

func pathsOf(entries []scan.Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Path)
	}
	return out
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// snapshotVault returns path → sha256 of every regular file under root.
func snapshotVault(t *testing.T, root string) map[string]string {
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
		t.Fatalf("snapshotVault: %v", err)
	}
	return out
}

func equalStringMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
