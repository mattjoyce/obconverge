// Package scan walks a vault and emits index.jsonl.
//
// Scan is a pure read phase — it never mutates the vault. Its only output is
// the artifact written to OutputPath, which callers typically locate inside
// <vault>/.obconverge/.
package scan

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattjoyce/obconverge/internal/artifact"
	"github.com/mattjoyce/obconverge/internal/hashing"
)

// Schema is the header schema string for index.jsonl artifacts.
const Schema = "index/1"

// Entry describes one regular file discovered during a scan. It is the only
// record type written into index.jsonl (aside from the header).
type Entry struct {
	// Path is the vault-relative path using forward slashes.
	Path string `json:"path"`
	// Basename is filepath.Base(Path).
	Basename string `json:"basename"`
	// Size is the file size in bytes.
	Size int64 `json:"size"`
	// ModTime is the file's modification time in UTC.
	ModTime time.Time `json:"mod_time"`
	// ByteHash is the SHA-256 of the file's raw bytes.
	ByteHash string `json:"byte_hash"`
	// ContentHash is the SHA-256 of the file's bytes with CRLF collapsed to LF.
	// Two files that differ only in line endings share a ContentHash.
	ContentHash string `json:"content_hash"`
}

// DefaultProtectedPrefixes are the vault-relative path prefixes that scan
// never descends into. They mirror the Obsidian-semantics section of SPEC.md.
var DefaultProtectedPrefixes = []string{
	".obsidian",
	".trash",
	".git",
	".stfolder",
	".sync",
	".obconverge",
}

// Options configures a scan.
type Options struct {
	// VaultRoot is the absolute path to the vault to walk.
	VaultRoot string
	// OutputPath is the destination for index.jsonl.
	OutputPath string
	// ProtectedPrefixes are additional vault-relative prefixes to skip, on top
	// of DefaultProtectedPrefixes.
	ProtectedPrefixes []string
}

// Run walks the vault and writes index.jsonl.
func Run(opts Options) error {
	if opts.VaultRoot == "" {
		return fmt.Errorf("scan: VaultRoot is required")
	}
	if opts.OutputPath == "" {
		return fmt.Errorf("scan: OutputPath is required")
	}

	prefixes := append([]string(nil), DefaultProtectedPrefixes...)
	prefixes = append(prefixes, opts.ProtectedPrefixes...)

	w, err := artifact.NewWriter(opts.OutputPath, Schema)
	if err != nil {
		return err
	}
	defer w.Close()

	return filepath.WalkDir(opts.VaultRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(opts.VaultRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		relSlash := filepath.ToSlash(rel)

		if isProtected(relSlash, prefixes) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		byteHash, contentHash, err := hashPair(path)
		if err != nil {
			return err
		}
		return w.Write(Entry{
			Path:        relSlash,
			Basename:    filepath.Base(rel),
			Size:        info.Size(),
			ModTime:     info.ModTime().UTC(),
			ByteHash:    string(byteHash),
			ContentHash: contentHash,
		})
	})
}

func isProtected(relSlash string, prefixes []string) bool {
	for _, p := range prefixes {
		if relSlash == p || strings.HasPrefix(relSlash, p+"/") {
			return true
		}
	}
	return false
}

// hashPair returns (byteHash, contentHash). contentHash normalizes CRLF to LF
// before hashing, so files that differ only in line endings collide.
func hashPair(path string) (hashing.ContentHash, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	byteHash := hashing.OfBytes(b)
	normalized := strings.ReplaceAll(string(b), "\r\n", "\n")
	contentHash := hashing.OfBytes([]byte(normalized))
	return byteHash, string(contentHash), nil
}
