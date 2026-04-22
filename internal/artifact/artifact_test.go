package artifact

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type sampleRecord struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestWriteRead_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.jsonl")

	w, err := NewWriter(path, "sample/1")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	inputs := []sampleRecord{
		{Name: "alpha", Count: 1},
		{Name: "beta", Count: 2},
		{Name: "gamma", Count: 3},
	}
	for _, r := range inputs {
		if err := w.Write(r); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, err := NewReader(path, "sample/1")
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	defer r.Close()

	if got := r.Header().Schema; got != "sample/1" {
		t.Fatalf("header schema: got %q, want %q", got, "sample/1")
	}

	var got []sampleRecord
	for {
		var rec sampleRecord
		err := r.Next(&rec)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		got = append(got, rec)
	}
	if len(got) != len(inputs) {
		t.Fatalf("record count: got %d, want %d", len(got), len(inputs))
	}
	for i := range inputs {
		if got[i] != inputs[i] {
			t.Errorf("record[%d] = %+v, want %+v", i, got[i], inputs[i])
		}
	}
}

func TestReader_RejectsWrongSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mismatch.jsonl")
	w, err := NewWriter(path, "index/99")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	_ = w.Close()

	_, err = NewReader(path, "index/1")
	if !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("expected ErrUnsupportedSchema, got %v", err)
	}
}

func TestReader_RejectsMissingHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-header.jsonl")
	// Write a record with no header first line.
	if err := os.WriteFile(path, []byte(`{"name":"x","count":1}`+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := NewReader(path, "sample/1")
	if err == nil {
		t.Fatal("expected error when first record is not a header")
	}
}

func TestReader_RejectsEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := NewReader(path, "sample/1")
	if err == nil {
		t.Fatal("expected error on empty file")
	}
}
