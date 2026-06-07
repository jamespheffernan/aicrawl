package claudecodejsonl

import (
	"path/filepath"
	"testing"

	"github.com/openclaw/aicrawl/internal/archive"
)

func TestStreamFileParsesClaudeCodeJSONL(t *testing.T) {
	fixture := filepath.Join("..", "..", "..", "testdata", "redacted", "claude-code-session.fixture.jsonl")
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
	if conversation.ID != "claude-code:cc-session-fixture" {
		t.Fatalf("conversation ID = %q, want stable session ID", conversation.ID)
	}
	if len(conversation.Messages) != 3 {
		t.Fatalf("messages = %+v, want three visible transcript messages", conversation.Messages)
	}
	if conversation.Messages[0].Role != "user" || conversation.Messages[1].Role != "assistant" || conversation.Messages[2].Role != "user" {
		t.Fatalf("message roles = %+v, want user, assistant, user", conversation.Messages)
	}
	if conversation.Messages[1].Text != "claude code jsonl fixture assistant phrase" {
		t.Fatalf("assistant text = %q", conversation.Messages[1].Text)
	}
	for _, message := range conversation.Messages {
		if message.Text == "private tool output that should not be indexed" || message.Text == "thinking text that should not be indexed" {
			t.Fatalf("non-visible Claude Code content was indexed: %+v", conversation.Messages)
		}
	}
}
