// Package scan walks a vault and emits index.jsonl.
//
// Scan is a pure read phase — it never mutates the vault. Its only output is
// the artifact written to OutputPath, which callers typically locate inside
// <vault>/.obconverge/.
package scan

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattjoyce/obconverge/internal/artifact"
	"github.com/mattjoyce/obconverge/internal/frontmatter"
	"github.com/mattjoyce/obconverge/internal/hashing"
	"github.com/mattjoyce/obconverge/internal/secrets"
)

// Schema is the header schema string for index.jsonl artifacts. Bumped to v4
// with the addition of WhitespaceHash (used by classify to detect
// WHITESPACE-ONLY — pairs that differ only in trailing whitespace or
// blank-line padding).
const Schema = "index/4"

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
	// WhitespaceHash is the SHA-256 of the file's bytes after CRLF → LF,
	// trailing-space/tab trimmed from each line, and trailing blank
	// lines removed. Two files that differ only in whitespace noise
	// share a WhitespaceHash. Superset of ContentHash (anything that
	// matches on ContentHash also matches on WhitespaceHash).
	WhitespaceHash string `json:"whitespace_hash"`
	// FrontmatterHash is the SHA-256 of the CRLF-normalized frontmatter YAML,
	// or empty if the file has no frontmatter (or is not markdown).
	FrontmatterHash string `json:"frontmatter_hash,omitempty"`
	// FrontmatterNoTagsHash is the SHA-256 of the frontmatter re-emitted
	// without the top-level tags key. When two files' FrontmatterHash
	// differ but their FrontmatterNoTagsHash matches, classify assigns
	// them to TAG-DELTA instead of the coarser FRONTMATTER-ONLY bucket.
	FrontmatterNoTagsHash string `json:"frontmatter_no_tags_hash,omitempty"`
	// BodyHash is the SHA-256 of the CRLF-normalized post-frontmatter body.
	// For files without frontmatter or non-markdown files, BodyHash == ContentHash.
	BodyHash string `json:"body_hash"`
	// Tags are the parsed frontmatter tags, if any.
	Tags []string `json:"tags,omitempty"`
	// Aliases are the parsed frontmatter aliases, if any.
	Aliases []string `json:"aliases,omitempty"`
	// HasSecrets is true if the file matches any known credential pattern.
	HasSecrets bool `json:"has_secrets,omitempty"`
	// SecretPattern names the first matched pattern (e.g. "anthropic"), or
	// empty. Never contains the secret itself.
	SecretPattern string `json:"secret_pattern,omitempty"`
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
	// Detector scans note bodies for credential-shaped strings. Required —
	// secret detection is part of the vault audit, not optional. Callers
	// that don't care about user extensions can pass secrets.NewBuiltins().
	Detector *secrets.Detector
}

// Run walks the vault and writes index.jsonl.
func Run(opts Options) error {
	if opts.VaultRoot == "" {
		return fmt.Errorf("scan: VaultRoot is required")
	}
	if opts.OutputPath == "" {
		return fmt.Errorf("scan: OutputPath is required")
	}
	if opts.Detector == nil {
		return fmt.Errorf("scan: Detector is required")
	}

	prefixes := append([]string(nil), DefaultProtectedPrefixes...)
	prefixes = append(prefixes, opts.ProtectedPrefixes...)

	w, err := artifact.NewWriter(opts.OutputPath, Schema)
	if err != nil {
		return err
	}
	defer func() { _ = w.Close() }()

	var fileCount int
	err = filepath.WalkDir(opts.VaultRoot, func(path string, d fs.DirEntry, walkErr error) error {
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

		entry, err := analyze(path, relSlash, info, opts.Detector)
		if err != nil {
			return err
		}
		fileCount++
		return w.Write(entry)
	})
	if err != nil {
		return err
	}
	slog.Debug("scan walked", "vault", opts.VaultRoot, "files", fileCount)
	return nil
}

func isProtected(relSlash string, prefixes []string) bool {
	for _, p := range prefixes {
		if relSlash == p || strings.HasPrefix(relSlash, p+"/") {
			return true
		}
	}
	return false
}

// analyze reads a single file and computes all its signals.
func analyze(path, relSlash string, info os.FileInfo, detector *secrets.Detector) (Entry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, err
	}
	entry := Entry{
		Path:           relSlash,
		Basename:       filepath.Base(relSlash),
		Size:           info.Size(),
		ModTime:        info.ModTime().UTC(),
		ByteHash:       string(hashing.OfBytes(b)),
		ContentHash:    string(hashing.OfBytes(normalizeLineEndings(b))),
		WhitespaceHash: string(hashing.OfBytes(normalizeWhitespace(b))),
	}

	// Frontmatter + secret analysis only applies to markdown.
	if isMarkdown(relSlash) {
		fm, body := frontmatter.Split(b)
		entry.BodyHash = string(hashing.OfBytes(normalizeLineEndings(body)))
		if fm != nil {
			entry.FrontmatterHash = string(hashing.OfBytes(normalizeLineEndings(fm)))
			fields, fmErr := frontmatter.ExtractFields(fm)
			if fmErr != nil {
				slog.Warn("frontmatter parse failed", "path", relSlash, "err", fmErr)
			} else {
				entry.Tags = fields.Tags
				entry.Aliases = fields.Aliases
			}
			// Hash the frontmatter re-emitted without tags, so classify
			// can pick TAG-DELTA out of the FRONTMATTER-ONLY haystack.
			if stripped, serr := frontmatter.StripTags(fm); serr == nil {
				entry.FrontmatterNoTagsHash = string(hashing.OfBytes(normalizeLineEndings(stripped)))
			}
		}
		if matched, name := detector.Detect(b); matched {
			entry.HasSecrets = true
			entry.SecretPattern = name
		}
		return entry, nil
	}

	// Non-markdown: BodyHash is the CRLF-normalized full content.
	entry.BodyHash = entry.ContentHash
	return entry, nil
}

func isMarkdown(relSlash string) bool {
	return strings.EqualFold(filepath.Ext(relSlash), ".md")
}

// normalizeWhitespace returns a canonical form of b useful for the
// WHITESPACE-ONLY bucket: CRLF -> LF, trailing space/tab trimmed from
// every line, and trailing blank lines collapsed. Leading whitespace
// (indentation) is preserved because it's semantically load-bearing in
// Markdown (code blocks, list nesting).
func normalizeWhitespace(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	lf := normalizeLineEndings(b)
	lines := bytesSplitLines(lf)
	for i, line := range lines {
		lines[i] = trimRightWS(line)
	}
	for len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	return bytesJoinLines(lines)
}

// bytesSplitLines splits on '\n' without a final empty element if the
// input ended with a newline.
func bytesSplitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	} else if start == len(b) {
		// Input ended with newline. Represent that as a trailing empty
		// line so we can distinguish "foo\n" from "foo".
		out = append(out, nil)
	}
	return out
}

func bytesJoinLines(lines [][]byte) []byte {
	if len(lines) == 0 {
		return nil
	}
	total := 0
	for _, l := range lines {
		total += len(l) + 1
	}
	out := make([]byte, 0, total)
	for i, l := range lines {
		out = append(out, l...)
		if i < len(lines)-1 {
			out = append(out, '\n')
		}
	}
	return out
}

func trimRightWS(b []byte) []byte {
	end := len(b)
	for end > 0 && (b[end-1] == ' ' || b[end-1] == '\t') {
		end--
	}
	return b[:end]
}

func normalizeLineEndings(b []byte) []byte {
	// strings.ReplaceAll would allocate a string first; we can do the
	// byte-level replace directly to avoid one round trip.
	if len(b) == 0 {
		return b
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if i+1 < len(b) && b[i] == '\r' && b[i+1] == '\n' {
			continue
		}
		out = append(out, b[i])
	}
	return out
}
