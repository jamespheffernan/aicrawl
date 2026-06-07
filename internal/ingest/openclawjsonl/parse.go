package openclawjsonl

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
	Provider   = "openclaw"
	SourceKind = "openclaw_jsonl"
)

func StreamFile(path string, emit archive.ConversationEmitter) (archive.ParsedSource, error) {
	if emit == nil {
		return archive.ParsedSource{}, fmt.Errorf("openclaw stream callback is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return archive.ParsedSource{}, fmt.Errorf("open OpenClaw JSONL: %w", err)
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
	title := "OpenClaw session " + rawID
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
			return archive.Conversation{}, nil, fmt.Errorf("read OpenClaw JSONL: %w", err)
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
			return archive.Conversation{}, nil, fmt.Errorf("parse OpenClaw JSONL line %d: %w", ordinal+1, err)
		}
		eventType := localtext.StringField(object, "type")
		if eventType == "session" {
			if id := localtext.StringField(object, "id"); id != "" {
				rawID = id
				title = "OpenClaw session " + id
			}
			createdAt = localtext.TimeField(object, "timestamp")
			rawConversation = append(rawConversation[:0], line...)
			continue
		}
		if eventType != "message" {
			continue
		}
		message, ok := parseMessage(object, rawID, ordinal, line)
		if !ok {
			warnings = append(warnings, "skipped OpenClaw message without visible text at line "+fmt.Sprint(ordinal+1))
			continue
		}
		messages = append(messages, message)
	}
	if len(messages) == 0 {
		return archive.Conversation{}, nil, fmt.Errorf("OpenClaw JSONL contains no importable messages")
	}
	conversationID := Provider + ":" + rawID
	for i := range messages {
		messages[i].ConversationID = conversationID
		if messages[i].ID == "" {
			messages[i].ID = fmt.Sprintf("%s:%s:%d", Provider, rawID, i)
		}
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

func parseMessage(object map[string]json.RawMessage, sessionID string, ordinal int, raw json.RawMessage) (archive.Message, bool) {
	var messageObject map[string]json.RawMessage
	if rawMessage, ok := object["message"]; ok {
		_ = json.Unmarshal(rawMessage, &messageObject)
	}
	role := localtext.StringField(messageObject, "role")
	if role == "" {
		role = "unknown"
	}
	text := ""
	if rawContent, ok := messageObject["content"]; ok {
		text = localtext.Text(rawContent)
	}
	if text == "" {
		return archive.Message{}, false
	}
	rawID := localtext.StringField(object, "id")
	if rawID == "" {
		rawID = fmt.Sprintf("line-%d", ordinal+1)
	}
	parentID := ""
	if parentRawID := localtext.StringField(object, "parentId"); parentRawID != "" {
		parentID = Provider + ":" + sessionID + ":" + parentRawID
	}
	createdAt := localtext.TimeField(object, "timestamp")
	if messageAt := localtext.TimeField(messageObject, "timestamp"); messageAt != "" {
		createdAt = messageAt
	}
	return archive.Message{
		ID:            Provider + ":" + sessionID + ":" + rawID,
		Provider:      Provider,
		RawID:         rawID,
		ParentID:      parentID,
		Role:          normalizeRole(role),
		Sender:        role,
		CreatedAt:     createdAt,
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
