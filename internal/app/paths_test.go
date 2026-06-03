package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsurePrivateDirDoesNotChmodExistingParent(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.Chmod(parent, 0o755); err != nil {
		t.Fatalf("chmod parent: %v", err)
	}
	child := filepath.Join(parent, "aicrawl")
	if err := ensurePrivateDir(child); err != nil {
		t.Fatalf("ensurePrivateDir: %v", err)
	}
	parentInfo, err := os.Stat(parent)
	if err != nil {
		t.Fatalf("stat parent: %v", err)
	}
	if got := parentInfo.Mode().Perm(); got != 0o755 {
		t.Fatalf("parent mode = %o, want 0755", got)
	}
	childInfo, err := os.Stat(child)
	if err != nil {
		t.Fatalf("stat child: %v", err)
	}
	if got := childInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("child mode = %o, want 0700", got)
	}
}

func TestEnsurePrivateRuntimeDoesNotChmodExistingConfiguredDir(t *testing.T) {
	shared := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(shared, 0o755); err != nil {
		t.Fatalf("mkdir shared: %v", err)
	}
	if err := os.Chmod(shared, 0o755); err != nil {
		t.Fatalf("chmod shared: %v", err)
	}
	rt := runtime{
		ConfigPath: filepath.Join(shared, "aicrawl.toml"),
		DBPath:     filepath.Join(shared, "aicrawl.db"),
		CacheDir:   filepath.Join(shared, "cache"),
		LogDir:     filepath.Join(shared, "logs"),
		ShareDir:   filepath.Join(shared, "share"),
	}
	if err := ensurePrivateRuntime(rt); err != nil {
		t.Fatalf("ensurePrivateRuntime: %v", err)
	}
	info, err := os.Stat(shared)
	if err != nil {
		t.Fatalf("stat shared: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("shared mode = %o, want 0755", got)
	}
}
