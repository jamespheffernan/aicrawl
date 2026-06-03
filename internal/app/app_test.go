package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openclaw/aicrawl/internal/archive"
	"github.com/openclaw/aicrawl/internal/schema"
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
