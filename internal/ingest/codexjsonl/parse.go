package codexjsonl

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/openclaw/aicrawl/internal/archive"
	"github.com/openclaw/aicrawl/internal/ingest/localtext"
)

const (
	Provider   = "codex"
	SourceKind = "codex_jsonl"
)

func StreamFile(path string, emit archive.ConversationEmitter) (archive.ParsedSource, error) {
	if emit == nil {
		return archive.ParsedSource{}, fmt.Errorf("codex stream callback is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return archive.ParsedSource{}, fmt.Errorf("open Codex JSONL: %w", err)
	}
	defer file.Close()
	conversation, warnings, err := parseReader(file, filepath.Base(path))
	if err != nil {
		return archive.ParsedSource{}, err
	}
	if err := emit(conversation, warnings); err != nil {
		return archive.ParsedSource{}, err
	}
	return archive.ParsedSource{Provider: Provider, SourceKind: SourceKind}, nil
}

func parseReader(r io.Reader, fallbackID string) (archive.Conversation, []string, error) {
	reader := bufio.NewReader(r)
	rawID := strings.TrimSuffix(fallbackID, filepath.Ext(fallbackID))
	title := "Codex session " + rawID
	var createdAt string
	var rawConversation json.RawMessage
	var messages []archive.Message
	warnings := []string{}
	for ordinal := 0; ; ordinal++ {
		line, err := reader.ReadBytes('\n')
		if err != nil && len(line) == 0 {
			if err == io.EOF {
				break
			}
			return archive.Conversation{}, nil, fmt.Errorf("read Codex JSONL: %w", err)
		}
		line = []byte(strings.TrimSpace(string(line)))
		if len(line) == 0 {
			if err == io.EOF {
				break
			}
			continue
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(line, &object); err != nil {
			return archive.Conversation{}, nil, fmt.Errorf("parse Codex JSONL line %d: %w", ordinal+1, err)
		}
		eventType := localtext.StringField(object, "type")
		if eventType == "session_meta" {
			var payload map[string]json.RawMessage
			_ = json.Unmarshal(object["payload"], &payload)
			if id := localtext.StringField(payload, "id"); id != "" {
				rawID = id
				title = "Codex session " + id
			}
			createdAt = localtext.TimeField(object, "timestamp")
			rawConversation = append(rawConversation[:0], line...)
			continue
		}
		if eventType != "response_item" {
			continue
		}
		message, ok := parseResponseItem(object, rawID, ordinal, line)
		if !ok {
			continue
		}
		messages = append(messages, message)
	}
	if len(messages) == 0 {
		return archive.Conversation{}, nil, fmt.Errorf("Codex JSONL contains no importable response messages")
	}
	conversationID := Provider + ":" + rawID
	for i := range messages {
		messages[i].ConversationID = conversationID
	}
	if len(rawConversation) == 0 {
		rawConversation = []byte(`{}`)
	}
	return archive.Conversation{
		ID:         conversationID,
		Provider:   Provider,
		RawID:      rawID,
		Title:      title,
		CreatedAt:  createdAt,
		UpdatedAt:  messages[len(messages)-1].CreatedAt,
		RawPayload: rawConversation,
		Messages:   messages,
	}, warnings, nil
}

func parseResponseItem(object map[string]json.RawMessage, sessionID string, ordinal int, raw json.RawMessage) (archive.Message, bool) {
	var payload map[string]json.RawMessage
	if rawPayload, ok := object["payload"]; ok {
		_ = json.Unmarshal(rawPayload, &payload)
	}
	if localtext.StringField(payload, "type") != "message" {
		return archive.Message{}, false
	}
	role := localtext.StringField(payload, "role")
	text := ""
	if rawContent, ok := payload["content"]; ok {
		text = localtext.Text(rawContent)
	}
	if text == "" {
		return archive.Message{}, false
	}
	rawID := fmt.Sprintf("response-%d", ordinal+1)
	return archive.Message{
		ID:            fmt.Sprintf("%s:%s:%s", Provider, sessionID, rawID),
		Provider:      Provider,
		RawID:         rawID,
		Role:          normalizeRole(role),
		Sender:        role,
		CreatedAt:     localtext.TimeField(object, "timestamp"),
		Ordinal:       ordinal,
		IsCurrentPath: true,
		IsPathKnown:   false,
		Text:          text,
		RawPayload:    raw,
	}, true
}

func normalizeRole(role string) string {
	switch role {
	case "user", "assistant", "system", "developer", "tool":
		return role
	default:
		return "unknown"
	}
}
