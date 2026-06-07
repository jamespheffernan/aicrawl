package codexjsonl

import (
	"path/filepath"
	"testing"

	"github.com/openclaw/aicrawl/internal/archive"
)

func TestStreamFileParsesCodexJSONL(t *testing.T) {
	fixture := filepath.Join("..", "..", "..", "testdata", "redacted", "codex-session.fixture.jsonl")
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
		t.Fatalf("conversations = %+v, want one conversation with two transcript messages", conversations)
	}
	if conversations[0].Messages[0].Role != "user" || conversations[0].Messages[1].Role != "assistant" {
		t.Fatalf("message roles = %+v, want user then assistant", conversations[0].Messages)
	}
}
