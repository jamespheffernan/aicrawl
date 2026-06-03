package claudeexport_test

import (
	"path/filepath"
	"testing"

	"github.com/openclaw/aicrawl/internal/ingest/claudeexport"
)

func TestParseFixture(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "..", "testdata", "redacted", "claude-export.fixture.json")
	parsed, err := claudeexport.ParseFile(fixturePath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if parsed.Provider != "claude" || parsed.SourceKind != "claude_export" {
		t.Fatalf("parsed source = %+v", parsed)
	}
	if len(parsed.Conversations) != 1 {
		t.Fatalf("conversations = %d, want 1", len(parsed.Conversations))
	}
	conversation := parsed.Conversations[0]
	if conversation.CreatedAt != "2026-01-02T03:04:05.000000000Z" {
		t.Fatalf("created_at = %q, want fixed-width UTC timestamp", conversation.CreatedAt)
	}
	if len(conversation.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(conversation.Messages))
	}
	if len(conversation.Attachments) != 2 {
		t.Fatalf("attachments = %d, want 2", len(conversation.Attachments))
	}
	if conversation.Messages[0].Role != "user" || conversation.Messages[1].Role != "assistant" {
		t.Fatalf("roles = %q, %q", conversation.Messages[0].Role, conversation.Messages[1].Role)
	}
	if conversation.Messages[0].CreatedAt != "2026-01-02T03:05:00.000000000Z" {
		t.Fatalf("message created_at = %q, want fixed-width UTC timestamp", conversation.Messages[0].CreatedAt)
	}
	if string(conversation.RawPayload) == "" || string(conversation.Messages[1].RawPayload) == "" {
		t.Fatalf("raw payloads were not preserved")
	}

	if _, err := claudeexport.ParseFile(filepath.Join("..", "..", "..", "testdata", "redacted", "claude-export.fixture.zip")); err != nil {
		t.Fatalf("ParseFile zip fixture: %v", err)
	}
}
