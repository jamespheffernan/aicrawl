package hermessession

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openclaw/aicrawl/internal/archive"
	"github.com/openclaw/aicrawl/internal/ingest/localtext"
	"github.com/openclaw/aicrawl/internal/timefmt"
	_ "modernc.org/sqlite"
)

const (
	Provider   = "hermes"
	SourceKind = "hermes_session"
)

type storeSessionRow struct {
	ID              string
	Source          sql.NullString
	Model           sql.NullString
	ModelConfig     sql.NullString
	SystemPrompt    sql.NullString
	ParentSessionID sql.NullString
	StartedAt       sql.NullFloat64
	EndedAt         sql.NullFloat64
	EndReason       sql.NullString
	MessageCount    sql.NullInt64
	ToolCallCount   sql.NullInt64
	InputTokens     sql.NullInt64
	OutputTokens    sql.NullInt64
	Title           sql.NullString
}

type storeMessageRow struct {
	ID                  int64
	Role                sql.NullString
	Content             sql.NullString
	ToolCallID          sql.NullString
	ToolCalls           sql.NullString
	ToolName            sql.NullString
	Timestamp           sql.NullFloat64
	FinishReason        sql.NullString
	Reasoning           sql.NullString
	ReasoningDetails    sql.NullString
	CodexReasoningItems sql.NullString
}

type parsedConversation struct {
	Conversation archive.Conversation
	Warnings     []string
}

func StreamFile(path string, emit archive.ConversationEmitter) (archive.ParsedSource, error) {
	if emit == nil {
		return archive.ParsedSource{}, fmt.Errorf("Hermes stream callback is required")
	}
	name := strings.ToLower(filepath.Base(path))
	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case name == "state.db":
		return streamStore(path, emit)
	case ext == ".json":
		return streamJSONFile(path, emit)
	case ext == ".jsonl":
		return streamJSONLFile(path, emit)
	default:
		return archive.ParsedSource{}, fmt.Errorf("Hermes source must be state.db, a session JSON file, or a session JSONL file")
	}
}

func streamStore(path string, emit archive.ConversationEmitter) (archive.ParsedSource, error) {
	db, err := openReadOnlyStore(path)
	if err != nil {
		return archive.ParsedSource{}, err
	}
	defer db.Close()
	if err := validateStoreSchema(db); err != nil {
		return archive.ParsedSource{}, err
	}
	sessions, err := readStoreSessions(db)
	if err != nil {
		return archive.ParsedSource{}, err
	}
	var conversations []parsedConversation
	var storeWarnings []string
	for _, session := range sessions {
		conversation, sessionWarnings, ok, err := parseStoreSession(db, session)
		if err != nil {
			return archive.ParsedSource{}, err
		}
		if !ok {
			storeWarnings = append(storeWarnings, "skipped Hermes store session "+localtext.RedactID(session.ID)+" without importable messages")
			continue
		}
		conversations = append(conversations, parsedConversation{Conversation: conversation, Warnings: sessionWarnings})
	}
	if len(conversations) == 0 {
		return archive.ParsedSource{}, fmt.Errorf("Hermes session store contains no importable messages")
	}
	for i, conversation := range conversations {
		warnings := conversation.Warnings
		if i == 0 && len(storeWarnings) > 0 {
			warnings = append(append([]string(nil), storeWarnings...), warnings...)
		}
		if err := emit(conversation.Conversation, warnings); err != nil {
			return archive.ParsedSource{}, err
		}
	}
	return archive.ParsedSource{Provider: Provider, SourceKind: SourceKind}, nil
}

func streamJSONFile(path string, emit archive.ConversationEmitter) (archive.ParsedSource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return archive.ParsedSource{}, fmt.Errorf("read Hermes session JSON: %w", err)
	}
	conversation, warnings, err := parseJSONData(data, filepath.Base(path))
	if err != nil {
		return archive.ParsedSource{}, err
	}
	if err := emit(conversation, warnings); err != nil {
		return archive.ParsedSource{}, err
	}
	return archive.ParsedSource{Provider: Provider, SourceKind: SourceKind}, nil
}

func streamJSONLFile(path string, emit archive.ConversationEmitter) (archive.ParsedSource, error) {
	file, err := os.Open(path)
	if err != nil {
		return archive.ParsedSource{}, fmt.Errorf("open Hermes JSONL: %w", err)
	}
	defer file.Close()
	conversation, warnings, err := parseJSONLReader(file, filepath.Base(path))
	if err != nil {
		return archive.ParsedSource{}, err
	}
	if err := emit(conversation, warnings); err != nil {
		return archive.ParsedSource{}, err
	}
	return archive.ParsedSource{Provider: Provider, SourceKind: SourceKind}, nil
}

func openReadOnlyStore(path string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open Hermes session store read-only: %w", err)
	}
	if _, err := db.Exec(`pragma query_only = on`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable Hermes session store query-only mode: %w", err)
	}
	return db, nil
}

func validateStoreSchema(db *sql.DB) error {
	for _, table := range []string{"sessions", "messages"} {
		var count int
		if err := db.QueryRow(`select count(*) from sqlite_master where type = 'table' and name = ?`, table).Scan(&count); err != nil {
			return fmt.Errorf("inspect Hermes store schema: %w", err)
		}
		if count != 1 {
			return fmt.Errorf("Hermes session store is missing %s table", table)
		}
	}
	return nil
}

func readStoreSessions(db *sql.DB) ([]storeSessionRow, error) {
	rows, err := db.Query(`select
		id, source, model, model_config, system_prompt, parent_session_id,
		started_at, ended_at, end_reason, message_count, tool_call_count,
		input_tokens, output_tokens, title
		from sessions order by coalesce(started_at, 0), id`)
	if err != nil {
		return nil, fmt.Errorf("read Hermes sessions: %w", err)
	}
	defer rows.Close()
	var out []storeSessionRow
	for rows.Next() {
		var row storeSessionRow
		if err := rows.Scan(
			&row.ID, &row.Source, &row.Model, &row.ModelConfig, &row.SystemPrompt, &row.ParentSessionID,
			&row.StartedAt, &row.EndedAt, &row.EndReason, &row.MessageCount, &row.ToolCallCount,
			&row.InputTokens, &row.OutputTokens, &row.Title,
		); err != nil {
			return nil, fmt.Errorf("scan Hermes session: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func parseStoreSession(db *sql.DB, session storeSessionRow) (archive.Conversation, []string, bool, error) {
	rows, err := db.Query(`select
		id, role, content, tool_call_id, tool_calls, tool_name, timestamp,
		finish_reason, reasoning, reasoning_details, codex_reasoning_items
		from messages where session_id = ? order by coalesce(timestamp, 0), id`, session.ID)
	if err != nil {
		return archive.Conversation{}, nil, false, fmt.Errorf("read Hermes messages: %w", err)
	}
	defer rows.Close()
	var messages []archive.Message
	skippedNoText := 0
	for rows.Next() {
		var row storeMessageRow
		if err := rows.Scan(
			&row.ID, &row.Role, &row.Content, &row.ToolCallID, &row.ToolCalls, &row.ToolName, &row.Timestamp,
			&row.FinishReason, &row.Reasoning, &row.ReasoningDetails, &row.CodexReasoningItems,
		); err != nil {
			return archive.Conversation{}, nil, false, fmt.Errorf("scan Hermes message: %w", err)
		}
		message, ok, noText := parseStoreMessage(row, session.ID, len(messages))
		if !ok {
			if noText {
				skippedNoText++
			}
			continue
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return archive.Conversation{}, nil, false, err
	}
	if len(messages) == 0 {
		return archive.Conversation{}, nil, false, nil
	}
	conversationID := Provider + ":" + session.ID
	for i := range messages {
		messages[i].ConversationID = conversationID
	}
	warnings := []string{}
	if skippedNoText > 0 {
		warnings = append(warnings, fmt.Sprintf("skipped %d Hermes store messages without visible text in session %s", skippedNoText, localtext.RedactID(session.ID)))
	}
	updatedAt := unixFloatTime(session.EndedAt)
	if updatedAt == "" {
		updatedAt = messages[len(messages)-1].CreatedAt
	}
	return archive.Conversation{
		ID:         conversationID,
		Provider:   Provider,
		RawID:      session.ID,
		Title:      hermesTitle(session.Title.String, session.Source.String, session.ID),
		CreatedAt:  unixFloatTime(session.StartedAt),
		UpdatedAt:  updatedAt,
		RawPayload: marshalStoreSession(session),
		Messages:   messages,
	}, warnings, true, nil
}

func parseStoreMessage(row storeMessageRow, sessionID string, ordinal int) (archive.Message, bool, bool) {
	role := strings.TrimSpace(row.Role.String)
	if role == "session_meta" {
		return archive.Message{}, false, false
	}
	text := strings.TrimSpace(row.Content.String)
	if text == "" {
		return archive.Message{}, false, role != ""
	}
	rawID := fmt.Sprintf("message-%d", row.ID)
	sender := role
	if role == "tool" && strings.TrimSpace(row.ToolName.String) != "" {
		sender = "tool:" + strings.TrimSpace(row.ToolName.String)
	}
	return archive.Message{
		ID:            fmt.Sprintf("%s:%s:%s", Provider, sessionID, rawID),
		Provider:      Provider,
		RawID:         rawID,
		Role:          normalizeRole(role),
		Sender:        sender,
		CreatedAt:     unixFloatTime(row.Timestamp),
		Ordinal:       ordinal,
		IsCurrentPath: true,
		IsPathKnown:   false,
		Text:          text,
		RawPayload:    marshalStoreMessage(row),
	}, true, false
}

func parseJSONData(data []byte, fallbackID string) (archive.Conversation, []string, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return archive.Conversation{}, nil, fmt.Errorf("parse Hermes session JSON: %w", err)
	}
	var rawMessages []json.RawMessage
	if raw, ok := object["messages"]; ok {
		if err := json.Unmarshal(raw, &rawMessages); err != nil {
			return archive.Conversation{}, nil, fmt.Errorf("Hermes messages field is not an array: %w", err)
		}
	}
	if len(rawMessages) == 0 {
		return archive.Conversation{}, nil, fmt.Errorf("Hermes session JSON contains no importable messages")
	}
	rawID := stringFieldAny(object, "session_id", "sessionId")
	if rawID == "" {
		rawID = strings.TrimSuffix(fallbackID, filepath.Ext(fallbackID))
	}
	platform := localtext.StringField(object, "platform")
	var messages []archive.Message
	skippedNoText := 0
	for i, raw := range rawMessages {
		message, ok, noText := parseJSONMessage(raw, rawID, i)
		if !ok {
			if noText {
				skippedNoText++
			}
			continue
		}
		messages = append(messages, message)
	}
	if len(messages) == 0 {
		return archive.Conversation{}, nil, fmt.Errorf("Hermes session JSON contains no importable messages")
	}
	conversationID := Provider + ":" + rawID
	for i := range messages {
		messages[i].ConversationID = conversationID
	}
	warnings := []string{}
	if skippedNoText > 0 {
		warnings = append(warnings, fmt.Sprintf("skipped %d Hermes JSON messages without visible text", skippedNoText))
	}
	updatedAt := localtext.TimeField(object, "last_updated")
	if updatedAt == "" {
		updatedAt = messages[len(messages)-1].CreatedAt
	}
	return archive.Conversation{
		ID:         conversationID,
		Provider:   Provider,
		RawID:      rawID,
		Title:      hermesTitle("", platform, rawID),
		CreatedAt:  localtext.TimeField(object, "session_start"),
		UpdatedAt:  updatedAt,
		RawPayload: data,
		Messages:   messages,
	}, warnings, nil
}

func parseJSONMessage(raw json.RawMessage, sessionID string, ordinal int) (archive.Message, bool, bool) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return archive.Message{}, false, false
	}
	role := localtext.StringField(object, "role")
	if role == "session_meta" {
		return archive.Message{}, false, false
	}
	text := ""
	if rawContent, ok := object["content"]; ok {
		text = localtext.Text(rawContent)
	}
	if text == "" {
		return archive.Message{}, false, role != ""
	}
	rawID := localtext.StringField(object, "id")
	if rawID == "" {
		rawID = fmt.Sprintf("message-%d", ordinal+1)
	}
	return archive.Message{
		ID:            fmt.Sprintf("%s:%s:%s", Provider, sessionID, rawID),
		Provider:      Provider,
		RawID:         rawID,
		Role:          normalizeRole(role),
		Sender:        senderName(role, object),
		CreatedAt:     localtext.TimeField(object, "timestamp"),
		Ordinal:       ordinal,
		IsCurrentPath: true,
		IsPathKnown:   false,
		Text:          text,
		RawPayload:    raw,
	}, true, false
}

func parseJSONLReader(r io.Reader, fallbackID string) (archive.Conversation, []string, error) {
	reader := bufio.NewReader(r)
	rawID := strings.TrimSuffix(fallbackID, filepath.Ext(fallbackID))
	platform := ""
	var rawConversation json.RawMessage
	var messages []archive.Message
	skippedNoText := 0
	for lineNumber := 1; ; lineNumber++ {
		line, err := reader.ReadBytes('\n')
		if err != nil && len(line) == 0 {
			if err == io.EOF {
				break
			}
			return archive.Conversation{}, nil, fmt.Errorf("read Hermes JSONL: %w", err)
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
			return archive.Conversation{}, nil, fmt.Errorf("parse Hermes JSONL line %d: %w", lineNumber, err)
		}
		if platform == "" {
			platform = localtext.StringField(object, "platform")
		}
		if nextRawID := stringFieldAny(object, "session_id", "sessionId"); nextRawID != "" {
			rawID = nextRawID
		}
		if len(rawConversation) == 0 && !hasContentField(object) {
			rawConversation = append(rawConversation[:0], line...)
		}
		message, ok, noText := parseJSONLMessage(object, rawID, lineNumber, line)
		if !ok {
			if noText {
				skippedNoText++
			}
			if err == io.EOF {
				break
			}
			continue
		}
		message.Ordinal = len(messages)
		messages = append(messages, message)
		if err == io.EOF {
			break
		}
	}
	if len(messages) == 0 {
		return archive.Conversation{}, nil, fmt.Errorf("Hermes JSONL contains no importable messages")
	}
	conversationID := Provider + ":" + rawID
	for i := range messages {
		messages[i].ConversationID = conversationID
	}
	if len(rawConversation) == 0 {
		rawConversation = []byte(`{}`)
	}
	warnings := []string{}
	if skippedNoText > 0 {
		warnings = append(warnings, fmt.Sprintf("skipped %d Hermes JSONL messages without visible text", skippedNoText))
	}
	return archive.Conversation{
		ID:         conversationID,
		Provider:   Provider,
		RawID:      rawID,
		Title:      hermesTitle("", platform, rawID),
		CreatedAt:  messages[0].CreatedAt,
		UpdatedAt:  messages[len(messages)-1].CreatedAt,
		RawPayload: rawConversation,
		Messages:   messages,
	}, warnings, nil
}

func parseJSONLMessage(object map[string]json.RawMessage, sessionID string, lineNumber int, raw json.RawMessage) (archive.Message, bool, bool) {
	role := localtext.StringField(object, "role")
	if role == "session_meta" {
		return archive.Message{}, false, false
	}
	if !hasContentField(object) {
		return archive.Message{}, false, false
	}
	text := localtext.Text(object["content"])
	if text == "" {
		return archive.Message{}, false, role != ""
	}
	rawID := localtext.StringField(object, "id")
	if rawID == "" {
		rawID = fmt.Sprintf("line-%d", lineNumber)
	}
	return archive.Message{
		ID:            fmt.Sprintf("%s:%s:%s", Provider, sessionID, rawID),
		Provider:      Provider,
		RawID:         rawID,
		Role:          normalizeRole(role),
		Sender:        senderName(role, object),
		CreatedAt:     localtext.TimeField(object, "timestamp"),
		IsCurrentPath: true,
		IsPathKnown:   false,
		Text:          text,
		RawPayload:    raw,
	}, true, false
}

func hasContentField(object map[string]json.RawMessage) bool {
	_, ok := object["content"]
	return ok
}

func stringFieldAny(object map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if value := localtext.StringField(object, key); value != "" {
			return value
		}
	}
	return ""
}

func senderName(role string, object map[string]json.RawMessage) string {
	if role == "tool" {
		if toolName := localtext.StringField(object, "tool_name"); toolName != "" {
			return "tool:" + toolName
		}
	}
	return role
}

func normalizeRole(role string) string {
	switch role {
	case "user", "assistant", "system", "developer", "tool":
		return role
	default:
		return "unknown"
	}
}

func hermesTitle(title, source, rawID string) string {
	if strings.TrimSpace(title) != "" {
		return strings.TrimSpace(title)
	}
	if strings.TrimSpace(source) != "" {
		return "Hermes " + strings.TrimSpace(source) + " session " + localtext.RedactID(rawID)
	}
	return "Hermes session " + localtext.RedactID(rawID)
}

func unixFloatTime(value sql.NullFloat64) string {
	if !value.Valid || value.Float64 == 0 || math.IsNaN(value.Float64) || math.IsInf(value.Float64, 0) {
		return ""
	}
	seconds, fraction := math.Modf(value.Float64)
	return timefmt.FormatUTC(time.Unix(int64(seconds), int64(fraction*1e9)))
}

func marshalStoreSession(row storeSessionRow) []byte {
	data, err := json.Marshal(map[string]any{
		"id":                row.ID,
		"source":            nullString(row.Source),
		"model":             nullString(row.Model),
		"model_config":      nullString(row.ModelConfig),
		"system_prompt":     nullString(row.SystemPrompt),
		"parent_session_id": nullString(row.ParentSessionID),
		"started_at":        nullFloat(row.StartedAt),
		"ended_at":          nullFloat(row.EndedAt),
		"end_reason":        nullString(row.EndReason),
		"message_count":     nullInt(row.MessageCount),
		"tool_call_count":   nullInt(row.ToolCallCount),
		"input_tokens":      nullInt(row.InputTokens),
		"output_tokens":     nullInt(row.OutputTokens),
		"title":             nullString(row.Title),
	})
	if err != nil {
		return []byte(`{}`)
	}
	return data
}

func marshalStoreMessage(row storeMessageRow) []byte {
	data, err := json.Marshal(map[string]any{
		"id":                    row.ID,
		"role":                  nullString(row.Role),
		"content":               nullString(row.Content),
		"tool_call_id":          nullString(row.ToolCallID),
		"tool_calls":            nullString(row.ToolCalls),
		"tool_name":             nullString(row.ToolName),
		"timestamp":             nullFloat(row.Timestamp),
		"finish_reason":         nullString(row.FinishReason),
		"reasoning":             nullString(row.Reasoning),
		"reasoning_details":     nullString(row.ReasoningDetails),
		"codex_reasoning_items": nullString(row.CodexReasoningItems),
	})
	if err != nil {
		return []byte(`{}`)
	}
	return data
}

func nullString(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

func nullFloat(value sql.NullFloat64) any {
	if !value.Valid {
		return nil
	}
	return value.Float64
}

func nullInt(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}
