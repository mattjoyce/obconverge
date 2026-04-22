// Package classify reads an index.jsonl artifact and produces classification.jsonl.
//
// Classify is a pure function of the index: same input → same output. It
// never touches the vault. Policy (which bucket triggers which action) lives
// elsewhere, in internal/policy; classify's sole job is to name the pair.
package classify

import (
	"fmt"
	"io"
	"sort"

	"github.com/mattjoyce/obconverge/internal/artifact"
	"github.com/mattjoyce/obconverge/internal/scan"
)

// Schema is the header schema string for classification.jsonl artifacts.
const Schema = "classification/1"

// Bucket is the classifier's verdict for a pair (or single) of files.
//
// The v1 MVP handles EXACT, CRLF-ONLY, DIVERGED, and UNIQUE. Additional
// buckets (FRONTMATTER-ONLY, TAG-DELTA, APPEND-ONLY, SECRETS) require
// parsing markdown and frontmatter and will land in later passes.
type Bucket string

const (
	BucketExact    Bucket = "EXACT"
	BucketCRLFOnly Bucket = "CRLF-ONLY"
	BucketDiverged Bucket = "DIVERGED"
	BucketUnique   Bucket = "UNIQUE"
)

// Record is the union of classification record shapes written to
// classification.jsonl. Type is "pair" or "unique"; callers should decode
// that field first and then re-decode into the appropriate shape.
type Record struct {
	Type     string   `json:"type"`
	Bucket   Bucket   `json:"bucket"`
	Basename string   `json:"basename"`
	Paths    []string `json:"paths,omitempty"` // set for type=pair
	Path     string   `json:"path,omitempty"`  // set for type=unique
}

// Options configures a classification run.
type Options struct {
	IndexPath          string
	ClassificationPath string
}

// Run reads the index and writes classification records grouping entries by
// basename. Entries whose basename appears only once emit a UNIQUE record;
// every other group emits one PAIR record per unordered pair.
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
	defer r.Close()

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
	defer w.Close()

	// Deterministic output order — plan reviewability requires it.
	names := make([]string, 0, len(byBasename))
	for n := range byBasename {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		entries := byBasename[name]
		sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

		if len(entries) == 1 {
			rec := Record{
				Type:     "unique",
				Bucket:   BucketUnique,
				Basename: name,
				Path:     entries[0].Path,
			}
			if err := w.Write(rec); err != nil {
				return err
			}
			continue
		}
		for i := 0; i < len(entries); i++ {
			for j := i + 1; j < len(entries); j++ {
				a, b := entries[i], entries[j]
				rec := Record{
					Type:     "pair",
					Bucket:   bucketFor(a, b),
					Basename: name,
					Paths:    []string{a.Path, b.Path},
				}
				if err := w.Write(rec); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// bucketFor is the pure core of the classifier: given two index entries,
// return the bucket that names their relationship.
//
// The v1 logic is intentionally coarse:
//
//	ByteHash equal       → EXACT
//	ContentHash equal    → CRLF-ONLY (byte-different, CRLF-normalized equal)
//	else                 → DIVERGED
//
// Finer-grained buckets (FRONTMATTER-ONLY, TAG-DELTA, APPEND-ONLY) need
// parsed markdown and are explicitly deferred.
func bucketFor(a, b scan.Entry) Bucket {
	if a.ByteHash == b.ByteHash {
		return BucketExact
	}
	if a.ContentHash == b.ContentHash {
		return BucketCRLFOnly
	}
	return BucketDiverged
}
