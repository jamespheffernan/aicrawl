package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openclaw/aicrawl/internal/archive"
	"github.com/openclaw/aicrawl/internal/schema"
	"github.com/openclaw/aicrawl/internal/sync/websync"
	"github.com/openclaw/crawlkit/control"
	_ "modernc.org/sqlite"
	"nhooyr.io/websocket"
)

func TestDoctorJSONReportsErrorStateForInspectionErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks are not reliable as root")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	blocked := filepath.Join(home, ".crawlbar")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatalf("create blocked dir: %v", err)
	}
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatalf("chmod blocked dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(blocked, 0o700)
	})

	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{"doctor", "--json"}); err != nil {
		t.Fatalf("doctor --json: %v", err)
	}
	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode doctor json: %v", err)
	}
	if report.State != "error" {
		t.Fatalf("doctor state = %q, want error; report = %+v", report.State, report)
	}
	if len(report.Errors) == 0 {
		t.Fatalf("doctor errors are empty, want inspection error")
	}
}

func TestCrawlbarManifestTightensExistingFileMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	outDir := filepath.Join(home, "manifests")
	if err := os.Mkdir(outDir, 0o755); err != nil {
		t.Fatalf("create manifest dir: %v", err)
	}
	outPath := filepath.Join(outDir, "aicrawl.json")
	if err := os.WriteFile(outPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write existing manifest: %v", err)
	}
	if err := os.Chmod(outPath, 0o644); err != nil {
		t.Fatalf("chmod existing manifest: %v", err)
	}

	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{"crawlbar", "manifest", "--out", outPath}); err != nil {
		t.Fatalf("crawlbar manifest: %v", err)
	}
	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("stat manifest: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("manifest mode = %o, want 0600", got)
	}
}

func TestMetadataManifestIncludesWebSyncAndLocalTranscriptScopes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{"metadata", "--json"}); err != nil {
		t.Fatalf("metadata --json: %v", err)
	}
	var manifest control.Manifest
	if err := json.Unmarshal(stdout.Bytes(), &manifest); err != nil {
		t.Fatalf("decode metadata manifest: %v", err)
	}
	for _, command := range []string{"sync-web", "reconcile", "schedule-launchd"} {
		if _, ok := manifest.Commands[command]; !ok {
			t.Fatalf("manifest commands missing %q: %+v", command, manifest.Commands)
		}
	}
	for _, capability := range []string{"authenticated-browser-web-sync", "local-agent-transcript-import", "official-export-reconciliation"} {
		if !containsString(manifest.Capabilities, capability) {
			t.Fatalf("manifest capabilities missing %q: %+v", capability, manifest.Capabilities)
		}
	}
	for _, scope := range []string{"chatgpt_web", "claude_web", "openclaw_jsonl", "codex_jsonl", "gemini_cli", "claude_code_jsonl", "cursor_store"} {
		if !containsString(manifest.Privacy.LocalOnlyScopes, scope) {
			t.Fatalf("manifest local scopes missing %q: %+v", scope, manifest.Privacy.LocalOnlyScopes)
		}
	}
}

func TestDoctorJSONReportsDatabaseSchemaAndLastImport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{"init"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	stdout.Reset()

	fixturePath := filepath.Join("..", "..", "testdata", "redacted", "claude-export.fixture.json")
	if err := cli.Run(context.Background(), []string{"import", fixturePath, "--provider", "claude"}); err != nil {
		t.Fatalf("import fixture: %v", err)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"doctor", "--json"}); err != nil {
		t.Fatalf("doctor --json: %v", err)
	}
	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode doctor json: %v", err)
	}
	if report.DatabaseSchemaVersion != schema.Version {
		t.Fatalf("database schema version = %d, want %d", report.DatabaseSchemaVersion, schema.Version)
	}
	if report.LastImportAt == "" {
		t.Fatalf("last_import_at is empty")
	}
	if report.Counts.Conversations == 0 || report.Counts.Messages == 0 {
		t.Fatalf("counts = %+v, want imported conversations and messages", report.Counts)
	}
}

func TestImportDryRunReportsCountsWithoutCreatingArchive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	fixturePath := filepath.Join("..", "..", "testdata", "redacted", "chatgpt-export.fixture.json")
	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{"import", fixturePath, "--dry-run", "--json"}); err != nil {
		t.Fatalf("import dry-run: %v", err)
	}
	var report importDryRunReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode dry-run report: %v", err)
	}
	if !report.DryRun || report.Provider != "chatgpt" || report.SourceKind != "chatgpt_export" {
		t.Fatalf("report identity = %+v", report)
	}
	if report.Conversations != 1 || report.Messages == 0 {
		t.Fatalf("dry-run counts = %+v, want one conversation with messages", report)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"status", "--json"}); err != nil {
		t.Fatalf("status after dry-run: %v", err)
	}
	var status struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.State != "uninitialized" {
		t.Fatalf("status after dry-run = %q, want uninitialized", status.State)
	}
}

func TestImportDryRunCursorStoreReportsCounts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	storePath := filepath.Join(t.TempDir(), "workspace", "cursor-fixture-session", "store.db")
	writeCursorStoreFixture(t, storePath)

	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{"import", storePath, "--provider", "cursor", "--dry-run", "--json"}); err != nil {
		t.Fatalf("import cursor dry-run: %v", err)
	}
	var report importDryRunReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode dry-run report: %v", err)
	}
	if !report.DryRun || report.Provider != "cursor" || report.SourceKind != "cursor_store" {
		t.Fatalf("report identity = %+v", report)
	}
	if report.Conversations != 1 || report.Messages != 2 {
		t.Fatalf("dry-run counts = %+v, want one cursor conversation with two visible messages", report)
	}
}

func TestImportDirectoryDryRunReportsLocalSourceCountsWithoutCreatingArchive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	sourceDir := filepath.Join(t.TempDir(), "openclaw-root", "archive")
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatalf("create source dir: %v", err)
	}
	copyFixture(t,
		filepath.Join("..", "..", "testdata", "redacted", "openclaw-session.fixture.jsonl"),
		filepath.Join(sourceDir, "session.fixture.jsonl"),
	)
	if err := os.WriteFile(filepath.Join(sourceDir, "control-only.jsonl"), []byte(`{"type":"session","version":1,"id":"control-only","timestamp":"2025-10-09T08:53:20Z","cwd":"/private/workspace"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write control-only source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "ignored.txt"), []byte("ignored"), 0o600); err != nil {
		t.Fatalf("write ignored file: %v", err)
	}

	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{"import", filepath.Dir(sourceDir), "--provider", "openclaw", "--dry-run", "--json"}); err != nil {
		t.Fatalf("directory dry-run: %v", err)
	}
	var report importDryRunReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode directory dry-run: %v", err)
	}
	if report.Provider != "openclaw" || report.SourceKind != "openclaw_jsonl" || report.Sources != 2 || report.SkippedSources != 1 {
		t.Fatalf("report identity = %+v, want two OpenClaw sources with one skipped", report)
	}
	if report.Conversations != 1 || report.Messages == 0 {
		t.Fatalf("report counts = %+v, want one conversation with messages", report)
	}
	if len(report.Warnings) != 1 || strings.Contains(report.Warnings[0], sourceDir) {
		t.Fatalf("warnings = %+v, want one redacted skipped-source warning", report.Warnings)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"status", "--json"}); err != nil {
		t.Fatalf("status after directory dry-run: %v", err)
	}
	var status struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.State != "uninitialized" {
		t.Fatalf("status after directory dry-run = %q, want uninitialized", status.State)
	}
}

func TestImportDirectorySkipsOpenClawSourcesWithoutVisibleMessages(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	sourceDir := filepath.Join(t.TempDir(), "openclaw-root", "sessions")
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatalf("create source dir: %v", err)
	}
	copyFixture(t,
		filepath.Join("..", "..", "testdata", "redacted", "openclaw-session.fixture.jsonl"),
		filepath.Join(sourceDir, "session.fixture.jsonl"),
	)
	if err := os.WriteFile(filepath.Join(sourceDir, "control-only.jsonl"), []byte(`{"type":"session","version":1,"id":"control-only","timestamp":"2025-10-09T08:53:20Z","cwd":"/private/workspace"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write control-only source: %v", err)
	}

	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{"import", filepath.Dir(sourceDir), "--provider", "openclaw", "--json"}); err != nil {
		t.Fatalf("directory import: %v", err)
	}
	var stats importDirectoryStats
	if err := json.Unmarshal(stdout.Bytes(), &stats); err != nil {
		t.Fatalf("decode directory import: %v", err)
	}
	if stats.Provider != "openclaw" || stats.SourceKind != "openclaw_jsonl" || stats.Sources != 2 || stats.ImportedSources != 1 || stats.SkippedSources != 1 {
		t.Fatalf("directory stats = %+v, want one imported and one skipped OpenClaw source", stats)
	}
	if stats.Conversations != 1 || stats.Messages == 0 {
		t.Fatalf("directory counts = %+v, want one conversation with messages", stats)
	}
	if len(stats.Warnings) != 1 || strings.Contains(stats.Warnings[0], sourceDir) {
		t.Fatalf("warnings = %+v, want one redacted skipped-source warning", stats.Warnings)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"search", "openclaw jsonl fixture assistant phrase", "--provider", "openclaw", "--json"}); err != nil {
		t.Fatalf("search directory import: %v", err)
	}
	var hits []archive.SearchHit
	if err := json.Unmarshal(stdout.Bytes(), &hits); err != nil {
		t.Fatalf("decode search hits: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %+v, want one OpenClaw hit", hits)
	}
}

func TestImportDirectoryDryRunBoundsSkippedSourceWarnings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	sourceDir := filepath.Join(t.TempDir(), "openclaw-root", "sessions")
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatalf("create source dir: %v", err)
	}
	for i := 0; i < 105; i++ {
		data := fmt.Sprintf(`{"type":"session","version":1,"id":"control-only-%03d","timestamp":"2025-10-09T08:53:20Z","cwd":"/private/workspace"}`+"\n", i)
		if err := os.WriteFile(filepath.Join(sourceDir, fmt.Sprintf("control-only-%03d.jsonl", i)), []byte(data), 0o600); err != nil {
			t.Fatalf("write control-only source: %v", err)
		}
	}

	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{"import", filepath.Dir(sourceDir), "--provider", "openclaw", "--dry-run", "--json"}); err != nil {
		t.Fatalf("directory dry-run: %v", err)
	}
	var report importDryRunReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode directory dry-run: %v", err)
	}
	if report.Sources != 105 || report.SkippedSources != 105 || report.Conversations != 0 || report.Messages != 0 {
		t.Fatalf("report = %+v, want all sources skipped without importable messages", report)
	}
	if len(report.Warnings) != maxReportWarnings+1 {
		t.Fatalf("warning count = %d, want bounded warnings plus truncation summary", len(report.Warnings))
	}
	if report.Warnings[len(report.Warnings)-1] != "truncated 5 additional warnings" {
		t.Fatalf("last warning = %q, want truncation summary", report.Warnings[len(report.Warnings)-1])
	}
}

func TestImportDirectoryImportsSearchableLocalSourcesIdempotently(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	sourceDir := filepath.Join(t.TempDir(), "codex-root", "sessions", "2026")
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatalf("create source dir: %v", err)
	}
	copyFixture(t,
		filepath.Join("..", "..", "testdata", "redacted", "codex-session.fixture.jsonl"),
		filepath.Join(sourceDir, "rollout.fixture.jsonl"),
	)
	if err := os.WriteFile(filepath.Join(sourceDir, "metadata-only.jsonl"), []byte(`{"type":"turn_context","cwd":"/private/workspace"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write metadata-only Codex source: %v", err)
	}

	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{"import", filepath.Dir(filepath.Dir(sourceDir)), "--provider", "codex", "--json"}); err != nil {
		t.Fatalf("directory import: %v", err)
	}
	var stats importDirectoryStats
	if err := json.Unmarshal(stdout.Bytes(), &stats); err != nil {
		t.Fatalf("decode directory import: %v", err)
	}
	if stats.Provider != "codex" || stats.SourceKind != "codex_jsonl" || stats.Sources != 2 || stats.ImportedSources != 1 || stats.SkippedSources != 1 {
		t.Fatalf("directory stats = %+v, want one imported and one skipped Codex source", stats)
	}
	if stats.Conversations != 1 || stats.Messages == 0 {
		t.Fatalf("directory counts = %+v, want one conversation with messages", stats)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"search", "codex jsonl fixture assistant phrase", "--provider", "codex", "--json"}); err != nil {
		t.Fatalf("search directory import: %v", err)
	}
	var hits []archive.SearchHit
	if err := json.Unmarshal(stdout.Bytes(), &hits); err != nil {
		t.Fatalf("decode search hits: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %+v, want one Codex hit", hits)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"import", filepath.Dir(filepath.Dir(sourceDir)), "--provider", "codex", "--json"}); err != nil {
		t.Fatalf("repeat directory import: %v", err)
	}
	if err := json.Unmarshal(stdout.Bytes(), &stats); err != nil {
		t.Fatalf("decode repeat directory import: %v", err)
	}
	if stats.ImportedSources != 0 || stats.AlreadyImportedSources != 1 || stats.SkippedSources != 1 {
		t.Fatalf("repeat directory stats = %+v, want one already-imported and one skipped source", stats)
	}
}

func TestImportHermesDirectoryPrefersStateDBOverSidecarSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	hermesRoot := filepath.Join(t.TempDir(), ".hermes")
	writeHermesStoreFixture(t, filepath.Join(hermesRoot, "state.db"))
	copyFixture(t,
		filepath.Join("..", "..", "testdata", "redacted", "hermes-session.fixture.json"),
		filepath.Join(hermesRoot, "sessions", "session_stale_sidecar.json"),
	)

	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{"import", hermesRoot, "--provider", "hermes", "--json"}); err != nil {
		t.Fatalf("directory import: %v", err)
	}
	var stats importDirectoryStats
	if err := json.Unmarshal(stdout.Bytes(), &stats); err != nil {
		t.Fatalf("decode directory import: %v", err)
	}
	if stats.Provider != "hermes" || stats.SourceKind != "hermes_session" || stats.Sources != 1 || stats.ImportedSources != 1 {
		t.Fatalf("directory stats = %+v, want only state.db imported", stats)
	}
	if stats.Conversations != 1 || stats.Messages != 2 {
		t.Fatalf("directory counts = %+v, want state.db counts only", stats)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"search", "hermes store app fixture assistant phrase", "--provider", "hermes", "--json"}); err != nil {
		t.Fatalf("search directory import: %v", err)
	}
	var hits []archive.SearchHit
	if err := json.Unmarshal(stdout.Bytes(), &hits); err != nil {
		t.Fatalf("decode search hits: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %+v, want one Hermes state.db hit", hits)
	}
}

func TestImportGeminiDirectoryIgnoresNonSessionJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	geminiRoot := filepath.Join(t.TempDir(), ".gemini")
	sessionDir := filepath.Join(geminiRoot, "chats")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("create Gemini fixture dir: %v", err)
	}
	copyFixture(t,
		filepath.Join("..", "..", "testdata", "redacted", "gemini-session.fixture.json"),
		filepath.Join(sessionDir, "session.fixture.json"),
	)
	if err := os.WriteFile(filepath.Join(geminiRoot, "settings.json"), []byte(`{"mcpServers":{},"ui":{"theme":"dark"}}`), 0o600); err != nil {
		t.Fatalf("write Gemini settings fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(geminiRoot, "oauth_creds.json"), []byte(`{"refresh_token":"redacted","access_token":"redacted"}`), 0o600); err != nil {
		t.Fatalf("write Gemini auth fixture: %v", err)
	}

	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{"import", geminiRoot, "--provider", "gemini", "--dry-run", "--json"}); err != nil {
		t.Fatalf("Gemini directory dry-run: %v", err)
	}
	var report importDryRunReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode Gemini directory dry-run: %v", err)
	}
	if report.Provider != "gemini" || report.SourceKind != "gemini_cli" || report.Sources != 1 {
		t.Fatalf("report identity = %+v, want one Gemini session source", report)
	}
	if report.Conversations != 1 || report.Messages == 0 || report.SkippedSources != 0 {
		t.Fatalf("report counts = %+v, want only the session JSON imported", report)
	}
}

func TestReconcileOfficialExportReportsMissingAndArchivedRows(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	fixturePath := filepath.Join("..", "..", "testdata", "redacted", "chatgpt-export.fixture.json")
	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{"init"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"reconcile", fixturePath, "--provider", "chatgpt", "--json"}); err != nil {
		t.Fatalf("reconcile missing: %v", err)
	}
	var report reconcileReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode missing report: %v", err)
	}
	if report.Provider != "chatgpt" || report.SourceKind != "chatgpt_export" {
		t.Fatalf("report identity = %+v", report)
	}
	if report.SourceConversations != 1 || report.ArchivedConversations != 0 || report.MissingConversations != 1 {
		t.Fatalf("conversation coverage = %+v", report)
	}
	if report.SourceMessages == 0 || report.ArchivedMessages != 0 || report.MissingMessages != report.SourceMessages {
		t.Fatalf("message coverage = %+v", report)
	}
	if report.NextStep == "" {
		t.Fatalf("next step is empty for missing rows")
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"import", fixturePath, "--provider", "chatgpt", "--json"}); err != nil {
		t.Fatalf("import fixture: %v", err)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"reconcile", fixturePath, "--provider", "chatgpt", "--json"}); err != nil {
		t.Fatalf("reconcile archived: %v", err)
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode archived report: %v", err)
	}
	if report.MissingConversations != 0 || report.MissingMessages != 0 {
		t.Fatalf("missing after import = %+v, want none", report)
	}
	if report.ArchivedConversations != report.SourceConversations || report.ArchivedMessages != report.SourceMessages {
		t.Fatalf("archived coverage = %+v, want all source rows archived", report)
	}
}

func TestReconcileOfficialExportReportsDivergentMessages(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	dataHome := filepath.Join(home, ".local", "share")
	t.Setenv("XDG_DATA_HOME", dataHome)

	fixturePath := filepath.Join("..", "..", "testdata", "redacted", "chatgpt-export.fixture.json")
	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{"import", fixturePath, "--provider", "chatgpt", "--json"}); err != nil {
		t.Fatalf("import fixture: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dataHome, "aicrawl", "aicrawl.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(context.Background(), `update messages set text = 'divergent projection text' where id = (select id from messages order by id limit 1)`); err != nil {
		t.Fatalf("mutate message text: %v", err)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"reconcile", fixturePath, "--provider", "chatgpt", "--json"}); err != nil {
		t.Fatalf("reconcile divergent: %v", err)
	}
	var report reconcileReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode divergent report: %v", err)
	}
	if report.MissingConversations != 0 || report.MissingMessages != 0 {
		t.Fatalf("missing coverage = %+v, want none", report)
	}
	if report.DivergentMessages != 1 {
		t.Fatalf("divergent messages = %d, want 1 in %+v", report.DivergentMessages, report)
	}
	if report.NextStep == "" {
		t.Fatalf("next step is empty for divergent rows")
	}
}

func TestImportStreamsLargeConversationArray(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	fixturePath := filepath.Join(t.TempDir(), "large-chatgpt.fixture.json")
	writeLargeChatGPTFixture(t, fixturePath, 350)

	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{"init"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	stdout.Reset()
	if err := cli.Run(context.Background(), []string{"import", fixturePath}); err != nil {
		t.Fatalf("import large fixture: %v", err)
	}
	stdout.Reset()
	if err := cli.Run(context.Background(), []string{"conversations", "--provider", "chatgpt", "--limit", "400", "--json"}); err != nil {
		t.Fatalf("conversations --json: %v", err)
	}
	var conversations []archive.ConversationRow
	if err := json.Unmarshal(stdout.Bytes(), &conversations); err != nil {
		t.Fatalf("decode conversations: %v", err)
	}
	if len(conversations) != 350 {
		t.Fatalf("conversation count = %d, want 350", len(conversations))
	}
}

func TestDateFiltersIncludeFractionalSecondMatches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	fixturePath := filepath.Join(t.TempDir(), "fractional-chatgpt.fixture.json")
	data := `[
	  {
	    "id":"whole-second",
	    "title":"Whole Second",
	    "create_time":1760000000,
	    "current_node":"msg",
	    "mapping":{
	      "root":{"id":"root","parent":null,"children":["msg"],"message":null},
	      "msg":{
	        "id":"msg",
	        "parent":"root",
	        "children":[],
	        "message":{"create_time":1760000000,"author":{"role":"user"},"content":{"parts":["fractional-filter phrase whole"]}}
	      }
	    }
	  },
	  {
	    "id":"half-second",
	    "title":"Half Second",
	    "create_time":1760000000.5,
	    "current_node":"msg",
	    "mapping":{
	      "root":{"id":"root","parent":null,"children":["msg"],"message":null},
	      "msg":{
	        "id":"msg",
	        "parent":"root",
	        "children":[],
	        "message":{"create_time":1760000000.5,"author":{"role":"user"},"content":{"parts":["fractional-filter phrase half"]}}
	      }
	    }
	  }
	]`
	if err := os.WriteFile(fixturePath, []byte(data), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{"init"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	stdout.Reset()
	if err := cli.Run(context.Background(), []string{"import", fixturePath, "--provider", "chatgpt"}); err != nil {
		t.Fatalf("import fixture: %v", err)
	}
	stdout.Reset()

	since := "2025-10-09T08:53:20Z"
	if err := cli.Run(context.Background(), []string{"conversations", "--since", since, "--json"}); err != nil {
		t.Fatalf("conversations --since: %v", err)
	}
	var conversations []archive.ConversationRow
	if err := json.Unmarshal(stdout.Bytes(), &conversations); err != nil {
		t.Fatalf("decode conversations: %v", err)
	}
	if len(conversations) != 2 {
		t.Fatalf("conversations since whole second = %d, want whole and fractional conversations; rows = %+v", len(conversations), conversations)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"search", "fractional-filter phrase", "--since", since, "--json"}); err != nil {
		t.Fatalf("search --since: %v", err)
	}
	var hits []archive.SearchHit
	if err := json.Unmarshal(stdout.Bytes(), &hits); err != nil {
		t.Fatalf("decode search hits: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("search hits since whole second = %d, want whole and fractional hits; hits = %+v", len(hits), hits)
	}
}

func TestSearchMessagesContextAndSingleConversationExportCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{"init"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	stdout.Reset()

	fixturePath := filepath.Join("..", "..", "testdata", "redacted", "claude-export.fixture.json")
	if err := cli.Run(context.Background(), []string{"import", fixturePath, "--provider", "claude"}); err != nil {
		t.Fatalf("import fixture: %v", err)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"search", "known fixture phrase", "--group", "conversations", "--sort", "recent", "--json"}); err != nil {
		t.Fatalf("search grouped conversations: %v", err)
	}
	var conversationHits []archive.ConversationSearchHit
	if err := json.Unmarshal(stdout.Bytes(), &conversationHits); err != nil {
		t.Fatalf("decode conversation hits: %v", err)
	}
	if len(conversationHits) != 1 || conversationHits[0].ID == "" || conversationHits[0].BestMessageID == "" {
		t.Fatalf("conversation hits = %+v, want one hit with stable IDs", conversationHits)
	}
	conversationID := conversationHits[0].ID
	messageID := conversationHits[0].BestMessageID
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"search", "synthetic attachment text", "--scope", "attachments", "--json"}); err != nil {
		t.Fatalf("search attachments: %v", err)
	}
	var messageHits []archive.SearchHit
	if err := json.Unmarshal(stdout.Bytes(), &messageHits); err != nil {
		t.Fatalf("decode attachment hits: %v", err)
	}
	if len(messageHits) != 1 || messageHits[0].SourceRole != "attachment" || messageHits[0].Scope != "attachments" {
		t.Fatalf("attachment hits = %+v, want labeled attachment hit", messageHits)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"messages", "--conversation", conversationID, "--around", messageID, "--context", "0", "--json"}); err != nil {
		t.Fatalf("messages around: %v", err)
	}
	var messages []archive.MessageRow
	if err := json.Unmarshal(stdout.Bytes(), &messages); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	if len(messages) != 1 || messages[0].ID != messageID {
		t.Fatalf("messages around = %+v, want only selected message", messages)
	}
	stdout.Reset()

	outDir := filepath.Join(home, "single-export")
	if err := cli.Run(context.Background(), []string{"export", "markdown", "--out", outDir, "--conversation", conversationID, "--json"}); err != nil {
		t.Fatalf("export single conversation: %v", err)
	}
	var exported map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &exported); err != nil {
		t.Fatalf("decode export result: %v", err)
	}
	if exported["conversations"] != float64(1) {
		t.Fatalf("export result = %+v, want one exported conversation", exported)
	}
}

func TestSyncWebDryRunReportsContractAndRedactsCapture(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	capturePath := filepath.Join(t.TempDir(), "chatgpt-capture.json")
	capture := `{
		"log": {
			"entries": [
				{
					"request": {
						"method": "GET",
						"url": "https://chatgpt.com/backend-api/conversations?offset=0&access_token=secret"
					},
					"response": {"status": 200}
				},
				{
					"request": {
						"method": "GET",
						"url": "https://chatgpt.com/backend-api/conversation/abc?access_token=secret"
					},
					"response": {"status": 200}
				}
			]
		}
	}`
	if err := os.WriteFile(capturePath, []byte(capture), 0o600); err != nil {
		t.Fatalf("write capture: %v", err)
	}

	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	err := cli.Run(context.Background(), []string{
		"sync", "web",
		"--provider", "chatgpt",
		"--profile", filepath.Join(home, "missing-profile"),
		"--capture", capturePath,
		"--dry-run",
		"--json",
	})
	if err != nil {
		t.Fatalf("sync web dry-run: %v", err)
	}
	if strings.Contains(stdout.String(), "access_token") || strings.Contains(stdout.String(), "secret") || strings.Contains(stdout.String(), "/conversation/abc") {
		t.Fatalf("sync web output leaked capture query material: %s", stdout.String())
	}
	var report struct {
		Provider              string `json:"provider"`
		SourceKind            string `json:"source_kind"`
		AuthState             string `json:"auth_state"`
		EndpointContractState string `json:"endpoint_contract_state"`
		Source                struct {
			Conversations int `json:"conversations"`
			Messages      int `json:"messages"`
		} `json:"source"`
		Freshness struct {
			State string `json:"state"`
		} `json:"freshness"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode sync web report: %v", err)
	}
	if report.Provider != "chatgpt" || report.SourceKind != "chatgpt_web" {
		t.Fatalf("provider/source kind = %s/%s, want chatgpt/chatgpt_web", report.Provider, report.SourceKind)
	}
	if report.AuthState != "login_required" {
		t.Fatalf("auth state = %q, want login_required", report.AuthState)
	}
	if report.EndpointContractState != "matched" {
		t.Fatalf("contract state = %q, want matched", report.EndpointContractState)
	}
	if report.Freshness.State != "archive_missing" {
		t.Fatalf("freshness state = %q, want archive_missing", report.Freshness.State)
	}
}

func TestSyncWebStaleContractBlocksWrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	capturePath := filepath.Join(t.TempDir(), "stale-chatgpt-capture.json")
	if err := os.WriteFile(capturePath, []byte(`[{"request":{"url":"https://chatgpt.com/api/changed-shape","method":"GET"}}]`), 0o600); err != nil {
		t.Fatalf("write capture: %v", err)
	}
	sourcePath := filepath.Join("..", "..", "testdata", "redacted", "chatgpt-web-conversation.fixture.json")
	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	err := cli.Run(context.Background(), []string{
		"sync", "web",
		"--provider", "chatgpt",
		"--source", sourcePath,
		"--capture", capturePath,
		"--json",
	})
	if err == nil {
		t.Fatalf("sync web stale contract succeeded, want contract_stale error")
	}
	if !strings.Contains(err.Error(), "contract_stale") {
		t.Fatalf("error = %q, want contract_stale", err.Error())
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"status", "--json"}); err != nil {
		t.Fatalf("status after blocked stale contract: %v", err)
	}
	var status struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.State != "uninitialized" {
		t.Fatalf("status after blocked stale contract = %q, want uninitialized", status.State)
	}
}

func TestSyncWebSourceImportsSearchablePayloadAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	fixturePath := filepath.Join("..", "..", "testdata", "redacted", "chatgpt-web-conversation.fixture.json")
	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{"sync", "web", "--provider", "chatgpt", "--source", fixturePath, "--json"}); err != nil {
		t.Fatalf("sync web source: %v", err)
	}
	var stats archive.ImportStats
	if err := json.Unmarshal(stdout.Bytes(), &stats); err != nil {
		t.Fatalf("decode sync stats: %v", err)
	}
	if stats.Provider != "chatgpt" || stats.SourceKind != "chatgpt_web" {
		t.Fatalf("sync identity = %s/%s, want chatgpt/chatgpt_web", stats.Provider, stats.SourceKind)
	}
	if stats.Conversations != 1 || stats.Messages != 3 {
		t.Fatalf("sync counts = %d/%d, want 1 conversation and 3 messages", stats.Conversations, stats.Messages)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"search", "web sync chatgpt fixture assistant phrase", "--provider", "chatgpt", "--json"}); err != nil {
		t.Fatalf("search web synced content: %v", err)
	}
	var hits []archive.SearchHit
	if err := json.Unmarshal(stdout.Bytes(), &hits); err != nil {
		t.Fatalf("decode search hits: %v", err)
	}
	if len(hits) != 1 || hits[0].Provider != "chatgpt" {
		t.Fatalf("hits = %+v, want one ChatGPT web synced hit", hits)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"sync", "web", "--provider", "chatgpt", "--source", fixturePath, "--json"}); err != nil {
		t.Fatalf("repeat sync web source: %v", err)
	}
	if err := json.Unmarshal(stdout.Bytes(), &stats); err != nil {
		t.Fatalf("decode repeat sync stats: %v", err)
	}
	if !stats.AlreadyImported {
		t.Fatalf("repeat sync was not idempotent: %+v", stats)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"sync", "web", "--provider", "chatgpt", "--source", fixturePath, "--dry-run", "--json"}); err != nil {
		t.Fatalf("sync web dry-run source: %v", err)
	}
	var report struct {
		Freshness struct {
			State        string `json:"state"`
			LastImportAt string `json:"last_import_at"`
		} `json:"freshness"`
		Source struct {
			Conversations int `json:"conversations"`
			Messages      int `json:"messages"`
		} `json:"source"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode dry-run source report: %v", err)
	}
	if report.Freshness.State != "seen" || report.Freshness.LastImportAt == "" {
		t.Fatalf("freshness = %+v, want seen with last import", report.Freshness)
	}
	if report.Source.Conversations != 1 || report.Source.Messages != 3 {
		t.Fatalf("source counts = %+v, want 1/3", report.Source)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"status", "--json"}); err != nil {
		t.Fatalf("status after web sync: %v", err)
	}
	var status struct {
		State   string `json:"state"`
		WebSync []struct {
			Provider          string `json:"provider"`
			SourceKind        string `json:"source_kind"`
			State             string `json:"state"`
			LastImportAt      string `json:"last_import_at"`
			ConversationCount int64  `json:"conversation_count"`
			MessageCount      int64  `json:"message_count"`
		} `json:"web_sync"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("decode status web sync: %v", err)
	}
	if status.State != "ok" {
		t.Fatalf("status state = %q, want ok", status.State)
	}
	var chatgptStatus *struct {
		Provider          string `json:"provider"`
		SourceKind        string `json:"source_kind"`
		State             string `json:"state"`
		LastImportAt      string `json:"last_import_at"`
		ConversationCount int64  `json:"conversation_count"`
		MessageCount      int64  `json:"message_count"`
	}
	for i := range status.WebSync {
		if status.WebSync[i].Provider == "chatgpt" {
			chatgptStatus = &status.WebSync[i]
			break
		}
	}
	if chatgptStatus == nil {
		t.Fatalf("status web_sync missing chatgpt: %+v", status.WebSync)
	}
	if chatgptStatus.SourceKind != "chatgpt_web" || chatgptStatus.State != "seen" || chatgptStatus.LastImportAt == "" {
		t.Fatalf("chatgpt web status = %+v, want seen freshness", *chatgptStatus)
	}
	if chatgptStatus.ConversationCount != 1 || chatgptStatus.MessageCount != 3 {
		t.Fatalf("chatgpt web counts = %+v, want 1/3", *chatgptStatus)
	}
}

func TestImportLocalTranscriptSourcesAreSearchable(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		fixture  string
		query    string
	}{
		{
			name:     "openclaw",
			provider: "openclaw",
			fixture:  "openclaw-session.fixture.jsonl",
			query:    "openclaw jsonl fixture assistant phrase",
		},
		{
			name:     "codex",
			provider: "codex",
			fixture:  "codex-session.fixture.jsonl",
			query:    "codex jsonl fixture assistant phrase",
		},
		{
			name:     "gemini",
			provider: "gemini",
			fixture:  "gemini-session.fixture.json",
			query:    "gemini cli fixture assistant phrase",
		},
		{
			name:     "claude-code",
			provider: "claude-code",
			fixture:  "claude-code-session.fixture.jsonl",
			query:    "claude code jsonl fixture assistant phrase",
		},
		{
			name:     "hermes",
			provider: "hermes",
			fixture:  "hermes-session.fixture.json",
			query:    "hermes json fixture assistant phrase",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

			var stdout bytes.Buffer
			cli := New()
			cli.stdout = &stdout
			fixturePath := filepath.Join("..", "..", "testdata", "redacted", tt.fixture)
			if err := cli.Run(context.Background(), []string{"import", fixturePath, "--provider", tt.provider, "--json"}); err != nil {
				t.Fatalf("import %s fixture: %v", tt.provider, err)
			}
			var stats archive.ImportStats
			if err := json.Unmarshal(stdout.Bytes(), &stats); err != nil {
				t.Fatalf("decode import stats: %v", err)
			}
			if stats.Provider != tt.provider || stats.Conversations != 1 || stats.Messages == 0 {
				t.Fatalf("stats = %+v, want one %s conversation with messages", stats, tt.provider)
			}
			stdout.Reset()

			if err := cli.Run(context.Background(), []string{"search", tt.query, "--provider", tt.provider, "--json"}); err != nil {
				t.Fatalf("search %s fixture: %v", tt.provider, err)
			}
			var hits []archive.SearchHit
			if err := json.Unmarshal(stdout.Bytes(), &hits); err != nil {
				t.Fatalf("decode search hits: %v", err)
			}
			if len(hits) != 1 || hits[0].Provider != tt.provider {
				t.Fatalf("hits = %+v, want one %s hit", hits, tt.provider)
			}
		})
	}
}

func TestImportCursorStoreSourceIsSearchable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	storePath := filepath.Join(t.TempDir(), "workspace", "cursor-fixture-session", "store.db")
	writeCursorStoreFixture(t, storePath)

	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{"import", storePath, "--provider", "cursor", "--json"}); err != nil {
		t.Fatalf("import cursor store fixture: %v", err)
	}
	var stats archive.ImportStats
	if err := json.Unmarshal(stdout.Bytes(), &stats); err != nil {
		t.Fatalf("decode import stats: %v", err)
	}
	if stats.Provider != "cursor" || stats.SourceKind != "cursor_store" || stats.Conversations != 1 || stats.Messages != 2 {
		t.Fatalf("stats = %+v, want one cursor conversation with two visible messages", stats)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"search", "cursor store app fixture assistant phrase", "--provider", "cursor", "--json"}); err != nil {
		t.Fatalf("search cursor fixture: %v", err)
	}
	var hits []archive.SearchHit
	if err := json.Unmarshal(stdout.Bytes(), &hits); err != nil {
		t.Fatalf("decode search hits: %v", err)
	}
	if len(hits) != 1 || hits[0].Provider != "cursor" {
		t.Fatalf("hits = %+v, want one cursor hit", hits)
	}
}

func TestImportCursorStateStoreSourceIsSearchable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	storePath := filepath.Join(t.TempDir(), "workspace-hash", "state.vscdb")
	writeCursorStateStoreFixture(t, storePath)

	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{"import", storePath, "--provider", "cursor", "--json"}); err != nil {
		t.Fatalf("import cursor state fixture: %v", err)
	}
	var stats archive.ImportStats
	if err := json.Unmarshal(stdout.Bytes(), &stats); err != nil {
		t.Fatalf("decode import stats: %v", err)
	}
	if stats.Provider != "cursor" || stats.SourceKind != "cursor_store" || stats.Conversations != 1 || stats.Messages != 2 {
		t.Fatalf("stats = %+v, want one cursor state conversation with two visible messages", stats)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"search", "cursor state app fixture assistant phrase", "--provider", "cursor", "--json"}); err != nil {
		t.Fatalf("search cursor state fixture: %v", err)
	}
	var hits []archive.SearchHit
	if err := json.Unmarshal(stdout.Bytes(), &hits); err != nil {
		t.Fatalf("decode search hits: %v", err)
	}
	if len(hits) != 1 || hits[0].Provider != "cursor" {
		t.Fatalf("hits = %+v, want one cursor hit", hits)
	}
}

func TestImportHermesStateStoreSourceIsSearchable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	storePath := filepath.Join(t.TempDir(), ".hermes", "state.db")
	writeHermesStoreFixture(t, storePath)

	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{"import", storePath, "--provider", "hermes", "--json"}); err != nil {
		t.Fatalf("import hermes store fixture: %v", err)
	}
	var stats archive.ImportStats
	if err := json.Unmarshal(stdout.Bytes(), &stats); err != nil {
		t.Fatalf("decode import stats: %v", err)
	}
	if stats.Provider != "hermes" || stats.SourceKind != "hermes_session" || stats.Conversations != 1 || stats.Messages != 2 {
		t.Fatalf("stats = %+v, want one Hermes conversation with two visible messages", stats)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"search", "hermes store app fixture assistant phrase", "--provider", "hermes", "--json"}); err != nil {
		t.Fatalf("search hermes fixture: %v", err)
	}
	var hits []archive.SearchHit
	if err := json.Unmarshal(stdout.Bytes(), &hits); err != nil {
		t.Fatalf("decode search hits: %v", err)
	}
	if len(hits) != 1 || hits[0].Provider != "hermes" {
		t.Fatalf("hits = %+v, want one Hermes hit", hits)
	}
}

func TestScheduleLaunchdWritesSyncPlist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	outPath := filepath.Join(home, "LaunchAgents", "aicrawl-chatgpt.plist")
	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{
		"schedule", "launchd",
		"--provider", "chatgpt",
		"--cdp-url", "http://127.0.0.1:9222",
		"--interval-minutes", "7",
		"--max-conversations", "9",
		"--aicrawl-bin", "/usr/local/bin/aicrawl",
		"--out", outPath,
		"--json",
	}); err != nil {
		t.Fatalf("schedule launchd: %v", err)
	}
	var result launchdResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode launchd result: %v", err)
	}
	if result.Provider != "chatgpt" || result.IntervalSeconds != 420 || result.MaxConversations != 9 {
		t.Fatalf("result = %+v", result)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read plist: %v", err)
	}
	plist := string(data)
	for _, want := range []string{
		"<key>ProgramArguments</key>",
		"<string>/usr/local/bin/aicrawl</string>",
		"<string>sync</string>",
		"<string>web</string>",
		"<string>--provider</string>",
		"<string>chatgpt</string>",
		"<string>--cdp-url</string>",
		"<string>http://127.0.0.1:9222</string>",
		"<key>StartInterval</key>",
		"<integer>420</integer>",
	} {
		if !strings.Contains(plist, want) {
			t.Fatalf("plist missing %q:\n%s", want, plist)
		}
	}
	if strings.Contains(plist, "token") || strings.Contains(plist, "Authorization") || strings.Contains(plist, "<key>Program</key>") {
		t.Fatalf("plist contains disallowed auth/shell material:\n%s", plist)
	}
}

func TestScheduleLaunchdWritesProfileLaunchPlist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	outPath := filepath.Join(home, "LaunchAgents", "aicrawl-claude.plist")
	profilePath := filepath.Join(home, "profiles", "claude")
	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{
		"schedule", "launchd",
		"--provider", "claude",
		"--profile", profilePath,
		"--browser", "/Applications/Chromium.app/Contents/MacOS/Chromium",
		"--remote-debugging-port", "0",
		"--out", outPath,
		"--json",
	}); err != nil {
		t.Fatalf("schedule launchd profile: %v", err)
	}
	var result launchdResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode launchd profile result: %v", err)
	}
	if result.Provider != "claude" || result.CDPURL != "" || result.ProfilePath != profilePath {
		t.Fatalf("result = %+v", result)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read plist: %v", err)
	}
	plist := string(data)
	for _, want := range []string{
		"<string>sync</string>",
		"<string>web</string>",
		"<string>--provider</string>",
		"<string>claude</string>",
		"<string>--profile</string>",
		"<string>" + profilePath + "</string>",
		"<string>--browser</string>",
		"<string>/Applications/Chromium.app/Contents/MacOS/Chromium</string>",
		"<string>--remote-debugging-port</string>",
		"<string>0</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Fatalf("plist missing %q:\n%s", want, plist)
		}
	}
	if strings.Contains(plist, "--cdp-url") || strings.Contains(plist, "token") || strings.Contains(plist, "Authorization") {
		t.Fatalf("profile plist contains disallowed material:\n%s", plist)
	}
}

func TestSyncWebLiveCDPImportsSearchableChatGPTPayload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	server := newFakeChatGPTCDPServer(t)
	defer server.Close()

	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{
		"sync", "web",
		"--provider", "chatgpt",
		"--cdp-url", server.URL,
		"--max-conversations", "1",
		"--json",
	}); err != nil {
		t.Fatalf("sync web live CDP: %v", err)
	}
	var stats archive.ImportStats
	if err := json.Unmarshal(stdout.Bytes(), &stats); err != nil {
		t.Fatalf("decode sync stats: %v", err)
	}
	if stats.Provider != "chatgpt" || stats.SourceKind != "chatgpt_web" || stats.Conversations != 1 || stats.Messages != 1 {
		t.Fatalf("stats = %+v, want one live chatgpt conversation/message", stats)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"search", "live cdp chatgpt assistant phrase", "--provider", "chatgpt", "--json"}); err != nil {
		t.Fatalf("search live CDP import: %v", err)
	}
	var hits []archive.SearchHit
	if err := json.Unmarshal(stdout.Bytes(), &hits); err != nil {
		t.Fatalf("decode search hits: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %+v, want one live CDP hit", hits)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{
		"sync", "web",
		"--provider", "chatgpt",
		"--cdp-url", server.URL,
		"--max-conversations", "1",
		"--json",
	}); err != nil {
		t.Fatalf("repeat sync web live CDP: %v", err)
	}
	if err := json.Unmarshal(stdout.Bytes(), &stats); err != nil {
		t.Fatalf("decode repeat sync stats: %v", err)
	}
	if stats.Conversations != 0 || stats.Messages != 0 || stats.SourceKind != "chatgpt_web" {
		t.Fatalf("repeat live sync stats = %+v, want no-change sync", stats)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"status", "--json"}); err != nil {
		t.Fatalf("status after live CDP import: %v", err)
	}
	var status struct {
		WebSync []struct {
			Provider       string `json:"provider"`
			CursorKind     string `json:"cursor_kind"`
			CursorAt       string `json:"cursor_at"`
			LastCheckedAt  string `json:"last_checked_at"`
			CandidateCount int64  `json:"candidate_count"`
		} `json:"web_sync"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("decode live status: %v", err)
	}
	var found bool
	for _, state := range status.WebSync {
		if state.Provider != "chatgpt" {
			continue
		}
		found = true
		if state.CursorKind != "provider_updated_at" || state.CursorAt == "" || state.LastCheckedAt == "" || state.CandidateCount != 1 {
			t.Fatalf("chatgpt live sync state = %+v, want provider cursor", state)
		}
	}
	if !found {
		t.Fatalf("status missing chatgpt web sync state: %+v", status.WebSync)
	}
}

func TestSyncWebLiveCDPRecordsInaccessibleDetailStatus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	dataHome := filepath.Join(home, ".local", "share")
	t.Setenv("XDG_DATA_HOME", dataHome)

	server := newFakeChatGPTCDPServerWithMissingDetail(t)
	defer server.Close()

	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{
		"sync", "web",
		"--provider", "chatgpt",
		"--cdp-url", server.URL,
		"--max-conversations", "2",
		"--json",
	}); err != nil {
		t.Fatalf("sync web live CDP with inaccessible detail: %v", err)
	}
	var stats archive.ImportStats
	if err := json.Unmarshal(stdout.Bytes(), &stats); err != nil {
		t.Fatalf("decode sync stats: %v", err)
	}
	if stats.Conversations != 1 || len(stats.Warnings) != 1 {
		t.Fatalf("stats = %+v, want one imported conversation and one bounded warning", stats)
	}

	db, err := sql.Open("sqlite", filepath.Join(dataHome, "aicrawl", "aicrawl.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	rows, err := db.QueryContext(context.Background(), `select raw_id, status, coalesce(http_status, 0), coalesce(last_seen_at, ''), coalesce(last_checked_at, '') from conversation_sync_status order by raw_id`)
	if err != nil {
		t.Fatalf("query sync statuses: %v", err)
	}
	defer rows.Close()
	statuses := map[string]struct {
		status        string
		http          int
		lastSeenAt    string
		lastCheckedAt string
	}{}
	for rows.Next() {
		var rawID string
		var status string
		var httpStatus int
		var lastSeenAt string
		var lastCheckedAt string
		if err := rows.Scan(&rawID, &status, &httpStatus, &lastSeenAt, &lastCheckedAt); err != nil {
			t.Fatalf("scan sync status: %v", err)
		}
		statuses[rawID] = struct {
			status        string
			http          int
			lastSeenAt    string
			lastCheckedAt string
		}{status: status, http: httpStatus, lastSeenAt: lastSeenAt, lastCheckedAt: lastCheckedAt}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("sync status rows: %v", err)
	}
	if got := statuses["live-cdp-ok"]; got.status != "seen" || got.http != 0 {
		t.Fatalf("seen status = %+v, statuses = %+v", got, statuses)
	}
	if got := statuses["live-cdp-missing"]; got.status != "inaccessible" || got.http != 404 {
		t.Fatalf("inaccessible status = %+v, statuses = %+v", got, statuses)
	}
	if statuses["live-cdp-ok"].lastSeenAt == "" || statuses["live-cdp-ok"].lastCheckedAt == "" || statuses["live-cdp-missing"].lastSeenAt == "" || statuses["live-cdp-missing"].lastCheckedAt == "" {
		t.Fatalf("sync status timestamps = %+v, want last_seen_at and last_checked_at for both rows", statuses)
	}
}

func TestSyncWebDryRunCDPReportsLiveListCandidatesWithoutCreatingArchive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	server := newFakeChatGPTCDPServer(t)
	defer server.Close()

	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{
		"sync", "web",
		"--provider", "chatgpt",
		"--cdp-url", server.URL,
		"--max-conversations", "1",
		"--dry-run",
		"--json",
	}); err != nil {
		t.Fatalf("sync web dry-run CDP: %v", err)
	}
	var report websync.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode dry-run CDP report: %v", err)
	}
	if report.Source == nil {
		t.Fatalf("dry-run report missing live source stats: %+v", report)
	}
	if report.Source.Kind != "live_list" || report.Source.Conversations != 1 || report.Source.Messages != 0 {
		t.Fatalf("source stats = %+v, want one list candidate and no detail messages", *report.Source)
	}
	stdout.Reset()

	if err := cli.Run(context.Background(), []string{"status", "--json"}); err != nil {
		t.Fatalf("status after live dry-run: %v", err)
	}
	var status struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.State != "uninitialized" {
		t.Fatalf("status after live dry-run = %q, want uninitialized", status.State)
	}
}

func TestSyncWebDryRunCDPReportsUnavailableLiveListAsWarning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/list" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"type": "page",
			"url":  "https://example.invalid/",
		}})
	}))
	defer server.Close()

	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{
		"sync", "web",
		"--provider", "chatgpt",
		"--cdp-url", server.URL,
		"--dry-run",
		"--json",
	}); err != nil {
		t.Fatalf("sync web dry-run CDP without provider page: %v", err)
	}
	var report websync.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode dry-run unavailable report: %v", err)
	}
	if report.Source == nil || report.Source.Kind != "live_list_unavailable" {
		t.Fatalf("source stats = %+v, want unavailable live list", report.Source)
	}
	if len(report.Warnings) == 0 || !strings.Contains(strings.Join(report.Warnings, "\n"), "candidate inspection unavailable") {
		t.Fatalf("warnings = %+v, want candidate inspection warning", report.Warnings)
	}
}

func TestSyncWebLaunchesMissingProfileAndReportsLoginRequired(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"Browser":"fake"}`))
	}))
	defer server.Close()
	port := strings.TrimPrefix(server.URL, "http://127.0.0.1:")
	fakeBrowser := writeFakeBrowser(t, port)
	profilePath := filepath.Join(home, "profiles", "chatgpt")

	var stdout bytes.Buffer
	cli := New()
	cli.stdout = &stdout
	if err := cli.Run(context.Background(), []string{
		"sync", "web",
		"--provider", "chatgpt",
		"--profile", profilePath,
		"--browser", fakeBrowser,
		"--json",
	}); err != nil {
		t.Fatalf("sync web first-run launch: %v", err)
	}
	var report websync.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode first-run report: %v", err)
	}
	if report.AuthState != "login_required" {
		t.Fatalf("auth state = %q, want login_required", report.AuthState)
	}
	if !report.Session.Launched || report.Session.CDPURL != server.URL {
		t.Fatalf("session launch = %+v, want launched fake CDP endpoint", report.Session)
	}
	if report.Session.BrowserPath != fakeBrowser {
		t.Fatalf("browser path = %q, want %q", report.Session.BrowserPath, fakeBrowser)
	}
	if !strings.Contains(strings.Join(report.Warnings, "\n"), "log in") {
		t.Fatalf("warnings = %+v, want login guidance", report.Warnings)
	}
}

func newFakeChatGPTCDPServer(t *testing.T) *httptest.Server {
	t.Helper()
	var wsURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/list":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"type":                 "page",
				"url":                  "https://chatgpt.com/",
				"webSocketDebuggerUrl": wsURL,
			}})
		case "/devtools/page/1":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "")
			for {
				_, data, err := conn.Read(context.Background())
				if err != nil {
					return
				}
				var cmd struct {
					ID     int            `json:"id"`
					Method string         `json:"method"`
					Params map[string]any `json:"params"`
				}
				if err := json.Unmarshal(data, &cmd); err != nil {
					t.Errorf("decode command: %v", err)
					return
				}
				expression, _ := cmd.Params["expression"].(string)
				body := `{}`
				status := 200
				switch {
				case strings.Contains(expression, "/backend-api/conversations?"):
					body = `{"items":[{"id":"live-cdp-chatgpt","update_time":1760000060}]}`
				case strings.Contains(expression, "/backend-api/conversation/live-cdp-chatgpt"):
					body = `{
  "id": "live-cdp-chatgpt",
  "title": "Live CDP ChatGPT",
  "update_time": 1760000060,
  "mapping": {
    "assistant": {
      "id": "assistant",
      "parent": null,
      "children": [],
      "message": {
        "author": {"role": "assistant"},
        "content": {"parts": ["live cdp chatgpt assistant phrase"]}
      }
    }
  }
}`
				default:
					status = 404
				}
				response := map[string]any{
					"id": cmd.ID,
					"result": map[string]any{
						"result": map[string]any{
							"type": "object",
							"value": map[string]any{
								"status": status,
								"url":    "https://chatgpt.com/synthetic",
								"text":   body,
							},
						},
					},
				}
				data, _ = json.Marshal(response)
				if err := conn.Write(context.Background(), websocket.MessageText, data); err != nil {
					return
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	wsURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/page/1"
	return server
}

func newFakeChatGPTCDPServerWithMissingDetail(t *testing.T) *httptest.Server {
	t.Helper()
	var wsURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/list":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"type":                 "page",
				"url":                  "https://chatgpt.com/",
				"webSocketDebuggerUrl": wsURL,
			}})
		case "/devtools/page/1":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "")
			for {
				_, data, err := conn.Read(context.Background())
				if err != nil {
					return
				}
				var cmd struct {
					ID     int            `json:"id"`
					Method string         `json:"method"`
					Params map[string]any `json:"params"`
				}
				if err := json.Unmarshal(data, &cmd); err != nil {
					t.Errorf("decode command: %v", err)
					return
				}
				expression, _ := cmd.Params["expression"].(string)
				body := `{}`
				status := 200
				switch {
				case strings.Contains(expression, "/backend-api/conversations?"):
					body = `{"items":[{"id":"live-cdp-ok","update_time":1760000060},{"id":"live-cdp-missing","update_time":1760000050}]}`
				case strings.Contains(expression, "/backend-api/conversation/live-cdp-ok"):
					body = `{
  "id": "live-cdp-ok",
  "title": "Live CDP OK",
  "update_time": 1760000060,
  "mapping": {
    "assistant": {
      "id": "assistant",
      "parent": null,
      "children": [],
      "message": {
        "author": {"role": "assistant"},
        "content": {"parts": ["live cdp accessible assistant phrase"]}
      }
    }
  }
}`
				case strings.Contains(expression, "/backend-api/conversation/live-cdp-missing"):
					status = 404
					body = `{"error":"missing"}`
				default:
					status = 404
				}
				response := map[string]any{
					"id": cmd.ID,
					"result": map[string]any{
						"result": map[string]any{
							"type": "object",
							"value": map[string]any{
								"status": status,
								"url":    "https://chatgpt.com/synthetic",
								"text":   body,
							},
						},
					},
				}
				data, _ = json.Marshal(response)
				if err := conn.Write(context.Background(), websocket.MessageText, data); err != nil {
					return
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	wsURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/page/1"
	return server
}

func writeFakeBrowser(t *testing.T, port string) string {
	t.Helper()
	browserPath := filepath.Join(t.TempDir(), "fake-browser")
	script := `#!/bin/sh
profile=""
for arg in "$@"; do
  case "$arg" in
    --user-data-dir=*) profile="${arg#--user-data-dir=}" ;;
  esac
done
mkdir -p "$profile"
printf "%s\n/devtools/browser/fake\n" "$FAKE_CDP_PORT" > "$profile/DevToolsActivePort"
sleep 2
`
	if err := os.WriteFile(browserPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake browser: %v", err)
	}
	t.Setenv("FAKE_CDP_PORT", port)
	return browserPath
}

func copyFixture(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		t.Fatalf("create fixture dst dir: %v", err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatalf("write fixture copy: %v", err)
	}
}

func writeCursorStoreFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create cursor fixture dir: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open cursor fixture db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`create table blobs (id text primary key, data blob); create table meta (key text primary key, value text);`); err != nil {
		t.Fatalf("create cursor fixture schema: %v", err)
	}
	meta := `{"agentId":"cursor-fixture-session","createdAt":"2026-06-07T10:00:00Z","name":"Cursor app fixture"}`
	if _, err := db.Exec(`insert into meta(key, value) values('0', ?)`, hex.EncodeToString([]byte(meta))); err != nil {
		t.Fatalf("insert cursor fixture meta: %v", err)
	}
	rows := []struct {
		id   string
		data []byte
	}{
		{"binary-index", []byte{0x00, 0x01, 0x02}},
		{"user-1", []byte(`{"role":"user","content":"cursor store app fixture user phrase","id":"user-1"}`)},
		{"assistant-1", []byte(`{"role":"assistant","content":[{"type":"text","text":"cursor store app fixture assistant phrase"},{"type":"tool-call","input":{"command":"ignored"}}],"id":"assistant-1"}`)},
		{"tool-1", []byte(`{"role":"tool","content":[{"type":"tool-result","content":"private tool output that should not be indexed"}],"id":"tool-1"}`)},
	}
	for _, row := range rows {
		if _, err := db.Exec(`insert into blobs(id, data) values(?, ?)`, row.id, row.data); err != nil {
			t.Fatalf("insert cursor fixture blob: %v", err)
		}
	}
}

func writeCursorStateStoreFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create cursor state fixture dir: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open cursor state fixture db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`create table ItemTable (key text, value blob); create table cursorDiskKV (key text, value blob);`); err != nil {
		t.Fatalf("create cursor state fixture schema: %v", err)
	}
	if _, err := db.Exec(`insert into ItemTable(key, value) values('composer.composerData', ?)`, []byte(`{"selectedComposerIds":["workspace-hash"]}`)); err != nil {
		t.Fatalf("insert cursor state metadata: %v", err)
	}
	rows := []struct {
		id   string
		data []byte
	}{
		{"user-1", []byte(`{"role":"user","content":"cursor state app fixture user phrase","id":"user-1"}`)},
		{"assistant-1", []byte(`{"role":"assistant","content":[{"type":"text","text":"cursor state app fixture assistant phrase"},{"type":"tool-call","input":{"command":"ignored"}}],"id":"assistant-1"}`)},
		{"tool-1", []byte(`{"role":"tool","content":[{"type":"tool-result","content":"private tool output that should not be indexed"}],"id":"tool-1"}`)},
	}
	if _, err := db.Exec(`insert into cursorDiskKV(key, value) values('composer.content.package-json', ?)`, []byte(hex.EncodeToString([]byte(`{"name":"not a message"}`)))); err != nil {
		t.Fatalf("insert cursor state non-message blob: %v", err)
	}
	for _, row := range rows {
		if _, err := db.Exec(`insert into cursorDiskKV(key, value) values(?, ?)`, "agentKv:blob:"+row.id, []byte(hex.EncodeToString(row.data))); err != nil {
			t.Fatalf("insert cursor state fixture blob: %v", err)
		}
	}
}

func writeHermesStoreFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create hermes fixture dir: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open hermes fixture db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		create table sessions (
			id text primary key,
			source text,
			user_id text,
			model text,
			model_config text,
			system_prompt text,
			parent_session_id text,
			started_at real,
			ended_at real,
			end_reason text,
			message_count integer,
			tool_call_count integer,
			input_tokens integer,
			output_tokens integer,
			cache_read_tokens integer,
			cache_write_tokens integer,
			reasoning_tokens integer,
			billing_provider text,
			billing_base_url text,
			billing_mode text,
			estimated_cost_usd real,
			actual_cost_usd real,
			cost_status text,
			cost_source text,
			pricing_version text,
			title text
		);
		create table messages (
			id integer primary key,
			session_id text,
			role text,
			content text,
			tool_call_id text,
			tool_calls text,
			tool_name text,
			timestamp real,
			token_count integer,
			finish_reason text,
			reasoning text,
			reasoning_details text,
			codex_reasoning_items text
		);
	`); err != nil {
		t.Fatalf("create hermes fixture schema: %v", err)
	}
	if _, err := db.Exec(`insert into sessions (
		id, source, model, model_config, system_prompt, started_at, ended_at,
		end_reason, message_count, tool_call_count, input_tokens, output_tokens, title
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"hermes-store-session", "telegram", "redacted-model", "{}", "redacted system prompt",
		1760000000.0, 1760000060.0, "stop", 4, 0, 10, 20, "Hermes store app fixture",
	); err != nil {
		t.Fatalf("insert hermes fixture session: %v", err)
	}
	rows := []struct {
		role    string
		content string
		ts      float64
	}{
		{"session_meta", "metadata that should not be indexed", 1760000000.0},
		{"user", "hermes store app fixture user phrase", 1760000001.0},
		{"assistant", "hermes store app fixture assistant phrase", 1760000002.0},
		{"tool", "", 1760000003.0},
	}
	for _, row := range rows {
		if _, err := db.Exec(`insert into messages(session_id, role, content, timestamp) values(?, ?, ?, ?)`, "hermes-store-session", row.role, row.content, row.ts); err != nil {
			t.Fatalf("insert hermes fixture message: %v", err)
		}
	}
}

func writeLargeChatGPTFixture(t *testing.T, path string, conversations int) {
	t.Helper()
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < conversations; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{
			"id":"stream-conv-%03d",
			"title":"Stream Conversation %03d",
			"current_node":"msg",
			"mapping":{
				"root":{"id":"root","parent":null,"children":["msg"],"message":null},
				"msg":{
					"id":"msg",
					"parent":"root",
					"children":[],
					"message":{"author":{"role":"user"},"content":{"parts":["streamed fixture phrase %03d"]}}
				}
			}
		}`, i, i, i)
	}
	b.WriteByte(']')
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write large fixture: %v", err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestInvalidFilterOptionsReturnUsageErrors(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "exported")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "conversations provider",
			args: []string{"conversations", "--provider", "chatgptt"},
			want: "--provider",
		},
		{
			name: "search provider",
			args: []string{"search", "known", "--provider", "claudee"},
			want: "--provider",
		},
		{
			name: "export provider",
			args: []string{"export", "markdown", "--out", outDir, "--provider", "openai"},
			want: "--provider",
		},
		{
			name: "import provider",
			args: []string{"import", "source.fixture.json", "--provider", "openai"},
			want: "--provider",
		},
		{
			name: "sync web provider",
			args: []string{"sync", "web", "--provider", "gemini", "--dry-run"},
			want: "--provider",
		},
		{
			name: "schedule launchd provider",
			args: []string{"schedule", "launchd", "--provider", "gemini", "--cdp-url", "http://127.0.0.1:9222"},
			want: "--provider",
		},
		{
			name: "sync web remote debugging port",
			args: []string{"sync", "web", "--provider", "chatgpt", "--remote-debugging-port", "-1", "--dry-run"},
			want: "--remote-debugging-port",
		},
		{
			name: "conversations limit",
			args: []string{"conversations", "--limit", "0"},
			want: "--limit",
		},
		{
			name: "search limit",
			args: []string{"search", "known", "--limit", "many"},
			want: "--limit",
		},
		{
			name: "search group",
			args: []string{"search", "known", "--group", "threads"},
			want: "--group",
		},
		{
			name: "search scope",
			args: []string{"search", "known", "--scope", "raw"},
			want: "--scope",
		},
		{
			name: "search role",
			args: []string{"search", "known", "--role", "critic"},
			want: "--role",
		},
		{
			name: "search sort",
			args: []string{"search", "known", "--sort", "oldest"},
			want: "--sort",
		},
		{
			name: "messages context without around",
			args: []string{"messages", "--conversation", "c", "--context", "2"},
			want: "--around",
		},
		{
			name: "messages before negative",
			args: []string{"messages", "--conversation", "c", "--around", "m", "--before", "-1"},
			want: "--before",
		},
		{
			name: "export path",
			args: []string{"export", "markdown", "--out", outDir, "--path", "latest"},
			want: "--path",
		},
		{
			name: "export scope requires query",
			args: []string{"export", "markdown", "--out", outDir, "--scope", "internal"},
			want: "--query",
		},
		{
			name: "date bound",
			args: []string{"search", "known", "--since", "yesterday"},
			want: "--since",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cli := New()
			err := cli.Run(context.Background(), tt.args)
			if err == nil {
				t.Fatalf("Run returned nil error")
			}
			if got := ExitCode(err); got != 2 {
				t.Fatalf("exit code = %d, want 2; err = %v", got, err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want to mention %q", err.Error(), tt.want)
			}
		})
	}
}
