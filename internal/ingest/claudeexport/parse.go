package claudeexport

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/openclaw/aicrawl/internal/archive"
	"github.com/openclaw/aicrawl/internal/ingest/jsonstream"
	"github.com/openclaw/aicrawl/internal/security"
	"github.com/openclaw/aicrawl/internal/textnorm"
	"github.com/openclaw/aicrawl/internal/timefmt"
)

var ErrNotClaude = errors.New("not a claude export")
var errStopSourceWalk = errors.New("stop claude source walk")

func ParseFile(path string) (archive.ParsedSource, error) {
	var parsed archive.ParsedSource
	header, err := StreamFile(path, func(conversation archive.Conversation, warnings []string) error {
		parsed.Conversations = append(parsed.Conversations, conversation)
		parsed.Warnings = append(parsed.Warnings, warnings...)
		return nil
	})
	if err != nil {
		return archive.ParsedSource{}, err
	}
	parsed.Provider = header.Provider
	parsed.SourceKind = header.SourceKind
	return parsed, nil
}

func StreamFile(path string, emit archive.ConversationEmitter) (archive.ParsedSource, error) {
	if emit == nil {
		return archive.ParsedSource{}, fmt.Errorf("claude stream callback is required")
	}
	var parsed archive.ParsedSource
	var notClaude bool
	err := security.WalkJSONSources(path, func(name string, r io.Reader) error {
		next, err := parseReader(r, emit)
		if err == nil {
			parsed = next
			return errStopSourceWalk
		}
		if errors.Is(err, ErrNotClaude) {
			notClaude = true
			return nil
		}
		if filepath.Base(name) == "conversations.json" {
			return err
		}
		return nil
	})
	if errors.Is(err, errStopSourceWalk) {
		return parsed, nil
	}
	if err != nil {
		return archive.ParsedSource{}, err
	}
	if notClaude {
		return archive.ParsedSource{}, ErrNotClaude
	}
	return archive.ParsedSource{}, ErrNotClaude
}

func parseData(data []byte) (archive.ParsedSource, error) {
	var parsed archive.ParsedSource
	header, err := parseDataTo(data, func(conversation archive.Conversation, warnings []string) error {
		parsed.Conversations = append(parsed.Conversations, conversation)
		parsed.Warnings = append(parsed.Warnings, warnings...)
		return nil
	})
	if err != nil {
		return archive.ParsedSource{}, err
	}
	parsed.Provider = header.Provider
	parsed.SourceKind = header.SourceKind
	return parsed, nil
}

func parseReader(r io.Reader, emit archive.ConversationEmitter) (archive.ParsedSource, error) {
	parsed := archive.ParsedSource{Provider: "claude", SourceKind: "claude_export"}
	var sawConversation bool
	br := bufio.NewReader(r)
	streamed, err := jsonstream.ForEachTopLevelArrayValue(br, func(rawConversation json.RawMessage, index int) error {
		if index == 0 && !looksLikeClaudeConversation(rawConversation) {
			return ErrNotClaude
		}
		sawConversation = true
		conversation, warnings, err := parseConversation(rawConversation, index)
		if err != nil {
			return err
		}
		return emit(conversation, warnings)
	})
	if err != nil {
		return archive.ParsedSource{}, err
	}
	if streamed {
		if !sawConversation {
			return archive.ParsedSource{}, ErrNotClaude
		}
		return parsed, nil
	}
	data, err := io.ReadAll(br)
	if err != nil {
		return archive.ParsedSource{}, fmt.Errorf("read claude JSON source: %w", err)
	}
	return parseDataTo(data, emit)
}

func parseDataTo(data []byte, emit archive.ConversationEmitter) (archive.ParsedSource, error) {
	rawConversations, err := topLevelConversations(data)
	if err != nil {
		return archive.ParsedSource{}, err
	}
	if !looksLikeClaude(rawConversations) {
		return archive.ParsedSource{}, ErrNotClaude
	}
	parsed := archive.ParsedSource{Provider: "claude", SourceKind: "claude_export"}
	for i, rawConversation := range rawConversations {
		conversation, warnings, err := parseConversation(rawConversation, i)
		if err != nil {
			return archive.ParsedSource{}, err
		}
		if err := emit(conversation, warnings); err != nil {
			return archive.ParsedSource{}, err
		}
	}
	return parsed, nil
}

func topLevelConversations(data []byte) ([]json.RawMessage, error) {
	var array []json.RawMessage
	if err := json.Unmarshal(data, &array); err == nil {
		return array, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, ErrNotClaude
	}
	if raw, ok := object["conversations"]; ok {
		if err := json.Unmarshal(raw, &array); err != nil {
			return nil, fmt.Errorf("claude conversations field is not an array: %w", err)
		}
		return array, nil
	}
	return nil, ErrNotClaude
}

func looksLikeClaude(rawConversations []json.RawMessage) bool {
	for _, raw := range rawConversations {
		if looksLikeClaudeConversation(raw) {
			return true
		}
	}
	return false
}

func looksLikeClaudeConversation(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return false
	}
	if _, hasUUID := object["uuid"]; hasUUID {
		if _, hasMessages := object["chat_messages"]; hasMessages {
			return true
		}
	}
	return false
}

func parseConversation(raw json.RawMessage, index int) (archive.Conversation, []string, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return archive.Conversation{}, nil, fmt.Errorf("parse claude conversation %d: %w", index, err)
	}
	rawID := stringField(object, "uuid")
	if rawID == "" {
		return archive.Conversation{}, nil, fmt.Errorf("claude conversation %d is missing required uuid", index)
	}
	conversationID := "claude:" + rawID
	warnings := []string{}
	createdAt := parseClaudeTime(object, "created_at", rawID, &warnings)
	updatedAt := parseClaudeTime(object, "updated_at", rawID, &warnings)
	conversation := archive.Conversation{
		ID:         conversationID,
		Provider:   "claude",
		RawID:      rawID,
		Title:      stringField(object, "name"),
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
		RawPayload: raw,
	}
	var rawMessages []json.RawMessage
	if rawField, ok := object["chat_messages"]; ok {
		if err := json.Unmarshal(rawField, &rawMessages); err != nil {
			return archive.Conversation{}, nil, fmt.Errorf("claude conversation %s has invalid chat_messages: %w", redactID(rawID), err)
		}
	}
	for i, rawMessage := range rawMessages {
		message, attachments, messageWarnings, err := parseMessage(rawMessage, conversationID, rawID, i)
		if err != nil {
			return archive.Conversation{}, nil, err
		}
		conversation.Messages = append(conversation.Messages, message)
		conversation.Attachments = append(conversation.Attachments, attachments...)
		warnings = append(warnings, messageWarnings...)
	}
	return conversation, warnings, nil
}

func parseMessage(raw json.RawMessage, conversationID, conversationRawID string, index int) (archive.Message, []archive.Attachment, []string, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return archive.Message{}, nil, nil, fmt.Errorf("parse claude message %d in conversation %s: %w", index, redactID(conversationRawID), err)
	}
	rawID := stringField(object, "uuid")
	if rawID == "" {
		return archive.Message{}, nil, nil, fmt.Errorf("claude message %d in conversation %s is missing required uuid", index, redactID(conversationRawID))
	}
	warnings := []string{}
	sender := stringField(object, "sender")
	role := "unknown"
	switch sender {
	case "human":
		role = "user"
	case "assistant":
		role = "assistant"
	case "":
		role = "unknown"
	default:
		warnings = append(warnings, "unknown claude sender for message "+redactID(rawID))
	}
	messageID := "claude:" + conversationRawID + ":" + rawID
	message := archive.Message{
		ID:             messageID,
		Provider:       "claude",
		ConversationID: conversationID,
		RawID:          rawID,
		Role:           role,
		Sender:         sender,
		CreatedAt:      parseClaudeTime(object, "created_at", rawID, &warnings),
		UpdatedAt:      parseClaudeTime(object, "updated_at", rawID, &warnings),
		Ordinal:        index,
		IsCurrentPath:  true,
		IsPathKnown:    true,
		Text:           extractClaudeText(object),
		RawPayload:     raw,
	}
	var attachments []archive.Attachment
	attachments = append(attachments, parseAttachmentArray(object, "attachments", conversationID, messageID, conversationRawID, rawID)...)
	attachments = append(attachments, parseAttachmentArray(object, "files", conversationID, messageID, conversationRawID, rawID)...)
	return message, attachments, warnings, nil
}

func parseAttachmentArray(object map[string]json.RawMessage, field, conversationID, messageID, conversationRawID, messageRawID string) []archive.Attachment {
	rawArray, ok := object[field]
	if !ok {
		return nil
	}
	var values []json.RawMessage
	if json.Unmarshal(rawArray, &values) != nil {
		return nil
	}
	attachments := make([]archive.Attachment, 0, len(values))
	for i, raw := range values {
		attachmentObject := map[string]json.RawMessage{}
		_ = json.Unmarshal(raw, &attachmentObject)
		attachments = append(attachments, archive.Attachment{
			ID:             fmt.Sprintf("claude:%s:%s:%s:%d", conversationRawID, messageRawID, field, i),
			Provider:       "claude",
			ConversationID: conversationID,
			MessageID:      messageID,
			Kind:           field,
			Filename:       firstStringField(attachmentObject, "file_name", "filename", "name"),
			MimeType:       firstStringField(attachmentObject, "mime_type", "content_type"),
			Text:           firstStringField(attachmentObject, "text", "extracted_content"),
			RawPayload:     raw,
		})
	}
	return attachments
}

func extractClaudeText(object map[string]json.RawMessage) string {
	parts := []string{}
	if text := stringField(object, "text"); text != "" {
		parts = append(parts, text)
	}
	if raw, ok := object["content"]; ok {
		parts = append(parts, extractText(raw)...)
	}
	return textnorm.Normalize(strings.Join(parts, "\n\n"))
}

func extractText(raw json.RawMessage) []string {
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
			out = append(out, extractText(item)...)
		}
		return out
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil
	}
	for _, key := range []string{"text", "content"} {
		if value, ok := object[key]; ok {
			return extractText(value)
		}
	}
	return nil
}

func parseClaudeTime(object map[string]json.RawMessage, field, rawID string, warnings *[]string) string {
	value := stringField(object, field)
	if strings.TrimSpace(value) == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000000Z"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return timefmt.FormatUTC(parsed)
		}
	}
	*warnings = append(*warnings, "malformed claude timestamp "+field+" for "+redactID(rawID))
	return ""
}

func stringField(object map[string]json.RawMessage, key string) string {
	raw, ok := object[key]
	if !ok || string(raw) == "null" {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return ""
}

func firstStringField(object map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if value := stringField(object, key); value != "" {
			return value
		}
	}
	return ""
}

func redactID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:8] + "..."
}
