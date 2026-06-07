package geminicli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openclaw/aicrawl/internal/archive"
	"github.com/openclaw/aicrawl/internal/ingest/localtext"
)

const (
	Provider   = "gemini"
	SourceKind = "gemini_cli"
)

func StreamFile(path string, emit archive.ConversationEmitter) (archive.ParsedSource, error) {
	if emit == nil {
		return archive.ParsedSource{}, fmt.Errorf("gemini stream callback is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return archive.ParsedSource{}, fmt.Errorf("read Gemini session JSON: %w", err)
	}
	conversation, warnings, err := parseData(data, filepath.Base(path))
	if err != nil {
		return archive.ParsedSource{}, err
	}
	if err := emit(conversation, warnings); err != nil {
		return archive.ParsedSource{}, err
	}
	return archive.ParsedSource{Provider: Provider, SourceKind: SourceKind}, nil
}

func parseData(data []byte, fallbackID string) (archive.Conversation, []string, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return archive.Conversation{}, nil, fmt.Errorf("parse Gemini session JSON: %w", err)
	}
	rawID := localtext.StringField(object, "sessionId")
	if rawID == "" {
		rawID = strings.TrimSuffix(fallbackID, filepath.Ext(fallbackID))
	}
	var rawMessages []json.RawMessage
	if raw, ok := object["messages"]; ok {
		if err := json.Unmarshal(raw, &rawMessages); err != nil {
			return archive.Conversation{}, nil, fmt.Errorf("Gemini messages field is not an array: %w", err)
		}
	}
	var messages []archive.Message
	warnings := []string{}
	for i, raw := range rawMessages {
		message, ok := parseMessage(raw, rawID, i)
		if !ok {
			warnings = append(warnings, "skipped Gemini message without visible text at index "+fmt.Sprint(i))
			continue
		}
		messages = append(messages, message)
	}
	if len(messages) == 0 {
		return archive.Conversation{}, nil, fmt.Errorf("Gemini session contains no importable messages")
	}
	conversationID := Provider + ":" + rawID
	for i := range messages {
		messages[i].ConversationID = conversationID
	}
	return archive.Conversation{
		ID:         conversationID,
		Provider:   Provider,
		RawID:      rawID,
		Title:      "Gemini session " + rawID,
		CreatedAt:  localtext.TimeField(object, "startTime"),
		UpdatedAt:  localtext.TimeField(object, "lastUpdated"),
		RawPayload: data,
		Messages:   messages,
	}, warnings, nil
}

func parseMessage(raw json.RawMessage, sessionID string, ordinal int) (archive.Message, bool) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return archive.Message{}, false
	}
	sourceType := localtext.StringField(object, "type")
	role := normalizeRole(sourceType)
	text := ""
	if rawContent, ok := object["content"]; ok {
		text = localtext.Text(rawContent)
	}
	if text == "" {
		return archive.Message{}, false
	}
	rawID := localtext.StringField(object, "id")
	if rawID == "" {
		rawID = fmt.Sprintf("message-%d", ordinal+1)
	}
	return archive.Message{
		ID:            fmt.Sprintf("%s:%s:%s", Provider, sessionID, rawID),
		Provider:      Provider,
		RawID:         rawID,
		Role:          role,
		Sender:        sourceType,
		CreatedAt:     localtext.TimeField(object, "timestamp"),
		Ordinal:       ordinal,
		IsCurrentPath: true,
		IsPathKnown:   false,
		Text:          text,
		RawPayload:    raw,
	}, true
}

func normalizeRole(sourceType string) string {
	switch sourceType {
	case "user":
		return "user"
	case "gemini":
		return "assistant"
	case "info":
		return "system"
	default:
		return "unknown"
	}
}
