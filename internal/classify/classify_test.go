package classify_test

import (
	"io"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mattjoyce/obconverge/internal/artifact"
	"github.com/mattjoyce/obconverge/internal/classify"
	"github.com/mattjoyce/obconverge/internal/scan"
	"github.com/mattjoyce/obconverge/internal/testvault"
)

func TestClassify_ExactDuplicateAcrossFolders(t *testing.T) {
	root := testvault.Build(t,
		testvault.File{Path: "Notes/Alpha.md", Content: "same body\n"},
		testvault.File{Path: "Prod/Alpha.md", Content: "same body\n"},
	)
	records := scanAndClassify(t, root)

	pair := findPair(t, records, "Alpha.md")
	if pair.Bucket != classify.BucketExact {
		t.Errorf("bucket = %s, want EXACT", pair.Bucket)
	}
}

func TestClassify_CRLFOnlyDifference(t *testing.T) {
	root := testvault.Build(t,
		testvault.File{Path: "Notes/Beta.md", Content: "one\ntwo\n"},
		testvault.File{Path: "Prod/Beta.md", Content: "one\r\ntwo\r\n"},
	)
	records := scanAndClassify(t, root)

	pair := findPair(t, records, "Beta.md")
	if pair.Bucket != classify.BucketCRLFOnly {
		t.Errorf("bucket = %s, want CRLF-ONLY", pair.Bucket)
	}
}

func TestClassify_DivergedBodies(t *testing.T) {
	root := testvault.Build(t,
		testvault.File{Path: "Notes/Gamma.md", Content: "original\n"},
		testvault.File{Path: "Prod/Gamma.md", Content: "altered\n"},
	)
	records := scanAndClassify(t, root)

	pair := findPair(t, records, "Gamma.md")
	if pair.Bucket != classify.BucketDiverged {
		t.Errorf("bucket = %s, want DIVERGED", pair.Bucket)
	}
}

func TestClassify_UniqueHasNoPair(t *testing.T) {
	root := testvault.Build(t,
		testvault.File{Path: "Notes/Solo.md", Content: "alone\n"},
	)
	records := scanAndClassify(t, root)

	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	r := records[0]
	if r.Type != "unique" || r.Bucket != classify.BucketUnique {
		t.Errorf("got type=%s bucket=%s, want unique/UNIQUE", r.Type, r.Bucket)
	}
	if r.Path != "Notes/Solo.md" {
		t.Errorf("path = %q, want Notes/Solo.md", r.Path)
	}
}

func TestClassify_MixedVault(t *testing.T) {
	root := testvault.Build(t,
		testvault.File{Path: "Notes/Alpha.md", Content: "alpha\n"},
		testvault.File{Path: "Prod/Alpha.md", Content: "alpha\n"}, // EXACT
		testvault.File{Path: "Notes/Beta.md", Content: "beta\n"},
		testvault.File{Path: "Prod/Beta.md", Content: "beta\r\n"},    // CRLF-ONLY
		testvault.File{Path: "Notes/Solo.md", Content: "only one\n"}, // UNIQUE
	)
	records := scanAndClassify(t, root)

	buckets := map[string]classify.Bucket{}
	for _, r := range records {
		key := r.Basename
		buckets[key] = r.Bucket
	}

	if buckets["Alpha.md"] != classify.BucketExact {
		t.Errorf("Alpha.md bucket = %s, want EXACT", buckets["Alpha.md"])
	}
	if buckets["Beta.md"] != classify.BucketCRLFOnly {
		t.Errorf("Beta.md bucket = %s, want CRLF-ONLY", buckets["Beta.md"])
	}
	if buckets["Solo.md"] != classify.BucketUnique {
		t.Errorf("Solo.md bucket = %s, want UNIQUE", buckets["Solo.md"])
	}
}

func TestClassify_OutputIsDeterministic(t *testing.T) {
	// Same input → same output, byte-for-byte. This is the "pure function"
	// invariant from the spec.
	root := testvault.Build(t,
		testvault.File{Path: "Notes/Alpha.md", Content: "a\n"},
		testvault.File{Path: "Prod/Alpha.md", Content: "a\n"},
		testvault.File{Path: "Zeta.md", Content: "z\n"},
	)
	first := scanAndClassify(t, root)
	second := scanAndClassify(t, root)
	if len(first) != len(second) {
		t.Fatalf("record counts differ: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if !reflect.DeepEqual(first[i], second[i]) {
			t.Errorf("record[%d] differs: %+v vs %+v", i, first[i], second[i])
		}
	}
}

func scanAndClassify(t *testing.T, vaultRoot string) []classify.Record {
	t.Helper()
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "index.jsonl")
	classPath := filepath.Join(dir, "classification.jsonl")

	if err := scan.Run(scan.Options{VaultRoot: vaultRoot, OutputPath: indexPath}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}
	if err := classify.Run(classify.Options{IndexPath: indexPath, ClassificationPath: classPath}); err != nil {
		t.Fatalf("classify.Run: %v", err)
	}

	r, err := artifact.NewReader(classPath, classify.Schema)
	if err != nil {
		t.Fatalf("artifact.NewReader: %v", err)
	}
	defer r.Close()

	var records []classify.Record
	for {
		var rec classify.Record
		err := r.Next(&rec)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		records = append(records, rec)
	}
	return records
}

func findPair(t *testing.T, records []classify.Record, basename string) classify.Record {
	t.Helper()
	for _, r := range records {
		if r.Type == "pair" && r.Basename == basename {
			return r
		}
	}
	t.Fatalf("no pair record for basename %q in %+v", basename, records)
	return classify.Record{}
}
