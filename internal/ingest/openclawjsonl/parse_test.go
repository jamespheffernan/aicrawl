package openclawjsonl

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/openclaw/aicrawl/internal/archive"
)

func TestStreamFileParsesOpenClawJSONL(t *testing.T) {
	fixture := filepath.Join("..", "..", "..", "testdata", "redacted", "openclaw-session.fixture.jsonl")
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
	if len(conversations) != 1 || len(conversations[0].Messages) != 2 {
		t.Fatalf("conversations = %+v, want one conversation with two messages", conversations)
	}
	if conversations[0].Title != "OpenClaw Telegram session openclaw-session-1" {
		t.Fatalf("title = %q, want Telegram source channel in title", conversations[0].Title)
	}
	if conversations[0].Messages[0].Sender != "Redacted User (@redacted_user)" {
		t.Fatalf("sender = %q, want OpenClaw sender metadata", conversations[0].Messages[0].Sender)
	}
	if conversations[0].Messages[1].Role != "assistant" {
		t.Fatalf("assistant role = %q, want assistant", conversations[0].Messages[1].Role)
	}
}

func TestStreamFileAggregatesOpenClawNoTextWarnings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openclaw-control-mixed.jsonl")
	data := `{"type":"session","version":1,"id":"mixed","timestamp":"2025-10-09T08:53:20Z","cwd":"/private/workspace"}` + "\n" +
		`{"type":"message","id":"tool-1","timestamp":"2025-10-09T08:53:21Z","message":{"role":"tool","timestamp":"2025-10-09T08:53:21Z","content":[]}}` + "\n" +
		`{"type":"message","id":"user-1","timestamp":"2025-10-09T08:53:22Z","message":{"role":"user","timestamp":"2025-10-09T08:53:22Z","content":[{"type":"text","text":"visible mixed fixture phrase"}]}}` + "\n" +
		`{"type":"message","id":"tool-2","timestamp":"2025-10-09T08:53:23Z","message":{"role":"tool","timestamp":"2025-10-09T08:53:23Z","content":[]}}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var gotWarnings []string
	_, err := StreamFile(path, func(conversation archive.Conversation, warnings []string) error {
		gotWarnings = append(gotWarnings, warnings...)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamFile: %v", err)
	}
	if len(gotWarnings) != 1 || gotWarnings[0] != "skipped 2 OpenClaw messages without visible text" {
		t.Fatalf("warnings = %#v, want one aggregate warning", gotWarnings)
	}
}
