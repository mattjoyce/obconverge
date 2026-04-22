package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestNew_TextHandler(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Level: "info", Format: "text", Writer: &buf})
	l.Info("hello", "key", "value")
	out := buf.String()
	if !strings.Contains(out, "hello") {
		t.Errorf("text output missing message: %q", out)
	}
	if !strings.Contains(out, "key=value") {
		t.Errorf("text output missing attribute: %q", out)
	}
}

func TestNew_JSONHandler(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Level: "info", Format: "json", Writer: &buf})
	l.Info("hello", "key", "value")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("json output not valid JSON: %v\nraw: %s", err, buf.String())
	}
	if rec["msg"] != "hello" {
		t.Errorf("msg = %v, want hello", rec["msg"])
	}
	if rec["key"] != "value" {
		t.Errorf("key = %v, want value", rec["key"])
	}
}

func TestNew_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Level: "warn", Format: "text", Writer: &buf})
	l.Info("quiet")
	l.Debug("quieter")
	l.Warn("loud")
	out := buf.String()
	if strings.Contains(out, "quiet") {
		t.Errorf("info should be filtered at warn level: %q", out)
	}
	if !strings.Contains(out, "loud") {
		t.Errorf("warn should be emitted: %q", out)
	}
}

func TestNew_InvalidLevelFallsBackToInfo(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Level: "bogus", Format: "text", Writer: &buf})
	l.Debug("should-not-appear")
	l.Info("should-appear")
	out := buf.String()
	if strings.Contains(out, "should-not-appear") {
		t.Errorf("debug should be filtered at default (info) level: %q", out)
	}
	if !strings.Contains(out, "should-appear") {
		t.Errorf("info should be emitted at default level: %q", out)
	}
}

func TestNew_InvalidFormatFallsBackToText(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Level: "info", Format: "bogus", Writer: &buf})
	l.Info("plaintext")
	// Text handler output is not valid JSON by itself.
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err == nil {
		t.Errorf("expected non-JSON (text) output, got valid JSON: %s", buf.String())
	}
}
