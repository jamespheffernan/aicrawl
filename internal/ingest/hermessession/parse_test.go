package hermessession

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openclaw/aicrawl/internal/archive"
	_ "modernc.org/sqlite"
)

func TestStreamFileParsesHermesSessionJSON(t *testing.T) {
	fixture := filepath.Join("..", "..", "..", "testdata", "redacted", "hermes-session.fixture.json")
	conversations, warnings := streamFixture(t, fixture)
	if len(conversations) != 1 {
		t.Fatalf("conversation count = %d, want 1", len(conversations))
	}
	conversation := conversations[0]
	if conversation.ID != "hermes:hermes-session-fixture" || conversation.Title != "Hermes telegram session hermes-s..." {
		t.Fatalf("conversation identity = %s/%s", conversation.ID, conversation.Title)
	}
	if len(conversation.Messages) != 3 {
		t.Fatalf("messages = %+v, want user, assistant, and tool messages", conversation.Messages)
	}
	if conversation.Messages[1].Role != "assistant" || conversation.Messages[1].Text != "hermes json fixture assistant phrase" {
		t.Fatalf("assistant message = %+v", conversation.Messages[1])
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "skipped 1 Hermes JSON messages") {
		t.Fatalf("warnings = %+v, want one skipped no-text warning", warnings)
	}
}

func TestStreamFileParsesHermesJSONL(t *testing.T) {
	fixture := filepath.Join("..", "..", "..", "testdata", "redacted", "hermes-session.fixture.jsonl")
	conversations, warnings := streamFixture(t, fixture)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %+v, want none", warnings)
	}
	if len(conversations) != 1 || len(conversations[0].Messages) != 2 {
		t.Fatalf("conversations = %+v, want one conversation with two messages", conversations)
	}
	if conversations[0].Title != "Hermes cli session hermes-s..." {
		t.Fatalf("title = %q", conversations[0].Title)
	}
	if conversations[0].Messages[1].Text != "hermes jsonl fixture assistant phrase" {
		t.Fatalf("assistant text = %q", conversations[0].Messages[1].Text)
	}
}

func TestStreamFileParsesHermesStateDB(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "state.db")
	writeHermesStoreFixture(t, fixture)
	conversations, warnings := streamFixture(t, fixture)
	if len(conversations) != 1 {
		t.Fatalf("conversation count = %d, want 1", len(conversations))
	}
	conversation := conversations[0]
	if conversation.ID != "hermes:hermes-store-session" || conversation.Title != "Hermes store fixture" {
		t.Fatalf("conversation identity = %s/%s", conversation.ID, conversation.Title)
	}
	if len(conversation.Messages) != 2 {
		t.Fatalf("messages = %+v, want two visible messages", conversation.Messages)
	}
	if conversation.Messages[0].Role != "user" || conversation.Messages[1].Role != "assistant" {
		t.Fatalf("message roles = %+v", conversation.Messages)
	}
	if conversation.Messages[1].Text != "hermes store fixture assistant phrase" {
		t.Fatalf("assistant text = %q", conversation.Messages[1].Text)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "skipped 1 Hermes store messages") {
		t.Fatalf("warnings = %+v, want one skipped no-text warning", warnings)
	}
}

func streamFixture(t *testing.T, path string) ([]archive.Conversation, []string) {
	t.Helper()
	var conversations []archive.Conversation
	var warnings []string
	parsed, err := StreamFile(path, func(conversation archive.Conversation, nextWarnings []string) error {
		conversations = append(conversations, conversation)
		warnings = append(warnings, nextWarnings...)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamFile: %v", err)
	}
	if parsed.Provider != Provider || parsed.SourceKind != SourceKind {
		t.Fatalf("parsed identity = %s/%s, want %s/%s", parsed.Provider, parsed.SourceKind, Provider, SourceKind)
	}
	return conversations, warnings
}

func writeHermesStoreFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create fixture dir: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
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
		t.Fatalf("create fixture schema: %v", err)
	}
	if _, err := db.Exec(`insert into sessions (
		id, source, model, model_config, system_prompt, started_at, ended_at,
		end_reason, message_count, tool_call_count, input_tokens, output_tokens, title
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"hermes-store-session", "telegram", "redacted-model", "{}", "redacted system prompt",
		1760000000.0, 1760000060.0, "stop", 4, 0, 10, 20, "Hermes store fixture",
	); err != nil {
		t.Fatalf("insert fixture session: %v", err)
	}
	rows := []struct {
		role    string
		content string
		ts      float64
	}{
		{"session_meta", "metadata that should not be indexed", 1760000000.0},
		{"user", "hermes store fixture user phrase", 1760000001.0},
		{"assistant", "hermes store fixture assistant phrase", 1760000002.0},
		{"tool", "", 1760000003.0},
	}
	for _, row := range rows {
		if _, err := db.Exec(`insert into messages(session_id, role, content, timestamp) values(?, ?, ?, ?)`, "hermes-store-session", row.role, row.content, row.ts); err != nil {
			t.Fatalf("insert fixture message: %v", err)
		}
	}
}
