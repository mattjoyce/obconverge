// Package testvault builds real, on-disk Obsidian-shaped vaults for tests.
//
// The spec is clear: do not mock the filesystem. Tests run against real files
// in t.TempDir(). testvault is the fixture builder that keeps that ergonomic.
package testvault

import (
	"os"
	"path/filepath"
	"testing"
)

// File is one note (or attachment) to materialize under the vault root.
type File struct {
	// Path is relative to the vault root, using forward slashes.
	Path string
	// Content is the bytes written to Path. Line endings are preserved as-is
	// so tests can exercise CRLF-sensitive logic explicitly.
	Content string
}

// Build materializes the given files under a fresh t.TempDir() and returns
// the vault root path. Cleanup is handled by t.TempDir().
func Build(t *testing.T, files ...File) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range files {
		full := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("testvault: mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(f.Content), 0o644); err != nil {
			t.Fatalf("testvault: write %s: %v", f.Path, err)
		}
	}
	return root
}
