package hashing

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOfBytes_KnownVector(t *testing.T) {
	// SHA-256("abc") is a published test vector.
	got := OfBytes([]byte("abc"))
	want := ContentHash("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
	if got != want {
		t.Fatalf("OfBytes(abc)\n got:  %s\n want: %s", got, want)
	}
}

func TestOfFile_MatchesOfBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.md")
	content := []byte("# A note\n\nsome content\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	fromFile, err := OfFile(path)
	if err != nil {
		t.Fatalf("OfFile: %v", err)
	}
	fromBytes := OfBytes(content)
	if fromFile != fromBytes {
		t.Fatalf("file hash %s != bytes hash %s", fromFile, fromBytes)
	}
}

func TestOfFile_MissingReturnsError(t *testing.T) {
	_, err := OfFile(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}
