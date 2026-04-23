package classify_test

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mattjoyce/obconverge/internal/artifact"
	"github.com/mattjoyce/obconverge/internal/classify"
	"github.com/mattjoyce/obconverge/internal/scan"
	"github.com/mattjoyce/obconverge/internal/secrets"
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

func TestClassify_TagDelta_OnlyTagsDiffer(t *testing.T) {
	root := testvault.Build(t,
		testvault.File{Path: "Notes/Delta.md", Content: "---\ntitle: Same\ntags:\n  - a\n---\n\nshared body\n"},
		testvault.File{Path: "Prod/Delta.md", Content: "---\ntitle: Same\ntags:\n  - b\n---\n\nshared body\n"},
	)
	records := scanAndClassify(t, root)
	pair := findPair(t, records, "Delta.md")
	if pair.Bucket != classify.BucketTagDelta {
		t.Errorf("bucket = %s, want TAG-DELTA (only tags differ)", pair.Bucket)
	}
}

func TestClassify_FrontmatterOnly_WhenNonTagKeyDiffers(t *testing.T) {
	// Both have tags (different AND title key differs) -> FRONTMATTER-ONLY,
	// not TAG-DELTA, because the difference isn't tags-only.
	root := testvault.Build(t,
		testvault.File{Path: "Notes/Eps.md", Content: "---\ntitle: One\ntags:\n  - a\n---\n\nshared body\n"},
		testvault.File{Path: "Prod/Eps.md", Content: "---\ntitle: Two\ntags:\n  - b\n---\n\nshared body\n"},
	)
	records := scanAndClassify(t, root)
	pair := findPair(t, records, "Eps.md")
	if pair.Bucket != classify.BucketFrontmatterOnly {
		t.Errorf("bucket = %s, want FRONTMATTER-ONLY (title also differs)", pair.Bucket)
	}
}

func TestClassify_FrontmatterEqual_SameFrontmatterDifferentBody(t *testing.T) {
	root := testvault.Build(t,
		testvault.File{Path: "Notes/Epsilon.md", Content: "---\ntags: [x]\n---\n\nbody one\n"},
		testvault.File{Path: "Prod/Epsilon.md", Content: "---\ntags: [x]\n---\n\nbody two\n"},
	)
	records := scanAndClassify(t, root)
	pair := findPair(t, records, "Epsilon.md")
	if pair.Bucket != classify.BucketFrontmatterEqual {
		t.Errorf("bucket = %s, want FRONTMATTER-EQUAL", pair.Bucket)
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

func TestClassify_Secrets_PairBucketWinsOverExact(t *testing.T) {
	// Both files are byte-identical AND contain a credential. The spec says
	// SECRETS must win regardless of any other similarity signal.
	content := "token: AKIAIOSFODNN7EXAMPLE\n"
	root := testvault.Build(t,
		testvault.File{Path: "Notes/Keys.md", Content: content},
		testvault.File{Path: "Prod/Keys.md", Content: content},
	)
	records := scanAndClassify(t, root)
	pair := findPair(t, records, "Keys.md")
	if pair.Bucket != classify.BucketSecrets {
		t.Errorf("bucket = %s, want SECRETS (must win over EXACT)", pair.Bucket)
	}
	if pair.SecretPattern != "aws-access-key" {
		t.Errorf("SecretPattern = %q, want aws-access-key", pair.SecretPattern)
	}
}

func TestClassify_Secrets_UniqueFileQuarantined(t *testing.T) {
	root := testvault.Build(t,
		testvault.File{Path: "Notes/Lonely.md", Content: "key: AKIAIOSFODNN7EXAMPLE\n"},
	)
	records := scanAndClassify(t, root)
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	r := records[0]
	if r.Type != "unique" {
		t.Errorf("type = %s, want unique", r.Type)
	}
	if r.Bucket != classify.BucketSecrets {
		t.Errorf("bucket = %s, want SECRETS", r.Bucket)
	}
}

func TestClassify_SecretsNeverLeakContent(t *testing.T) {
	// Invariant from SPEC.md: the SECRETS record must not contain the
	// credential content itself — only a pattern name.
	secret := "AKIAIOSFODNN7EXAMPLE"
	root := testvault.Build(t,
		testvault.File{Path: "Keys.md", Content: "token: " + secret + "\n"},
	)
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "index.jsonl")
	classPath := filepath.Join(dir, "classification.jsonl")

	if err := scan.Run(scan.Options{VaultRoot: root, OutputPath: indexPath, Detector: secrets.NewBuiltins()}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}
	if err := classify.Run(classify.Options{IndexPath: indexPath, ClassificationPath: classPath}); err != nil {
		t.Fatalf("classify.Run: %v", err)
	}

	// Read the raw classification bytes and assert they do NOT contain the secret.
	data := mustReadFile(t, classPath)
	if strings.Contains(string(data), secret) {
		t.Errorf("classification.jsonl leaked the secret: %s", data)
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
		buckets[r.Basename] = r.Bucket
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
	// Same input → same output, byte-for-byte. The "pure function" invariant.
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

// helpers

func scanAndClassify(t *testing.T, vaultRoot string) []classify.Record {
	t.Helper()
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "index.jsonl")
	classPath := filepath.Join(dir, "classification.jsonl")

	if err := scan.Run(scan.Options{VaultRoot: vaultRoot, OutputPath: indexPath, Detector: secrets.NewBuiltins()}); err != nil {
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

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readFile %s: %v", path, err)
	}
	return data
}
