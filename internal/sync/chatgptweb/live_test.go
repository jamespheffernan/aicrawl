package chatgptweb

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

func TestFetchLiveBuildsChatGPTDetailArray(t *testing.T) {
	payload, err := FetchLive(context.Background(), fakeFetcher{
		"https://chatgpt.com/backend-api/conversations?offset=0&limit=2&order=updated": []byte(`{"items":[{"id":"chatgpt-live-1"},{"id":"chatgpt-live-2"}]}`),
		"https://chatgpt.com/backend-api/conversation/chatgpt-live-1":                  []byte(chatGPTDetail("chatgpt-live-1", "one")),
		"https://chatgpt.com/backend-api/conversation/chatgpt-live-2":                  []byte(chatGPTDetail("chatgpt-live-2", "two")),
	}, LiveOptions{MaxConversations: 2, PageSize: 2})
	if err != nil {
		t.Fatalf("FetchLive: %v", err)
	}
	var conversations []map[string]any
	if err := json.Unmarshal(payload, &conversations); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(conversations) != 2 || conversations[0]["id"] != "chatgpt-live-1" || conversations[1]["id"] != "chatgpt-live-2" {
		t.Fatalf("conversations = %+v", conversations)
	}
}

func TestInspectLiveCountsChatGPTListCandidatesWithoutDetails(t *testing.T) {
	inspection, err := InspectLive(context.Background(), fakeStatusFetcher{
		"https://chatgpt.com/backend-api/conversations?offset=0&limit=2&order=updated": {
			Status: 200,
			Body:   []byte(`{"items":[{"id":"chatgpt-live-1"},{"id":"chatgpt-live-2"}]}`),
		},
	}, LiveOptions{MaxConversations: 2, PageSize: 2})
	if err != nil {
		t.Fatalf("InspectLive: %v", err)
	}
	if inspection.CandidateConversations != 2 || len(inspection.Warnings) != 0 {
		t.Fatalf("inspection = %+v, want two clean candidates", inspection)
	}
}

func TestFetchLiveSkipsInaccessibleChatGPTDetails(t *testing.T) {
	payload, err := FetchLive(context.Background(), fakeStatusFetcher{
		"https://chatgpt.com/backend-api/conversations?offset=0&limit=2&order=updated": {
			Status: 200,
			Body:   []byte(`{"items":[{"id":"chatgpt-live-1"},{"id":"chatgpt-live-2"}]}`),
		},
		"https://chatgpt.com/backend-api/conversation/chatgpt-live-1": {
			Status: 200,
			Body:   []byte(chatGPTDetail("chatgpt-live-1", "one")),
		},
		"https://chatgpt.com/backend-api/conversation/chatgpt-live-2": {
			Status: 404,
			Body:   []byte(`{"error":"not found"}`),
		},
		"https://chatgpt.com/backend-api/conversations?offset=2&limit=1&order=updated": {
			Status: 200,
			Body:   []byte(`{"items":[]}`),
		},
	}, LiveOptions{MaxConversations: 2, PageSize: 2})
	if err != nil {
		t.Fatalf("FetchLive: %v", err)
	}
	var conversations []map[string]any
	if err := json.Unmarshal(payload, &conversations); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(conversations) != 1 || conversations[0]["id"] != "chatgpt-live-1" {
		t.Fatalf("conversations = %+v, want only accessible detail", conversations)
	}
}

func chatGPTDetail(id, text string) string {
	return `{
  "id": "` + id + `",
  "title": "Synthetic",
  "mapping": {
    "node": {
      "id": "node",
      "parent": null,
      "children": [],
      "message": {"author": {"role": "assistant"}, "content": {"parts": ["` + text + `"]}}
    }
  }
}`
}
