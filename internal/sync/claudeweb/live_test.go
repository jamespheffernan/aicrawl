package claudeweb

import (
	"context"
	"encoding/json"
	"testing"
)

type fakeFetcher map[string][]byte

func (f fakeFetcher) Fetch(ctx context.Context, requestURL string) (FetchResponse, error) {
	body, ok := f[requestURL]
	if !ok {
		return FetchResponse{}, errMissingFixture(requestURL)
	}
	return FetchResponse{Status: 200, URL: requestURL, Body: body}, nil
}

type fakeStatusFetcher map[string]FetchResponse

func (f fakeStatusFetcher) Fetch(ctx context.Context, requestURL string) (FetchResponse, error) {
	resp, ok := f[requestURL]
	if !ok {
		return FetchResponse{}, errMissingFixture(requestURL)
	}
	resp.URL = requestURL
	return resp, nil
}

type errMissingFixture string

func (e errMissingFixture) Error() string { return "missing fixture for " + string(e) }

func TestFetchLiveBuildsClaudeDetailArray(t *testing.T) {
	payload, err := FetchLive(context.Background(), fakeFetcher{
		"https://claude.ai/api/organizations":                                           []byte(`[{"uuid":"org-1"}]`),
		"https://claude.ai/api/organizations/org-1/chat_conversations?limit=2&offset=0": []byte(`{"chat_conversations":[{"uuid":"claude-live-1"},{"uuid":"claude-live-2"}]}`),
		"https://claude.ai/api/organizations/org-1/chat_conversations/claude-live-1":    []byte(claudeDetail("claude-live-1", "one")),
		"https://claude.ai/api/organizations/org-1/chat_conversations/claude-live-2":    []byte(claudeDetail("claude-live-2", "two")),
	}, LiveOptions{MaxConversations: 2, PageSize: 2})
	if err != nil {
		t.Fatalf("FetchLive: %v", err)
	}
	var conversations []map[string]any
	if err := json.Unmarshal(payload, &conversations); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(conversations) != 2 || conversations[0]["uuid"] != "claude-live-1" || conversations[1]["uuid"] != "claude-live-2" {
		t.Fatalf("conversations = %+v", conversations)
	}
}

func TestInspectLiveCountsClaudeListCandidatesWithoutDetails(t *testing.T) {
	inspection, err := InspectLive(context.Background(), fakeStatusFetcher{
		"https://claude.ai/api/organizations": {
			Status: 200,
			Body:   []byte(`[{"uuid":"org-1"}]`),
		},
		"https://claude.ai/api/organizations/org-1/chat_conversations?limit=2&offset=0": {
			Status: 200,
			Body:   []byte(`{"chat_conversations":[{"uuid":"claude-live-1"},{"uuid":"claude-live-2"}]}`),
		},
	}, LiveOptions{MaxConversations: 2, PageSize: 2})
	if err != nil {
		t.Fatalf("InspectLive: %v", err)
	}
	if inspection.CandidateConversations != 2 || len(inspection.Warnings) != 0 {
		t.Fatalf("inspection = %+v, want two clean candidates", inspection)
	}
}

func TestFetchLiveSkipsInaccessibleClaudeDetails(t *testing.T) {
	payload, err := FetchLive(context.Background(), fakeStatusFetcher{
		"https://claude.ai/api/organizations": {
			Status: 200,
			Body:   []byte(`[{"uuid":"org-1"}]`),
		},
		"https://claude.ai/api/organizations/org-1/chat_conversations?limit=2&offset=0": {
			Status: 200,
			Body:   []byte(`{"chat_conversations":[{"uuid":"claude-live-1"},{"uuid":"claude-live-2"}]}`),
		},
		"https://claude.ai/api/organizations/org-1/chat_conversations/claude-live-1": {
			Status: 200,
			Body:   []byte(claudeDetail("claude-live-1", "one")),
		},
		"https://claude.ai/api/organizations/org-1/chat_conversations/claude-live-2": {
			Status: 403,
			Body:   []byte(`{"error":"forbidden"}`),
		},
		"https://claude.ai/api/organizations/org-1/chat_conversations?limit=1&offset=2": {
			Status: 200,
			Body:   []byte(`{"chat_conversations":[]}`),
		},
	}, LiveOptions{MaxConversations: 2, PageSize: 2})
	if err != nil {
		t.Fatalf("FetchLive: %v", err)
	}
	var conversations []map[string]any
	if err := json.Unmarshal(payload, &conversations); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(conversations) != 1 || conversations[0]["uuid"] != "claude-live-1" {
		t.Fatalf("conversations = %+v, want only accessible detail", conversations)
	}
}

func claudeDetail(id, text string) string {
	return `{
  "uuid": "` + id + `",
  "name": "Synthetic",
  "chat_messages": [
    {"uuid":"message-` + id + `","sender":"assistant","text":"` + text + `"}
  ]
}`
}
