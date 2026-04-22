package frontmatter

import (
	"reflect"
	"testing"
)

func TestSplit_NoFrontmatter(t *testing.T) {
	content := []byte("# no frontmatter here\n\nbody line\n")
	fm, body := Split(content)
	if fm != nil {
		t.Errorf("fm = %q, want nil", fm)
	}
	if string(body) != string(content) {
		t.Errorf("body mismatch: got %q, want %q", body, content)
	}
}

func TestSplit_SimpleFrontmatter(t *testing.T) {
	content := []byte("---\ntitle: Hello\ntags:\n  - a\n  - b\n---\n\n# Body\n\nparagraph\n")
	fm, body := Split(content)
	wantFM := "title: Hello\ntags:\n  - a\n  - b\n"
	if string(fm) != wantFM {
		t.Errorf("fm = %q, want %q", fm, wantFM)
	}
	wantBody := "# Body\n\nparagraph\n"
	if string(body) != wantBody {
		t.Errorf("body = %q, want %q", body, wantBody)
	}
}

func TestSplit_CRLFDelimiters(t *testing.T) {
	content := []byte("---\r\ntitle: Hi\r\n---\r\n\r\nBody\r\n")
	fm, body := Split(content)
	if len(fm) == 0 {
		t.Fatalf("expected non-empty fm, got %q", fm)
	}
	if string(body) != "Body\r\n" {
		t.Errorf("body = %q, want %q", body, "Body\r\n")
	}
}

func TestSplit_UnclosedFrontmatterTreatedAsBody(t *testing.T) {
	content := []byte("---\ntitle: Hello\n\nno closing\n")
	fm, body := Split(content)
	if fm != nil {
		t.Errorf("fm should be nil when closing delim is missing; got %q", fm)
	}
	if string(body) != string(content) {
		t.Errorf("body should be the whole content; got %q", body)
	}
}

func TestSplit_FrontmatterNotAtOffsetZero(t *testing.T) {
	content := []byte("# Heading\n\n---\nfake: true\n---\n")
	fm, body := Split(content)
	if fm != nil {
		t.Errorf("fm should be nil when content doesn't start at --- ; got %q", fm)
	}
	if string(body) != string(content) {
		t.Errorf("body should be unchanged; got %q", body)
	}
}

func TestSplit_EmptyFrontmatterBlock(t *testing.T) {
	content := []byte("---\n---\nbody\n")
	fm, body := Split(content)
	if string(fm) != "" {
		t.Errorf("fm = %q, want empty string (not nil)", fm)
	}
	if fm == nil {
		t.Errorf("fm should be non-nil (empty) when the block exists")
	}
	if string(body) != "body\n" {
		t.Errorf("body = %q, want %q", body, "body\n")
	}
}

func TestExtractFields_TagsAndAliases(t *testing.T) {
	fm := []byte("tags:\n  - project\n  - cli\naliases:\n  - Alt Name\nsource: https://example.com\n")
	got, err := ExtractFields(fm)
	if err != nil {
		t.Fatalf("ExtractFields: %v", err)
	}
	want := Fields{
		Tags:    []string{"project", "cli"},
		Aliases: []string{"Alt Name"},
		Source:  "https://example.com",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestExtractFields_EmptyIsZero(t *testing.T) {
	got, err := ExtractFields([]byte("   \n"))
	if err != nil {
		t.Fatalf("ExtractFields: %v", err)
	}
	if len(got.Tags) != 0 || len(got.Aliases) != 0 || got.Source != "" {
		t.Errorf("expected zero Fields, got %+v", got)
	}
}

func TestExtractFields_InvalidYAML(t *testing.T) {
	// Unclosed flow mapping — yaml.v3 rejects this.
	fm := []byte("tags: {unclosed\n")
	_, err := ExtractFields(fm)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestExtractFields_IgnoresUnknownKeys(t *testing.T) {
	fm := []byte("tags: [a]\nunknown_key: value\nauthor: someone\n")
	got, err := ExtractFields(fm)
	if err != nil {
		t.Fatalf("ExtractFields: %v", err)
	}
	if !reflect.DeepEqual(got.Tags, []string{"a"}) {
		t.Errorf("Tags = %v, want [a]", got.Tags)
	}
}

func TestSplit_RoundTripThroughExtractFields(t *testing.T) {
	content := []byte(`---
title: Demo
tags:
  - first
  - second
aliases:
  - Demo Alias
---

body
`)
	fm, body := Split(content)
	fields, err := ExtractFields(fm)
	if err != nil {
		t.Fatalf("ExtractFields: %v", err)
	}
	if !reflect.DeepEqual(fields.Tags, []string{"first", "second"}) {
		t.Errorf("Tags = %v", fields.Tags)
	}
	if !reflect.DeepEqual(fields.Aliases, []string{"Demo Alias"}) {
		t.Errorf("Aliases = %v", fields.Aliases)
	}
	if string(body) != "body\n" {
		t.Errorf("body = %q", body)
	}
}
