// Package artifact reads and writes JSONL artifacts with a schema-versioned header.
//
// Every artifact obconverge produces — index.jsonl, classification.jsonl,
// journal.jsonl — is a sequence of JSON objects, one per line, with a header
// record as the first line. The header declares a schema version; readers
// refuse to consume an artifact whose schema they don't recognize.
package artifact

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// ErrUnsupportedSchema signals that a reader rejected an artifact whose header
// schema did not match what the reader expects.
var ErrUnsupportedSchema = errors.New("artifact: unsupported schema")

// Header is the first record of every JSONL artifact.
type Header struct {
	Type    string    `json:"type"` // always "header"
	Schema  string    `json:"schema"`
	Created time.Time `json:"created"`
}

// Writer emits one JSON object per line. The header is written on construction.
type Writer struct {
	f      *os.File
	enc    *json.Encoder
	closed bool
}

// NewWriter creates the file at path, writes the header record, and returns a
// writer for subsequent records.
func NewWriter(path, schema string) (*Writer, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("artifact: create %s: %w", path, err)
	}
	enc := json.NewEncoder(f)
	if err := enc.Encode(Header{Type: "header", Schema: schema, Created: time.Now().UTC()}); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("artifact: write header: %w", err)
	}
	return &Writer{f: f, enc: enc}, nil
}

// Write emits one record as a single JSON line.
func (w *Writer) Write(v any) error {
	return w.enc.Encode(v)
}

// Sync forces a flush to disk. Callers that care about durability (the journal)
// should call this after each append.
func (w *Writer) Sync() error {
	return w.f.Sync()
}

// Close closes the underlying file.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	return w.f.Close()
}

// Reader iterates JSONL records. The header is consumed on construction and
// its schema is validated against expectedSchema.
type Reader struct {
	f      *os.File
	sc     *bufio.Scanner
	header Header
}

// NewReader opens path, reads and validates the header.
func NewReader(path, expectedSchema string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("artifact: open %s: %w", path, err)
	}
	sc := bufio.NewScanner(f)
	// Allow large individual lines — an index entry can be a few KB.
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)

	if !sc.Scan() {
		_ = f.Close()
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("artifact: read header: %w", err)
		}
		return nil, fmt.Errorf("artifact: %s is empty", path)
	}

	var h Header
	if err := json.Unmarshal(sc.Bytes(), &h); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("artifact: parse header: %w", err)
	}
	if h.Type != "header" {
		_ = f.Close()
		return nil, fmt.Errorf("artifact: first record is not a header (type=%q)", h.Type)
	}
	if h.Schema != expectedSchema {
		_ = f.Close()
		return nil, fmt.Errorf("%w: want %q, got %q", ErrUnsupportedSchema, expectedSchema, h.Schema)
	}

	return &Reader{f: f, sc: sc, header: h}, nil
}

// Header returns the header that was parsed on open.
func (r *Reader) Header() Header { return r.header }

// Next reads the next record into v, or returns io.EOF at end of file.
func (r *Reader) Next(v any) error {
	if !r.sc.Scan() {
		if err := r.sc.Err(); err != nil {
			return err
		}
		return io.EOF
	}
	return json.Unmarshal(r.sc.Bytes(), v)
}

// Close closes the underlying file.
func (r *Reader) Close() error { return r.f.Close() }
