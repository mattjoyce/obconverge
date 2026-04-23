// Package classify reads an index.jsonl artifact and produces classification.jsonl.
//
// Classify is a pure function of the index: same input → same output. It
// never touches the vault directly in this MVP (future buckets like
// APPEND-ONLY will need to read file bytes; the spec explicitly allows
// read-only access during classify).
package classify

import (
	"fmt"
	"io"
	"sort"

	"github.com/mattjoyce/obconverge/internal/artifact"
	"github.com/mattjoyce/obconverge/internal/links"
	"github.com/mattjoyce/obconverge/internal/scan"
)

// Schema is the header schema string for classification.jsonl artifacts.
// Bumped to v3 with the addition of referrer counts.
const Schema = "classification/3"

// Bucket is the classifier's verdict for a pair (or single) of files.
//
// Buckets not yet implemented: TAG-DELTA, APPEND-ONLY. They require finer
// frontmatter canonicalization / byte-level prefix comparison respectively
// and will land in follow-up commits.
type Bucket string

const (
	BucketExact            Bucket = "EXACT"
	BucketCRLFOnly         Bucket = "CRLF-ONLY"
	BucketFrontmatterOnly  Bucket = "FRONTMATTER-ONLY"
	BucketFrontmatterEqual Bucket = "FRONTMATTER-EQUAL"
	BucketDiverged         Bucket = "DIVERGED"
	BucketSecrets          Bucket = "SECRETS"
	BucketUnique           Bucket = "UNIQUE"
)

// Record is the union of classification record shapes written to
// classification.jsonl. Type is "pair" or "unique".
type Record struct {
	Type     string   `json:"type"`
	Bucket   Bucket   `json:"bucket"`
	Basename string   `json:"basename"`
	Paths    []string `json:"paths,omitempty"` // set for type=pair
	Path     string   `json:"path,omitempty"`  // set for type=unique
	// SecretPattern names the first matched credential pattern. Set only for
	// SECRETS records. Never contains the secret itself — just a pattern
	// name like "anthropic" or "aws-access-key".
	SecretPattern string `json:"secret_pattern,omitempty"`
	// ReferrerCount is the number of incoming wikilinks / embeds to this
	// file's basename. For unique records this is a scalar; for pair
	// records see ReferrerCounts (both files share a basename but may
	// have different link contexts — v1 reports the same count for
	// both paths since Obsidian resolves by basename).
	ReferrerCount int `json:"referrer_count,omitempty"`
}

// Options configures a classification run.
type Options struct {
	IndexPath          string
	ClassificationPath string
	// Graph, if non-nil, provides referrer counts that are stamped onto
	// each record. Nil graph means ReferrerCount stays zero — fine for
	// tests that don't care about link topology.
	Graph *links.Graph
}

// Run reads the index and writes classification records grouping entries by
// basename. Entries whose basename appears only once emit a UNIQUE record
// (or SECRETS if the file carries credentials); every other group emits one
// PAIR record per unordered pair.
func Run(opts Options) error {
	if opts.IndexPath == "" {
		return fmt.Errorf("classify: IndexPath is required")
	}
	if opts.ClassificationPath == "" {
		return fmt.Errorf("classify: ClassificationPath is required")
	}

	r, err := artifact.NewReader(opts.IndexPath, scan.Schema)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()

	byBasename := map[string][]scan.Entry{}
	for {
		var e scan.Entry
		err := r.Next(&e)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("classify: read index: %w", err)
		}
		byBasename[e.Basename] = append(byBasename[e.Basename], e)
	}

	w, err := artifact.NewWriter(opts.ClassificationPath, Schema)
	if err != nil {
		return err
	}
	defer func() { _ = w.Close() }()

	// Deterministic output order — plan reviewability requires it.
	names := make([]string, 0, len(byBasename))
	for n := range byBasename {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		entries := byBasename[name]
		sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

		refCount := 0
		if opts.Graph != nil {
			refCount = opts.Graph.Count(name)
		}

		if len(entries) == 1 {
			if err := writeUnique(w, name, entries[0], refCount); err != nil {
				return err
			}
			continue
		}
		for i := 0; i < len(entries); i++ {
			for j := i + 1; j < len(entries); j++ {
				if err := writePair(w, name, entries[i], entries[j], refCount); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func writeUnique(w *artifact.Writer, name string, e scan.Entry, refCount int) error {
	rec := Record{
		Type:          "unique",
		Basename:      name,
		Path:          e.Path,
		ReferrerCount: refCount,
	}
	if e.HasSecrets {
		rec.Bucket = BucketSecrets
		rec.SecretPattern = e.SecretPattern
	} else {
		rec.Bucket = BucketUnique
	}
	return w.Write(rec)
}

func writePair(w *artifact.Writer, name string, a, b scan.Entry, refCount int) error {
	rec := Record{
		Type:          "pair",
		Basename:      name,
		Paths:         []string{a.Path, b.Path},
		ReferrerCount: refCount,
	}
	rec.Bucket, rec.SecretPattern = bucketFor(a, b)
	return w.Write(rec)
}

// bucketFor is the pure core of the classifier.
//
// Order matters — SECRETS always wins, regardless of any other similarity.
// This matches the spec: "Route any file containing such a string into the
// SECRETS bucket, regardless of other similarity signals."
func bucketFor(a, b scan.Entry) (Bucket, string) {
	if a.HasSecrets || b.HasSecrets {
		return BucketSecrets, firstNonEmpty(a.SecretPattern, b.SecretPattern)
	}
	if a.ByteHash == b.ByteHash {
		return BucketExact, ""
	}
	if a.ContentHash == b.ContentHash {
		return BucketCRLFOnly, ""
	}
	// Body match, frontmatter differs → FRONTMATTER-ONLY.
	// BodyHash can be empty on non-md files that went through older schemas;
	// require both sides non-empty before using it.
	if a.BodyHash != "" && a.BodyHash == b.BodyHash {
		return BucketFrontmatterOnly, ""
	}
	// Frontmatter match, body differs → FRONTMATTER-EQUAL.
	if a.FrontmatterHash != "" && a.FrontmatterHash == b.FrontmatterHash {
		return BucketFrontmatterEqual, ""
	}
	return BucketDiverged, ""
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}
