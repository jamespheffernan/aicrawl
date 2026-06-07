package claudeweb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"time"

	"github.com/openclaw/aicrawl/internal/timefmt"
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
	CursorAfter      string
}

type LiveInspection struct {
	CandidateConversations int
	Warnings               []string
}

type LiveCursor struct {
	Kind           string
	Value          string
	At             string
	CandidateCount int64
}

type SkippedDetail struct {
	ID     string
	Status int
}

type LiveResult struct {
	Payload                []byte
	Cursor                 LiveCursor
	CandidateConversations int
	FetchedConversations   int
	NoChanges              bool
	Warnings               []string
	SkippedDetails         []SkippedDetail
}

func InspectLive(ctx context.Context, fetcher Fetcher, opts LiveOptions) (LiveInspection, error) {
	if fetcher == nil {
		return LiveInspection{}, fmt.Errorf("Claude live fetcher is required")
	}
	maxConversations := bounded(opts.MaxConversations, defaultMaxConversations, 1, 1000)
	pageSize := bounded(opts.PageSize, defaultPageSize, 1, 100)
	orgID, warnings, err := inspectOrganizationID(ctx, fetcher)
	if err != nil {
		return LiveInspection{}, err
	}
	inspection := LiveInspection{Warnings: warnings}
	if orgID == "" {
		return inspection, nil
	}
	seen := map[string]bool{}
	for offset := 0; inspection.CandidateConversations < maxConversations; offset += pageSize {
		limit := min(pageSize, maxConversations-inspection.CandidateConversations)
		listURL := fmt.Sprintf("%s/api/organizations/%s/chat_conversations?limit=%d&offset=%d", claudeOrigin, url.PathEscape(orgID), limit, offset)
		listResp, err := fetcher.Fetch(ctx, listURL)
		if err != nil {
			return LiveInspection{}, fmt.Errorf("fetch Claude conversation list: %w", err)
		}
		if !okStatus(listResp.Status) {
			inspection.Warnings = append(inspection.Warnings, fmt.Sprintf("Claude conversation list returned HTTP status %d; login or endpoint shape may need attention", listResp.Status))
			return inspection, nil
		}
		ids, err := extractConversationIDs(listResp.Body, "chat_conversations", "conversations")
		if err != nil {
			inspection.Warnings = append(inspection.Warnings, "Claude conversation list could not be parsed for candidate IDs: "+err.Error())
			return inspection, nil
		}
		if len(ids) == 0 {
			break
		}
		newCandidates := 0
		for _, id := range ids {
			if id == "" || seen[id] || inspection.CandidateConversations >= maxConversations {
				continue
			}
			seen[id] = true
			newCandidates++
			inspection.CandidateConversations++
		}
		if newCandidates == 0 || len(ids) < limit {
			break
		}
	}
	return inspection, nil
}

func FetchLive(ctx context.Context, fetcher Fetcher, opts LiveOptions) ([]byte, error) {
	result, err := FetchLiveWithCursor(ctx, fetcher, opts)
	if err != nil {
		return nil, err
	}
	if result.NoChanges || len(result.Payload) == 0 {
		return nil, fmt.Errorf("Claude live sync found no importable conversations")
	}
	return result.Payload, nil
}

func FetchLiveWithCursor(ctx context.Context, fetcher Fetcher, opts LiveOptions) (LiveResult, error) {
	if fetcher == nil {
		return LiveResult{}, fmt.Errorf("Claude live fetcher is required")
	}
	maxConversations := bounded(opts.MaxConversations, defaultMaxConversations, 1, 1000)
	pageSize := bounded(opts.PageSize, defaultPageSize, 1, 100)
	orgID, err := fetchOrganizationID(ctx, fetcher)
	if err != nil {
		return LiveResult{}, err
	}
	var conversations []json.RawMessage
	seen := map[string]bool{}
	result := LiveResult{}
	maxObservedAt := ""
	for offset := 0; len(conversations) < maxConversations; offset += pageSize {
		limit := min(pageSize, maxConversations-len(conversations))
		listURL := fmt.Sprintf("%s/api/organizations/%s/chat_conversations?limit=%d&offset=%d", claudeOrigin, url.PathEscape(orgID), limit, offset)
		listResp, err := fetcher.Fetch(ctx, listURL)
		if err != nil {
			return LiveResult{}, fmt.Errorf("fetch Claude conversation list: %w", err)
		}
		if !okStatus(listResp.Status) {
			return LiveResult{}, fmt.Errorf("fetch Claude conversation list returned HTTP status %d", listResp.Status)
		}
		items, err := extractConversationItems(listResp.Body, []string{"updated_at", "created_at"}, "chat_conversations", "conversations")
		if err != nil {
			return LiveResult{}, fmt.Errorf("parse Claude conversation list: %w", err)
		}
		if len(items) == 0 {
			break
		}
		stopAfterPage := false
		newCandidates := 0
		for _, item := range items {
			if item.UpdatedAt != "" && item.UpdatedAt > maxObservedAt {
				maxObservedAt = item.UpdatedAt
			}
			if item.ID == "" || seen[item.ID] || len(conversations) >= maxConversations {
				continue
			}
			seen[item.ID] = true
			newCandidates++
			result.CandidateConversations++
			if opts.CursorAfter != "" && item.UpdatedAt != "" && item.UpdatedAt <= opts.CursorAfter {
				stopAfterPage = true
				continue
			}
			detailURL := claudeOrigin + "/api/organizations/" + url.PathEscape(orgID) + "/chat_conversations/" + url.PathEscape(item.ID)
			detailResp, err := fetcher.Fetch(ctx, detailURL)
			if err != nil {
				return LiveResult{}, fmt.Errorf("fetch Claude conversation detail: %w", err)
			}
			if inaccessibleStatus(detailResp.Status) {
				result.SkippedDetails = append(result.SkippedDetails, SkippedDetail{ID: item.ID, Status: detailResp.Status})
				continue
			}
			if !okStatus(detailResp.Status) {
				return LiveResult{}, fmt.Errorf("fetch Claude conversation detail returned HTTP status %d", detailResp.Status)
			}
			raw, err := unwrapConversation(detailResp.Body, "chat_messages")
			if err != nil {
				return LiveResult{}, fmt.Errorf("parse Claude conversation detail: %w", err)
			}
			if detailAt := conversationTimestamp(raw, []string{"updated_at", "created_at"}); detailAt != "" && detailAt > maxObservedAt {
				maxObservedAt = detailAt
			}
			conversations = append(conversations, raw)
		}
		if stopAfterPage || newCandidates == 0 || len(items) < limit {
			break
		}
	}
	if maxObservedAt != "" {
		result.Cursor = LiveCursor{
			Kind:           "provider_updated_at",
			Value:          maxObservedAt,
			At:             maxObservedAt,
			CandidateCount: int64(result.CandidateConversations),
		}
	}
	if len(result.SkippedDetails) > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("skipped %d inaccessible Claude conversation details", len(result.SkippedDetails)))
	}
	if len(conversations) == 0 {
		if opts.CursorAfter != "" || len(result.SkippedDetails) > 0 {
			result.NoChanges = true
			return result, nil
		}
		return LiveResult{}, fmt.Errorf("Claude live sync found no importable conversations")
	}
	result.Payload = marshalRawArray(conversations)
	result.FetchedConversations = len(conversations)
	return result, nil
}

func fetchOrganizationID(ctx context.Context, fetcher Fetcher) (string, error) {
	resp, err := fetcher.Fetch(ctx, claudeOrigin+"/api/organizations")
	if err != nil {
		return "", fmt.Errorf("fetch Claude organizations: %w", err)
	}
	if !okStatus(resp.Status) {
		return "", fmt.Errorf("fetch Claude organizations returned HTTP status %d", resp.Status)
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

func inspectOrganizationID(ctx context.Context, fetcher Fetcher) (string, []string, error) {
	resp, err := fetcher.Fetch(ctx, claudeOrigin+"/api/organizations")
	if err != nil {
		return "", nil, fmt.Errorf("fetch Claude organizations: %w", err)
	}
	if !okStatus(resp.Status) {
		return "", []string{fmt.Sprintf("Claude organizations returned HTTP status %d; login may be required", resp.Status)}, nil
	}
	ids, err := extractConversationIDs(resp.Body, "organizations")
	if err != nil {
		return "", []string{"Claude organizations response could not be parsed for an organization id: " + err.Error()}, nil
	}
	if len(ids) == 0 {
		return "", []string{"Claude organizations response did not include an organization id"}, nil
	}
	return ids[0], nil, nil
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

type conversationListItem struct {
	ID        string
	UpdatedAt string
}

func extractConversationItems(data []byte, timestampKeys []string, arrayKeys ...string) ([]conversationListItem, error) {
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	rawItems := extractArray(payload, arrayKeys...)
	items := make([]conversationListItem, 0, len(rawItems))
	for _, raw := range rawItems {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			continue
		}
		item := conversationListItem{UpdatedAt: timestampField(object, timestampKeys)}
		for _, key := range []string{"uuid", "id", "conversation_id"} {
			if id := stringField(object, key); id != "" {
				item.ID = id
				break
			}
		}
		if item.ID != "" {
			items = append(items, item)
		}
	}
	return items, nil
}

func conversationTimestamp(raw json.RawMessage, keys []string) string {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return ""
	}
	return timestampField(object, keys)
}

func timestampField(object map[string]json.RawMessage, keys []string) string {
	for _, key := range keys {
		raw, ok := object[key]
		if !ok {
			continue
		}
		if value := timestampRaw(raw); value != "" {
			return value
		}
	}
	return ""
}

func timestampRaw(raw json.RawMessage) string {
	var value string
	if err := json.Unmarshal(raw, &value); err == nil && value != "" {
		if parsedNumber, err := strconv.ParseFloat(value, 64); err == nil {
			sec, frac := math.Modf(parsedNumber)
			return timefmt.FormatUTC(time.Unix(int64(sec), int64(frac*1e9)))
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000000Z"} {
			if parsed, err := time.Parse(layout, value); err == nil {
				return timefmt.FormatUTC(parsed)
			}
		}
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		if parsed, err := strconv.ParseFloat(number.String(), 64); err == nil {
			sec, frac := math.Modf(parsed)
			return timefmt.FormatUTC(time.Unix(int64(sec), int64(frac*1e9)))
		}
	}
	return ""
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

func okStatus(status int) bool {
	return status >= 200 && status < 300
}

func inaccessibleStatus(status int) bool {
	return status == 403 || status == 404 || status == 410
}
