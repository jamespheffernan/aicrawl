package claudecodejsonl

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
	Provider   = "claude-code"
	SourceKind = "claude_code_jsonl"
)

func StreamFile(path string, emit archive.ConversationEmitter) (archive.ParsedSource, error) {
	if emit == nil {
		return archive.ParsedSource{}, fmt.Errorf("Claude Code stream callback is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return archive.ParsedSource{}, fmt.Errorf("open Claude Code JSONL: %w", err)
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
	title := "Claude Code session " + rawID
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
			return archive.Conversation{}, nil, fmt.Errorf("read Claude Code JSONL: %w", err)
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
			return archive.Conversation{}, nil, fmt.Errorf("parse Claude Code JSONL line %d: %w", ordinal+1, err)
		}
		sessionID := localtext.StringField(object, "sessionId")
		if sessionID != "" && (rawID == "" || rawID == strings.TrimSuffix(fallbackID, filepath.Ext(fallbackID))) {
			rawID = sessionID
			title = "Claude Code session " + sessionID
		}
		if len(rawConversation) == 0 {
			rawConversation = append(rawConversation[:0], line...)
		}
		message, ok := parseMessage(object, rawID, ordinal, line)
		if !ok {
			continue
		}
		if createdAt == "" {
			createdAt = message.CreatedAt
		}
		messages = append(messages, message)
	}
	if len(messages) == 0 {
		return archive.Conversation{}, nil, fmt.Errorf("Claude Code JSONL contains no importable messages")
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

func parseMessage(object map[string]json.RawMessage, sessionID string, ordinal int, raw json.RawMessage) (archive.Message, bool) {
	eventType := localtext.StringField(object, "type")
	if eventType != "user" && eventType != "assistant" {
		return archive.Message{}, false
	}
	var messageObject map[string]json.RawMessage
	if rawMessage, ok := object["message"]; ok {
		_ = json.Unmarshal(rawMessage, &messageObject)
	}
	role := localtext.StringField(messageObject, "role")
	if role == "" {
		role = eventType
	}
	text := ""
	if rawContent, ok := messageObject["content"]; ok {
		text = visibleMessageText(rawContent)
	}
	if text == "" {
		return archive.Message{}, false
	}
	rawID := localtext.StringField(object, "uuid")
	if rawID == "" {
		rawID = fmt.Sprintf("line-%d", ordinal+1)
	}
	parentID := ""
	if parentRawID := localtext.StringField(object, "parentUuid"); parentRawID != "" {
		parentID = Provider + ":" + sessionID + ":" + parentRawID
	}
	return archive.Message{
		ID:            Provider + ":" + sessionID + ":" + rawID,
		Provider:      Provider,
		RawID:         rawID,
		ParentID:      parentID,
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

func visibleMessageText(raw json.RawMessage) string {
	parts := visibleTextParts(raw)
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func visibleTextParts(raw json.RawMessage) []string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if strings.TrimSpace(s) != "" {
			return []string{s}
		}
		return nil
	}
	var array []json.RawMessage
	if err := json.Unmarshal(raw, &array); err == nil {
		var out []string
		for _, item := range array {
			out = append(out, visibleTextParts(item)...)
		}
		return out
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil
	}
	switch localtext.StringField(object, "type") {
	case "text", "":
		return localtext.ExtractText(raw)
	case "tool_result", "tool_use", "thinking":
		return nil
	default:
		return nil
	}
}

func normalizeRole(role string) string {
	switch role {
	case "user", "assistant", "system", "developer", "tool":
		return role
	default:
		return "unknown"
	}
}
