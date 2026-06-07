package chatgptexport_test

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openclaw/aicrawl/internal/ingest/chatgptexport"
)

func TestParseBranchyFixture(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "..", "testdata", "redacted", "chatgpt-export.fixture.json")
	parsed, err := chatgptexport.ParseFile(fixturePath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if parsed.Provider != "chatgpt" || parsed.SourceKind != "chatgpt_export" {
		t.Fatalf("parsed source = %+v", parsed)
	}
	if len(parsed.Conversations) != 1 {
		t.Fatalf("conversations = %d, want 1", len(parsed.Conversations))
	}
	conversation := parsed.Conversations[0]
	if conversation.CreatedAt != "2025-10-09T08:53:20.500000000Z" {
		t.Fatalf("created_at = %q, want fixed-width UTC timestamp", conversation.CreatedAt)
	}
	if len(conversation.Messages) != 6 {
		t.Fatalf("messages = %d, want every mapping node", len(conversation.Messages))
	}
	if len(conversation.Edges) != 5 {
		t.Fatalf("edges = %d, want explicit graph edges", len(conversation.Edges))
	}
	if len(parsed.Warnings) == 0 {
		t.Fatalf("warnings = 0, want malformed timestamp warning")
	}
	var currentPath int
	var toolFound bool
	for _, message := range conversation.Messages {
		if message.IsCurrentPath {
			currentPath++
		}
		if message.Role == "tool" && message.Text == "Tool output searchable text." {
			toolFound = true
		}
	}
	if currentPath != 5 {
		t.Fatalf("current path count = %d, want 5", currentPath)
	}
	if !toolFound {
		t.Fatalf("tool message text was not extracted")
	}

	if _, err := chatgptexport.ParseFile(filepath.Join("..", "..", "..", "testdata", "redacted", "chatgpt-export.fixture.zip")); err != nil {
		t.Fatalf("ParseFile zip fixture: %v", err)
	}
}

func TestParseMultiBatchZipImportsAllConversationArrays(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "chatgpt-multi-batch.fixture.zip")
	writeZipEntries(t, zipPath,
		zipEntry{Name: "account.json", Data: `{"account":"synthetic"}`},
		zipEntry{Name: "batch-0000.json", Data: `[` + chatGPTConversationJSON("batch-0-a") + `,` + chatGPTConversationJSON("batch-0-b") + `]`},
		zipEntry{Name: "batch-0001.json", Data: `[` + chatGPTConversationJSON("batch-1-a") + `]`},
		zipEntry{Name: "shared-links.json", Data: `[{"url":"https://example.invalid/synthetic"}]`},
	)

	parsed, err := chatgptexport.ParseFile(zipPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(parsed.Conversations) != 3 {
		t.Fatalf("conversations = %d, want all conversations from both batches", len(parsed.Conversations))
	}
	seen := map[string]bool{}
	for _, conversation := range parsed.Conversations {
		seen[conversation.RawID] = true
	}
	for _, want := range []string{"batch-0-a", "batch-0-b", "batch-1-a"} {
		if !seen[want] {
			t.Fatalf("missing conversation %q from multi-batch zip; got %#v", want, seen)
		}
	}
}

func TestDerivesCurrentPathFromChildrenEdges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "children-only.fixture.json")
	data := `[
  {
    "id": "children-only",
    "current_node": "assistant",
    "mapping": {
      "root": {"id": "root", "parent": null, "children": ["user"], "message": null},
      "user": {
        "id": "user",
        "parent": null,
        "children": ["assistant"],
        "message": {"author": {"role": "user"}, "content": {"parts": ["hi"]}}
      },
      "assistant": {
        "id": "assistant",
        "parent": null,
        "children": [],
        "message": {"author": {"role": "assistant"}, "content": {"parts": ["hello"]}}
      }
    }
  }
]`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	parsed, err := chatgptexport.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	var currentPath int
	for _, message := range parsed.Conversations[0].Messages {
		if message.IsCurrentPath {
			currentPath++
		}
		if !message.IsPathKnown {
			t.Fatalf("expected path to be known for message %+v", message)
		}
	}
	if currentPath != 3 {
		t.Fatalf("current path count = %d, want root/user/assistant", currentPath)
	}
	for _, message := range parsed.Conversations[0].Messages {
		if message.RawID == "assistant" && message.ParentID == "" {
			t.Fatalf("assistant parent id was not derived from children edge")
		}
	}
}

func chatGPTConversationJSON(id string) string {
	return `{
  "id": "` + id + `",
  "title": "Synthetic ` + id + `",
  "mapping": {
    "node-user": {
      "id": "node-user",
      "parent": null,
      "children": [],
      "message": {
        "author": {"role": "user"},
        "content": {"parts": ["synthetic multi batch text"]}
      }
    }
  }
}`
}

type zipEntry struct {
	Name string
	Data string
}

func writeZipEntries(t *testing.T, zipPath string, entries ...zipEntry) {
	t.Helper()
	file, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(file)
	for _, entry := range entries {
		w, err := zw.Create(entry.Name)
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := w.Write([]byte(entry.Data)); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close zip file: %v", err)
	}
}

func TestMissingIDsUseHashFallbackWithWarnings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-ids.fixture.json")
	data := `[
  {
    "title": "missing ids",
    "current_node": "node-key",
    "mapping": {
      "node-key": {
        "parent": null,
        "children": [],
        "message": {"author": {"role": "user"}, "content": {"parts": ["fallback text"]}}
      }
    }
  }
]`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	parsed, err := chatgptexport.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	conversation := parsed.Conversations[0]
	if !strings.HasPrefix(conversation.RawID, "hash-") {
		t.Fatalf("conversation raw id = %q, want hash fallback", conversation.RawID)
	}
	if len(parsed.Warnings) == 0 {
		t.Fatalf("expected fallback warnings")
	}
	var hasConversationWarning bool
	for _, warning := range parsed.Warnings {
		if strings.Contains(warning, "missing chatgpt conversation id") {
			hasConversationWarning = true
		}
	}
	if !hasConversationWarning {
		t.Fatalf("warnings = %#v, want conversation fallback warning", parsed.Warnings)
	}
}

func TestConversationIDFieldIsStableRawID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation-id.fixture.json")
	data := `[
  {
    "conversation_id": "web-detail-conversation-id",
    "title": "conversation id field",
    "current_node": "node-key",
    "mapping": {
      "node-key": {
        "id": "node-key",
        "parent": null,
        "children": [],
        "message": {"author": {"role": "assistant"}, "content": {"parts": ["conversation id field text"]}}
      }
    }
  }
]`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	parsed, err := chatgptexport.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	conversation := parsed.Conversations[0]
	if conversation.RawID != "web-detail-conversation-id" || conversation.ID != "chatgpt:web-detail-conversation-id" {
		t.Fatalf("conversation id = %q/%q, want conversation_id field", conversation.ID, conversation.RawID)
	}
	for _, warning := range parsed.Warnings {
		if strings.Contains(warning, "missing chatgpt conversation id") {
			t.Fatalf("warnings = %#v, did not want conversation id fallback warning", parsed.Warnings)
		}
	}
}

func TestMissingCurrentPathParentIsNotMarkedKnown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-parent.fixture.json")
	data := `[
  {
    "id": "missing-parent",
    "current_node": "assistant",
    "mapping": {
      "assistant": {
        "id": "assistant",
        "parent": "missing-node",
        "children": [],
        "message": {"author": {"role": "assistant"}, "content": {"parts": ["hello"]}}
      }
    }
  }
]`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	parsed, err := chatgptexport.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	for _, message := range parsed.Conversations[0].Messages {
		if message.IsPathKnown {
			t.Fatalf("path should not be known for incomplete chain: %+v", message)
		}
		if message.IsCurrentPath {
			t.Fatalf("message should not be marked current path for incomplete chain: %+v", message)
		}
	}
	var hasWarning bool
	for _, warning := range parsed.Warnings {
		if strings.Contains(warning, "missing node") {
			hasWarning = true
		}
	}
	if !hasWarning {
		t.Fatalf("warnings = %#v, want missing-node warning", parsed.Warnings)
	}
}

func TestMappingKeyIsCanonicalWhenEmbeddedNodeIDDiffers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mismatched-node-id.fixture.json")
	data := `[
  {
    "id": "mismatched-node-id",
    "current_node": "node-key",
    "mapping": {
      "root-key": {
        "id": "embedded-root-id",
        "parent": null,
        "children": ["node-key"],
        "message": null
      },
      "node-key": {
        "id": "embedded-message-id",
        "parent": "root-key",
        "children": [],
        "message": {"author": {"role": "user"}, "content": {"parts": ["key identity text"]}}
      }
    }
  }
]`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	parsed, err := chatgptexport.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	conversation := parsed.Conversations[0]
	if len(conversation.Edges) != 1 {
		t.Fatalf("edges = %d, want 1", len(conversation.Edges))
	}
	edge := conversation.Edges[0]
	if edge.RawParentID != "root-key" || edge.RawChildID != "node-key" {
		t.Fatalf("edge = %s -> %s, want root-key -> node-key", edge.RawParentID, edge.RawChildID)
	}
	var foundNode bool
	var currentPath int
	for _, message := range conversation.Messages {
		if message.IsCurrentPath {
			currentPath++
		}
		if message.RawID == "node-key" {
			foundNode = true
			if message.ParentID != "chatgpt:mismatched-node-id:root-key" {
				t.Fatalf("parent id = %q, want root-key parent", message.ParentID)
			}
		}
		if strings.HasPrefix(message.RawID, "embedded-") {
			t.Fatalf("message raw id = %q, want mapping key identity", message.RawID)
		}
	}
	if !foundNode {
		t.Fatalf("node-key message was not preserved")
	}
	if currentPath != 2 {
		t.Fatalf("current path count = %d, want root-key/node-key", currentPath)
	}
	var hasWarning bool
	for _, warning := range parsed.Warnings {
		if strings.Contains(warning, "mapping key differs") {
			hasWarning = true
		}
	}
	if !hasWarning {
		t.Fatalf("warnings = %#v, want mismatched node id warning", parsed.Warnings)
	}
}
