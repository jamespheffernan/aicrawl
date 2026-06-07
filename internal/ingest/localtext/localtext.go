package localtext

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/openclaw/aicrawl/internal/timefmt"
)

func StringField(object map[string]json.RawMessage, key string) string {
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

func TimeField(object map[string]json.RawMessage, key string) string {
	value := StringField(object, key)
	if strings.TrimSpace(value) == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000000Z"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return timefmt.FormatUTC(parsed)
		}
	}
	return ""
}

func Text(raw json.RawMessage) string {
	parts := ExtractText(raw)
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func ExtractText(raw json.RawMessage) []string {
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
			out = append(out, ExtractText(item)...)
		}
		return out
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil
	}
	for _, key := range []string{"text", "content", "message"} {
		if value, ok := object[key]; ok {
			return ExtractText(value)
		}
	}
	return nil
}

func RedactID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:8] + "..."
}
