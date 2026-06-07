package chatgptappcache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiscoverReadsConversationIDsFromFilenamesOnly(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "conversations-v3-account")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatalf("create cache: %v", err)
	}
	older := filepath.Join(cache, "chatgpt-older-1.data")
	newer := filepath.Join(cache, "chatgpt-newer-1.data")
	invalid := filepath.Join(cache, "bad id.data")
	for _, path := range []string{older, newer, invalid} {
		if err := os.WriteFile(path, []byte("opaque app cache body"), 0o600); err != nil {
			t.Fatalf("write cache file: %v", err)
		}
	}
	if err := os.Chtimes(older, time.Unix(1000, 0), time.Unix(1000, 0)); err != nil {
		t.Fatalf("chtime older: %v", err)
	}
	if err := os.Chtimes(newer, time.Unix(2000, 0), time.Unix(2000, 0)); err != nil {
		t.Fatalf("chtime newer: %v", err)
	}

	report, err := Discover(root, 0)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if report.SourceKind != SourceKind {
		t.Fatalf("source kind = %q, want %q", report.SourceKind, SourceKind)
	}
	if got, want := report.IDs, []string{"chatgpt-newer-1", "chatgpt-older-1"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("ids = %+v, want %+v", got, want)
	}
	if len(report.Warnings) != 1 {
		t.Fatalf("warnings = %+v, want invalid-id warning", report.Warnings)
	}
}

func TestDiscoverAppliesLimitAfterNewestOrdering(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "project", "conversations-v3-account")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatalf("create cache: %v", err)
	}
	for i, id := range []string{"chatgpt-old", "chatgpt-new"} {
		path := filepath.Join(cache, id+".data")
		if err := os.WriteFile(path, []byte("opaque"), 0o600); err != nil {
			t.Fatalf("write cache file: %v", err)
		}
		when := time.Unix(int64(1000+i), 0)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatalf("chtime cache file: %v", err)
		}
	}
	report, err := Discover(root, 1)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if got, want := report.IDs, []string{"chatgpt-new"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("ids = %+v, want %+v", got, want)
	}
}
