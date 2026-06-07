package cursorstore

import (
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/openclaw/aicrawl/internal/archive"
	_ "modernc.org/sqlite"
)

func TestStreamFileParsesCursorStore(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "workspace", "cursor-fixture-session", "store.db")
	writeFixtureStore(t, fixture, []fixtureBlob{
		{id: "binary-index", data: []byte{0x00, 0x01, 0x02}},
		{id: "user-1", data: []byte(`{"role":"user","content":"cursor store fixture user phrase","id":"user-1"}`)},
		{id: "assistant-1", data: []byte(`{"role":"assistant","content":[{"type":"text","text":"cursor store fixture assistant phrase"},{"type":"tool-call","input":{"command":"ignored"}}],"id":"assistant-1"}`)},
		{id: "tool-1", data: []byte(`{"role":"tool","content":[{"type":"tool-result","content":"private tool output that should not be indexed"}],"id":"tool-1"}`)},
		{id: "system-1", data: []byte(`{"role":"system","content":"cursor store fixture system phrase","id":"system-1"}`)},
	})
	var conversations []archive.Conversation
	parsed, err := StreamFile(fixture, func(conversation archive.Conversation, warnings []string) error {
		conversations = append(conversations, conversation)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamFile: %v", err)
	}
	if parsed.Provider != Provider || parsed.SourceKind != SourceKind {
		t.Fatalf("parsed identity = %s/%s, want %s/%s", parsed.Provider, parsed.SourceKind, Provider, SourceKind)
	}
	if len(conversations) != 1 {
		t.Fatalf("conversation count = %d, want 1", len(conversations))
	}
	conversation := conversations[0]
	if conversation.ID != "cursor:cursor-fixture-session" || conversation.Title != "Cursor fixture chat" {
		t.Fatalf("conversation identity = %s/%s", conversation.ID, conversation.Title)
	}
	if len(conversation.Messages) != 3 {
		t.Fatalf("messages = %+v, want user, assistant, and system visible messages", conversation.Messages)
	}
	if conversation.Messages[0].Role != "user" || conversation.Messages[1].Role != "assistant" || conversation.Messages[2].Role != "system" {
		t.Fatalf("message roles = %+v", conversation.Messages)
	}
	if conversation.Messages[1].Text != "cursor store fixture assistant phrase" {
		t.Fatalf("assistant text = %q", conversation.Messages[1].Text)
	}
	for _, message := range conversation.Messages {
		if message.Text == "private tool output that should not be indexed" || message.Text == "ignored" {
			t.Fatalf("tool payload was indexed: %+v", conversation.Messages)
		}
	}
}

type fixtureBlob struct {
	id   string
	data []byte
}

func writeFixtureStore(t *testing.T, path string, rows []fixtureBlob) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create fixture dir: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`create table blobs (id text primary key, data blob); create table meta (key text primary key, value text);`); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	meta := `{"agentId":"cursor-fixture-session","createdAt":"2026-06-07T10:00:00Z","name":"Cursor fixture chat"}`
	if _, err := db.Exec(`insert into meta(key, value) values('0', ?)`, hex.EncodeToString([]byte(meta))); err != nil {
		t.Fatalf("insert fixture meta: %v", err)
	}
	for _, row := range rows {
		if _, err := db.Exec(`insert into blobs(id, data) values(?, ?)`, row.id, row.data); err != nil {
			t.Fatalf("insert fixture blob: %v", err)
		}
	}
}
