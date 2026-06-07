package cursorstore

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/openclaw/aicrawl/internal/archive"
	"github.com/openclaw/aicrawl/internal/ingest/localtext"
	_ "modernc.org/sqlite"
)

const (
	Provider   = "cursor"
	SourceKind = "cursor_store"
)

type blobRow struct {
	RowID int64
	ID    string
	Data  []byte
}

type cursorMeta struct {
	AgentID   string `json:"agentId"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
}

func StreamFile(path string, emit archive.ConversationEmitter) (archive.ParsedSource, error) {
	if emit == nil {
		return archive.ParsedSource{}, fmt.Errorf("cursor stream callback is required")
	}
	conversation, warnings, err := parseStore(path)
	if err != nil {
		return archive.ParsedSource{}, err
	}
	if err := emit(conversation, warnings); err != nil {
		return archive.ParsedSource{}, err
	}
	return archive.ParsedSource{Provider: Provider, SourceKind: SourceKind}, nil
}

func parseStore(path string) (archive.Conversation, []string, error) {
	if filepath.Base(path) != "store.db" {
		return archive.Conversation{}, nil, fmt.Errorf("Cursor source must be a store.db SQLite file")
	}
	db, err := openReadOnlyStore(path)
	if err != nil {
		return archive.Conversation{}, nil, err
	}
	defer db.Close()
	if err := validateSchema(db); err != nil {
		return archive.Conversation{}, nil, err
	}
	meta, _ := readMeta(db)
	rawID := strings.TrimSpace(meta.AgentID)
	if rawID == "" {
		rawID = filepath.Base(filepath.Dir(path))
	}
	if rawID == "" || rawID == "." || rawID == string(filepath.Separator) {
		rawID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	title := strings.TrimSpace(meta.Name)
	if title == "" {
		title = "Cursor chat " + localtext.RedactID(rawID)
	}
	rows, err := readBlobRows(db)
	if err != nil {
		return archive.Conversation{}, nil, err
	}
	warnings := []string{}
	var messages []archive.Message
	for _, row := range rows {
		message, ok := parseBlobMessage(row, rawID, len(messages))
		if !ok {
			continue
		}
		messages = append(messages, message)
	}
	if len(messages) == 0 {
		return archive.Conversation{}, nil, fmt.Errorf("Cursor store contains no importable visible messages")
	}
	conversationID := Provider + ":" + rawID
	for i := range messages {
		messages[i].ConversationID = conversationID
	}
	return archive.Conversation{
		ID:         conversationID,
		Provider:   Provider,
		RawID:      rawID,
		Title:      title,
		CreatedAt:  localtext.TimeField(map[string]json.RawMessage{"createdAt": json.RawMessage(strconvQuote(meta.CreatedAt))}, "createdAt"),
		UpdatedAt:  messages[len(messages)-1].CreatedAt,
		RawPayload: marshalMeta(meta),
		Messages:   messages,
	}, warnings, nil
}

func openReadOnlyStore(path string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open Cursor store read-only: %w", err)
	}
	if _, err := db.Exec(`pragma query_only = on`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable Cursor store query-only mode: %w", err)
	}
	return db, nil
}

func validateSchema(db *sql.DB) error {
	for _, table := range []string{"blobs", "meta"} {
		var count int
		if err := db.QueryRow(`select count(*) from sqlite_master where type = 'table' and name = ?`, table).Scan(&count); err != nil {
			return fmt.Errorf("inspect Cursor store schema: %w", err)
		}
		if count != 1 {
			return fmt.Errorf("Cursor store is missing %s table", table)
		}
	}
	return nil
}

func readMeta(db *sql.DB) (cursorMeta, error) {
	var value string
	if err := db.QueryRow(`select value from meta where key = '0'`).Scan(&value); err != nil {
		return cursorMeta{}, err
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return cursorMeta{}, err
	}
	var meta cursorMeta
	if err := json.Unmarshal(decoded, &meta); err != nil {
		return cursorMeta{}, err
	}
	return meta, nil
}

func readBlobRows(db *sql.DB) ([]blobRow, error) {
	rows, err := db.Query(`select rowid, id, data from blobs order by rowid`)
	if err != nil {
		return nil, fmt.Errorf("read Cursor blobs: %w", err)
	}
	defer rows.Close()
	var out []blobRow
	for rows.Next() {
		var row blobRow
		if err := rows.Scan(&row.RowID, &row.ID, &row.Data); err != nil {
			return nil, fmt.Errorf("scan Cursor blob: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func parseBlobMessage(row blobRow, conversationRawID string, ordinal int) (archive.Message, bool) {
	if !json.Valid(row.Data) {
		return archive.Message{}, false
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(row.Data, &object); err != nil {
		return archive.Message{}, false
	}
	role := localtext.StringField(object, "role")
	switch role {
	case "user", "assistant", "system":
	default:
		return archive.Message{}, false
	}
	text := ""
	if rawContent, ok := object["content"]; ok {
		text = visibleMessageText(rawContent)
	}
	if text == "" {
		return archive.Message{}, false
	}
	rawID := localtext.StringField(object, "id")
	if rawID == "" {
		rawID = row.ID
	}
	if rawID == "" {
		rawID = fmt.Sprintf("row-%d", row.RowID)
	}
	return archive.Message{
		ID:            fmt.Sprintf("%s:%s:%s", Provider, conversationRawID, rawID),
		Provider:      Provider,
		RawID:         rawID,
		Role:          normalizeRole(role),
		Sender:        role,
		Ordinal:       ordinal,
		IsCurrentPath: true,
		IsPathKnown:   false,
		Text:          text,
		RawPayload:    row.Data,
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
	default:
		return nil
	}
}

func normalizeRole(role string) string {
	switch role {
	case "user", "assistant", "system":
		return role
	default:
		return "unknown"
	}
}

func marshalMeta(meta cursorMeta) []byte {
	data, err := json.Marshal(meta)
	if err != nil {
		return []byte(`{}`)
	}
	return data
}

func strconvQuote(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(data)
}
