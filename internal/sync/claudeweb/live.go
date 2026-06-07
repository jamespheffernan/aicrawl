package claudeweb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

const (
	defaultMaxConversations = 50
	defaultPageSize         = 50
	claudeOrigin            = "https://claude.ai"
)

type Fetcher interface {
	Fetch(ctx context.Context, requestURL string) (FetchResponse, error)
}

type FetchResponse struct {
	Status int
	URL    string
	Body   []byte
}

type LiveOptions struct {
	MaxConversations int
	PageSize         int
}

func FetchLive(ctx context.Context, fetcher Fetcher, opts LiveOptions) ([]byte, error) {
	if fetcher == nil {
		return nil, fmt.Errorf("Claude live fetcher is required")
	}
	maxConversations := bounded(opts.MaxConversations, defaultMaxConversations, 1, 1000)
	pageSize := bounded(opts.PageSize, defaultPageSize, 1, 100)
	orgID, err := fetchOrganizationID(ctx, fetcher)
	if err != nil {
		return nil, err
	}
	var conversations []json.RawMessage
	seen := map[string]bool{}
	for offset := 0; len(conversations) < maxConversations; offset += pageSize {
		limit := min(pageSize, maxConversations-len(conversations))
		listURL := fmt.Sprintf("%s/api/organizations/%s/chat_conversations?limit=%d&offset=%d", claudeOrigin, url.PathEscape(orgID), limit, offset)
		listResp, err := fetcher.Fetch(ctx, listURL)
		if err != nil {
			return nil, fmt.Errorf("fetch Claude conversation list: %w", err)
		}
		ids, err := extractConversationIDs(listResp.Body, "chat_conversations", "conversations")
		if err != nil {
			return nil, fmt.Errorf("parse Claude conversation list: %w", err)
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if id == "" || seen[id] || len(conversations) >= maxConversations {
				continue
			}
			seen[id] = true
			detailURL := claudeOrigin + "/api/organizations/" + url.PathEscape(orgID) + "/chat_conversations/" + url.PathEscape(id)
			detailResp, err := fetcher.Fetch(ctx, detailURL)
			if err != nil {
				return nil, fmt.Errorf("fetch Claude conversation detail: %w", err)
			}
			raw, err := unwrapConversation(detailResp.Body, "chat_messages")
			if err != nil {
				return nil, fmt.Errorf("parse Claude conversation detail: %w", err)
			}
			conversations = append(conversations, raw)
		}
		if len(ids) < limit {
			break
		}
	}
	if len(conversations) == 0 {
		return nil, fmt.Errorf("Claude live sync found no importable conversations")
	}
	return marshalRawArray(conversations), nil
}

func fetchOrganizationID(ctx context.Context, fetcher Fetcher) (string, error) {
	resp, err := fetcher.Fetch(ctx, claudeOrigin+"/api/organizations")
	if err != nil {
		return "", fmt.Errorf("fetch Claude organizations: %w", err)
	}
	ids, err := extractConversationIDs(resp.Body, "organizations")
	if err != nil {
		return "", fmt.Errorf("parse Claude organizations: %w", err)
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("Claude organizations response did not include an organization id")
	}
	return ids[0], nil
}

func extractConversationIDs(data []byte, arrayKeys ...string) ([]string, error) {
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	rawItems := extractArray(payload, arrayKeys...)
	ids := make([]string, 0, len(rawItems))
	for _, raw := range rawItems {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			continue
		}
		for _, key := range []string{"uuid", "id", "conversation_id"} {
			if id := stringField(object, key); id != "" {
				ids = append(ids, id)
				break
			}
		}
	}
	return ids, nil
}

func extractArray(payload any, keys ...string) []json.RawMessage {
	switch value := payload.(type) {
	case []any:
		out := make([]json.RawMessage, 0, len(value))
		for _, item := range value {
			raw, err := json.Marshal(item)
			if err == nil {
				out = append(out, raw)
			}
		}
		return out
	case map[string]any:
		for _, key := range keys {
			if child, ok := value[key]; ok {
				return extractArray(child, keys...)
			}
		}
		for _, key := range []string{"data", "results"} {
			if child, ok := value[key]; ok {
				return extractArray(child, keys...)
			}
		}
	}
	return nil
}

func unwrapConversation(data []byte, requiredKey string) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	if _, ok := object[requiredKey]; ok {
		return append(json.RawMessage(nil), data...), nil
	}
	for _, key := range []string{"conversation", "data"} {
		if raw, ok := object[key]; ok {
			var child map[string]json.RawMessage
			if json.Unmarshal(raw, &child) == nil {
				if _, ok := child[requiredKey]; ok {
					return append(json.RawMessage(nil), raw...), nil
				}
			}
		}
	}
	return nil, fmt.Errorf("detail payload is missing %q", requiredKey)
}

func stringField(object map[string]json.RawMessage, key string) string {
	raw, ok := object[key]
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

func marshalRawArray(items []json.RawMessage) []byte {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(item)
	}
	buf.WriteByte(']')
	return buf.Bytes()
}

func bounded(value, fallback, minValue, maxValue int) int {
	if value == 0 {
		value = fallback
	}
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
