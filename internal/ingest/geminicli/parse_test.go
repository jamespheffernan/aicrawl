package geminicli

import (
	"path/filepath"
	"testing"

	"github.com/openclaw/aicrawl/internal/archive"
)

func TestStreamFileParsesGeminiSession(t *testing.T) {
	fixture := filepath.Join("..", "..", "..", "testdata", "redacted", "gemini-session.fixture.json")
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
	if len(conversations) != 1 || len(conversations[0].Messages) != 3 {
		t.Fatalf("conversations = %+v, want one conversation with three messages", conversations)
	}
	if conversations[0].Messages[2].Role != "assistant" {
		t.Fatalf("Gemini role = %q, want assistant", conversations[0].Messages[2].Role)
	}
}
