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
	var sourceChannel string
	var rawConversation json.RawMessage
	var messages []archive.Message
	warnings := []string{}
	skippedNoText := 0
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
		message, channel, ok := parseMessage(object, rawID, ordinal, line)
		if !ok {
			skippedNoText++
			continue
		}
		if sourceChannel == "" {
			sourceChannel = channel
		}
		messages = append(messages, message)
	}
	if len(messages) == 0 {
		return archive.Conversation{}, nil, fmt.Errorf("OpenClaw JSONL contains no importable messages")
	}
	if skippedNoText > 0 {
		warnings = append(warnings, fmt.Sprintf("skipped %d OpenClaw messages without visible text", skippedNoText))
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
	if sourceChannel != "" {
		title = "OpenClaw " + displaySourceChannel(sourceChannel) + " session " + rawID
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

func parseMessage(object map[string]json.RawMessage, sessionID string, ordinal int, raw json.RawMessage) (archive.Message, string, bool) {
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
		return archive.Message{}, "", false
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
	sourceChannel := localtext.StringField(messageObject, "sourceChannel")
	return archive.Message{
		ID:            Provider + ":" + sessionID + ":" + rawID,
		Provider:      Provider,
		RawID:         rawID,
		ParentID:      parentID,
		Role:          normalizeRole(role),
		Sender:        messageSender(messageObject, role),
		CreatedAt:     createdAt,
		Ordinal:       ordinal,
		IsCurrentPath: true,
		IsPathKnown:   false,
		Text:          text,
		RawPayload:    raw,
	}, sourceChannel, true
}

func normalizeRole(role string) string {
	switch role {
	case "user", "assistant", "system", "developer", "tool":
		return role
	default:
		return "unknown"
	}
}

func messageSender(messageObject map[string]json.RawMessage, fallback string) string {
	if senderLabel := localtext.StringField(messageObject, "senderLabel"); senderLabel != "" {
		return senderLabel
	}
	name := localtext.StringField(messageObject, "senderName")
	username := strings.TrimPrefix(localtext.StringField(messageObject, "senderUsername"), "@")
	switch {
	case name != "" && username != "":
		return name + " (@" + username + ")"
	case name != "":
		return name
	case username != "":
		return "@" + username
	case fallback != "":
		return fallback
	default:
		return "unknown"
	}
}

func displaySourceChannel(channel string) string {
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "discord":
		return "Discord"
	case "telegram":
		return "Telegram"
	default:
		return strings.TrimSpace(channel)
	}
}
