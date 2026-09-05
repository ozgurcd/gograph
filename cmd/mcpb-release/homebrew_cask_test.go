package main

import (
	"bytes"
	"testing"
)

func TestRewriteHomebrewCaskPostflight(t *testing.T) {
	input := append([]byte("cask header\n"), legacyHomebrewPostflight...)
	input = append(input, []byte("\ncask footer\n")...)

	got, err := rewriteHomebrewCaskPostflight(input)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, legacyHomebrewPostflight) {
		t.Fatal("rewritten cask retains deprecated postflight hook")
	}
	if bytes.Contains(got, []byte("  postflight do\n")) {
		t.Fatal("rewritten cask contains a legacy postflight stanza")
	}
	if count := bytes.Count(got, structuredHomebrewPostflight); count != 1 {
		t.Fatalf("rewritten cask contains structured postflight_steps %d times, want 1", count)
	}
	for _, required := range [][]byte{
		[]byte("  postflight_steps do\n"),
		[]byte("run \"/usr/bin/xattr\""),
		[]byte("{{staged_path}}/gograph"),
	} {
		if !bytes.Contains(got, required) {
			t.Fatalf("rewritten cask is missing %q", required)
		}
	}
	idempotent, err := rewriteHomebrewCaskPostflight(got)
	if err != nil {
		t.Fatalf("rewrite structured cask: %v", err)
	}
	if !bytes.Equal(idempotent, got) {
		t.Fatal("rewriting a structured cask changed its bytes")
	}
}

func TestRewriteHomebrewCaskPostflightFailsClosed(t *testing.T) {
	tests := map[string][]byte{
		"missing":     []byte("cask without a postflight hook"),
		"duplicate":   append(bytes.Clone(legacyHomebrewPostflight), legacyHomebrewPostflight...),
		"conflicting": append(bytes.Clone(legacyHomebrewPostflight), structuredHomebrewPostflight...),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := rewriteHomebrewCaskPostflight(input); err == nil {
				t.Fatal("rewrite succeeded for unsupported cask input")
			}
		})
	}
}
