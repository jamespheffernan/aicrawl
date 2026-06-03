package chatgptexport

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/aicrawl/internal/archive"
	"github.com/openclaw/aicrawl/internal/ingest/jsonstream"
	"github.com/openclaw/aicrawl/internal/security"
	"github.com/openclaw/aicrawl/internal/textnorm"
	"github.com/openclaw/aicrawl/internal/timefmt"
)

var ErrNotChatGPT = errors.New("not a chatgpt export")

type nodeRecord struct {
	rawID       string
	parentRawID string
	children    []string
	role        string
	createdAt   string
	updatedAt   string
	text        string
	rawPayload  json.RawMessage
}

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
		return archive.ParsedSource{}, fmt.Errorf("chatgpt stream callback is required")
	}
	parsed := archive.ParsedSource{Provider: "chatgpt", SourceKind: "chatgpt_export"}
	var notChatGPT bool
	var sawChatGPT bool
	err := security.WalkJSONSources(path, func(name string, r io.Reader) error {
		next, err := parseReader(r, emit)
		if err == nil {
			parsed.Provider = next.Provider
			parsed.SourceKind = next.SourceKind
			sawChatGPT = true
			return nil
		}
		if errors.Is(err, ErrNotChatGPT) {
			notChatGPT = true
			return nil
		}
		return err
	})
	if err != nil {
		return archive.ParsedSource{}, err
	}
	if sawChatGPT {
		return parsed, nil
	}
	if notChatGPT {
		return archive.ParsedSource{}, ErrNotChatGPT
	}
	return archive.ParsedSource{}, ErrNotChatGPT
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
	parsed := archive.ParsedSource{Provider: "chatgpt", SourceKind: "chatgpt_export"}
	var sawConversation bool
	br := bufio.NewReader(r)
	streamed, err := jsonstream.ForEachTopLevelArrayValue(br, func(rawConversation json.RawMessage, index int) error {
		if index == 0 && !looksLikeChatGPTConversation(rawConversation) {
			return ErrNotChatGPT
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
			return archive.ParsedSource{}, ErrNotChatGPT
		}
		return parsed, nil
	}
	data, err := io.ReadAll(br)
	if err != nil {
		return archive.ParsedSource{}, fmt.Errorf("read chatgpt JSON source: %w", err)
	}
	return parseDataTo(data, emit)
}

func parseDataTo(data []byte, emit archive.ConversationEmitter) (archive.ParsedSource, error) {
	rawConversations, err := topLevelConversations(data)
	if err != nil {
		return archive.ParsedSource{}, err
	}
	if !looksLikeChatGPT(rawConversations) {
		return archive.ParsedSource{}, ErrNotChatGPT
	}
	parsed := archive.ParsedSource{Provider: "chatgpt", SourceKind: "chatgpt_export"}
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
		return nil, ErrNotChatGPT
	}
	if _, ok := object["mapping"]; ok {
		return []json.RawMessage{data}, nil
	}
	if raw, ok := object["conversations"]; ok {
		if err := json.Unmarshal(raw, &array); err != nil {
			return nil, fmt.Errorf("chatgpt conversations field is not an array: %w", err)
		}
		return array, nil
	}
	return nil, ErrNotChatGPT
}

func looksLikeChatGPT(rawConversations []json.RawMessage) bool {
	for _, raw := range rawConversations {
		if looksLikeChatGPTConversation(raw) {
			return true
		}
	}
	return false
}

func looksLikeChatGPTConversation(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return false
	}
	_, hasMapping := object["mapping"]
	return hasMapping
}

func parseConversation(raw json.RawMessage, index int) (archive.Conversation, []string, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return archive.Conversation{}, nil, fmt.Errorf("parse chatgpt conversation %d: %w", index, err)
	}
	warnings := []string{}
	rawID := stringField(object, "id")
	if rawID == "" {
		rawID = deterministicRawFallback("conversation", raw)
		warnings = append(warnings, "missing chatgpt conversation id, used content hash fallback "+redactID(rawID))
	}
	conversationID := "chatgpt:" + rawID
	conversation := archive.Conversation{
		ID:            conversationID,
		Provider:      "chatgpt",
		RawID:         rawID,
		Title:         stringField(object, "title"),
		CreatedAt:     parseTimestamp(object, "create_time", rawID, &warnings),
		UpdatedAt:     parseTimestamp(object, "update_time", rawID, &warnings),
		CurrentNodeID: stringField(object, "current_node"),
		RawPayload:    raw,
	}
	var mapping map[string]json.RawMessage
	if rawMapping, ok := object["mapping"]; ok {
		if err := json.Unmarshal(rawMapping, &mapping); err != nil {
			return archive.Conversation{}, nil, fmt.Errorf("chatgpt conversation %s has invalid mapping: %w", redactID(rawID), err)
		}
	} else {
		return archive.Conversation{}, nil, ErrNotChatGPT
	}
	records := make([]nodeRecord, 0, len(mapping))
	parentByNode := map[string]string{}
	knownNodes := map[string]bool{}
	for key, rawNode := range mapping {
		record, nodeWarnings, err := parseNode(rawNode, key, conversationID, rawID)
		if err != nil {
			return archive.Conversation{}, nil, err
		}
		records = append(records, record)
		warnings = append(warnings, nodeWarnings...)
		knownNodes[record.rawID] = true
		if record.parentRawID != "" {
			parentByNode[record.rawID] = record.parentRawID
		}
	}
	for _, record := range records {
		for _, child := range record.children {
			if child != "" && parentByNode[child] == "" {
				parentByNode[child] = record.rawID
			}
		}
	}
	for i := range records {
		if records[i].parentRawID == "" {
			records[i].parentRawID = parentByNode[records[i].rawID]
		}
	}
	currentPath, pathKnown, pathWarnings := deriveCurrentPath(conversation.CurrentNodeID, parentByNode, knownNodes)
	warnings = append(warnings, pathWarnings...)
	sort.Slice(records, func(i, j int) bool {
		if records[i].createdAt != records[j].createdAt {
			if records[i].createdAt == "" {
				return false
			}
			if records[j].createdAt == "" {
				return true
			}
			return records[i].createdAt < records[j].createdAt
		}
		return records[i].rawID < records[j].rawID
	})
	nodeMessages := map[string]string{}
	for i, record := range records {
		messageID := "chatgpt:" + rawID + ":" + record.rawID
		nodeMessages[record.rawID] = messageID
		parentID := ""
		if record.parentRawID != "" {
			parentID = "chatgpt:" + rawID + ":" + record.parentRawID
		}
		conversation.Messages = append(conversation.Messages, archive.Message{
			ID:             messageID,
			Provider:       "chatgpt",
			ConversationID: conversationID,
			RawID:          record.rawID,
			ParentID:       parentID,
			Role:           record.role,
			CreatedAt:      record.createdAt,
			UpdatedAt:      record.updatedAt,
			Ordinal:        i,
			IsCurrentPath:  currentPath[record.rawID],
			IsPathKnown:    pathKnown,
			Text:           record.text,
			RawPayload:     record.rawPayload,
		})
	}
	conversation.Edges = deriveEdges(rawID, conversationID, records, nodeMessages)
	return conversation, warnings, nil
}

func parseNode(raw json.RawMessage, key, conversationID, conversationRawID string) (nodeRecord, []string, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nodeRecord{}, nil, fmt.Errorf("parse chatgpt mapping node %s: %w", redactID(key), err)
	}
	warnings := []string{}
	embeddedID := stringField(object, "id")
	rawID := key
	if rawID == "" && embeddedID != "" {
		rawID = embeddedID
	}
	if rawID == "" {
		rawID = deterministicRawFallback("node", raw)
		warnings = append(warnings, "missing chatgpt node id, used content hash fallback "+redactID(rawID))
	} else if embeddedID != "" && embeddedID != rawID {
		warnings = append(warnings, "chatgpt mapping key differs from embedded node id, used mapping key "+redactID(rawID))
	}
	record := nodeRecord{
		rawID:       rawID,
		parentRawID: stringField(object, "parent"),
		role:        "unknown",
		rawPayload:  raw,
	}
	if rawChildren, ok := object["children"]; ok {
		_ = json.Unmarshal(rawChildren, &record.children)
	}
	if rawMessage, ok := object["message"]; ok && string(rawMessage) != "null" {
		var message map[string]json.RawMessage
		if err := json.Unmarshal(rawMessage, &message); err != nil {
			return nodeRecord{}, nil, fmt.Errorf("parse chatgpt message %s: %w", redactID(rawID), err)
		}
		record.role = messageRole(message)
		record.createdAt = parseTimestamp(message, "create_time", rawID, &warnings)
		record.updatedAt = parseTimestamp(message, "update_time", rawID, &warnings)
		if rawContent, ok := message["content"]; ok {
			record.text = textnorm.Normalize(strings.Join(extractText(rawContent), "\n\n"))
		}
	}
	if record.role == "" {
		record.role = "unknown"
	}
	_ = conversationID
	_ = conversationRawID
	return record, warnings, nil
}

func deriveCurrentPath(current string, parentByNode map[string]string, knownNodes map[string]bool) (map[string]bool, bool, []string) {
	path := map[string]bool{}
	if current == "" {
		return path, false, nil
	}
	warnings := []string{}
	if !knownNodes[current] {
		return path, false, []string{"chatgpt current_node is missing from mapping: " + redactID(current)}
	}
	seen := map[string]bool{}
	for node := current; node != ""; node = parentByNode[node] {
		if seen[node] {
			return map[string]bool{}, false, []string{"cycle detected while deriving chatgpt current path at " + redactID(node)}
		}
		if !knownNodes[node] {
			return map[string]bool{}, false, []string{"chatgpt current path references missing node: " + redactID(node)}
		}
		seen[node] = true
		path[node] = true
	}
	return path, true, warnings
}

func deriveEdges(conversationRawID, conversationID string, records []nodeRecord, nodeMessages map[string]string) []archive.MessageEdge {
	seen := map[string]bool{}
	var edges []archive.MessageEdge
	add := func(parent, child string) {
		if parent == "" || child == "" {
			return
		}
		key := parent + "\x00" + child
		if seen[key] {
			return
		}
		seen[key] = true
		edges = append(edges, archive.MessageEdge{
			Provider:        "chatgpt",
			ConversationID:  conversationID,
			ParentMessageID: messageIDForRaw(conversationRawID, parent),
			ChildMessageID:  messageIDForRaw(conversationRawID, child),
			RawParentID:     parent,
			RawChildID:      child,
			Kind:            "parent_child",
		})
		_ = nodeMessages
	}
	for _, record := range records {
		add(record.parentRawID, record.rawID)
		for _, child := range record.children {
			add(record.rawID, child)
		}
	}
	return edges
}

func messageIDForRaw(conversationRawID, rawID string) string {
	return "chatgpt:" + conversationRawID + ":" + rawID
}

func messageRole(message map[string]json.RawMessage) string {
	var author map[string]json.RawMessage
	if rawAuthor, ok := message["author"]; ok {
		_ = json.Unmarshal(rawAuthor, &author)
	}
	role := stringField(author, "role")
	if role == "" {
		return "unknown"
	}
	return role
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
	var out []string
	for _, key := range []string{"parts", "text", "result", "content", "value", "name"} {
		if value, ok := object[key]; ok {
			out = append(out, extractText(value)...)
		}
	}
	return out
}

func parseTimestamp(object map[string]json.RawMessage, field, rawID string, warnings *[]string) string {
	raw, ok := object[field]
	if !ok || string(raw) == "null" {
		return ""
	}
	var number float64
	if err := json.Unmarshal(raw, &number); err == nil {
		sec, frac := math.Modf(number)
		return timefmt.FormatUTC(time.Unix(int64(sec), int64(frac*1e9)))
	}
	value := stringField(object, field)
	if value == "" {
		*warnings = append(*warnings, "malformed chatgpt timestamp "+field+" for "+redactID(rawID))
		return ""
	}
	if parsedNumber, err := strconv.ParseFloat(value, 64); err == nil {
		sec, frac := math.Modf(parsedNumber)
		return timefmt.FormatUTC(time.Unix(int64(sec), int64(frac*1e9)))
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return timefmt.FormatUTC(parsed)
		}
	}
	*warnings = append(*warnings, "malformed chatgpt timestamp "+field+" for "+redactID(rawID))
	return ""
}

func stringField(object map[string]json.RawMessage, key string) string {
	if object == nil {
		return ""
	}
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

func deterministicRawFallback(scope string, raw json.RawMessage) string {
	sum := sha256.Sum256(append([]byte(scope+"\x00"), raw...))
	return "hash-" + fmt.Sprintf("%x", sum[:16])
}

func redactID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:8] + "..."
}
