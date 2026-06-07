package chatgptweb

import (
	"path/filepath"
	"testing"

	"github.com/openclaw/aicrawl/internal/archive"
)

func TestStreamFileUsesChatGPTWebSourceKind(t *testing.T) {
	fixture := filepath.Join("..", "..", "..", "testdata", "redacted", "chatgpt-web-conversation.fixture.json")
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
		t.Fatalf("conversations = %d, want 1", len(conversations))
	}
	if got := len(conversations[0].Messages); got != 3 {
		t.Fatalf("messages = %d, want graph nodes including root", got)
	}
}
