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

func TestFetchLiveWithCursorSkipsOlderChatGPTDetails(t *testing.T) {
	result, err := FetchLiveWithCursor(context.Background(), fakeStatusFetcher{
		"https://chatgpt.com/backend-api/conversations?offset=0&limit=2&order=updated": {
			Status: 200,
			Body:   []byte(`{"items":[{"id":"chatgpt-live-old","update_time":1760000000}]}`),
		},
	}, LiveOptions{MaxConversations: 2, PageSize: 2, CursorAfter: "2025-10-09T08:53:20.000000000Z"})
	if err != nil {
		t.Fatalf("FetchLiveWithCursor: %v", err)
	}
	if !result.NoChanges || len(result.Payload) != 0 || result.CandidateConversations != 1 {
		t.Fatalf("result = %+v, want one skipped old candidate and no payload", result)
	}
	if result.Cursor.Kind != "provider_updated_at" || result.Cursor.At == "" {
		t.Fatalf("cursor = %+v, want provider updated cursor", result.Cursor)
	}
}

func TestFetchLiveWithCursorRecordsChatGPTProviderWatermark(t *testing.T) {
	result, err := FetchLiveWithCursor(context.Background(), fakeStatusFetcher{
		"https://chatgpt.com/backend-api/conversations?offset=0&limit=1&order=updated": {
			Status: 200,
			Body:   []byte(`{"items":[{"id":"chatgpt-live-1","update_time":1760000060}]}`),
		},
		"https://chatgpt.com/backend-api/conversation/chatgpt-live-1": {
			Status: 200,
			Body:   []byte(chatGPTDetail("chatgpt-live-1", "one")),
		},
	}, LiveOptions{MaxConversations: 1, PageSize: 1})
	if err != nil {
		t.Fatalf("FetchLiveWithCursor: %v", err)
	}
	if result.NoChanges || result.FetchedConversations != 1 || len(result.Payload) == 0 {
		t.Fatalf("result = %+v, want one fetched conversation", result)
	}
	if result.Cursor.Kind != "provider_updated_at" || result.Cursor.At != "2025-10-09T08:54:20.000000000Z" || result.Cursor.CandidateCount != 1 {
		t.Fatalf("cursor = %+v, want normalized provider watermark", result.Cursor)
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

func TestInspectLiveStopsOnRepeatedChatGPTPage(t *testing.T) {
	inspection, err := InspectLive(context.Background(), fakeStatusFetcher{
		"https://chatgpt.com/backend-api/conversations?offset=0&limit=2&order=updated": {
			Status: 200,
			Body:   []byte(`{"items":[{"id":"chatgpt-live-1"},{"id":"chatgpt-live-2"}]}`),
		},
		"https://chatgpt.com/backend-api/conversations?offset=2&limit=1&order=updated": {
			Status: 200,
			Body:   []byte(`{"items":[{"id":"chatgpt-live-1"},{"id":"chatgpt-live-2"}]}`),
		},
	}, LiveOptions{MaxConversations: 3, PageSize: 2})
	if err != nil {
		t.Fatalf("InspectLive: %v", err)
	}
	if inspection.CandidateConversations != 2 || len(inspection.Warnings) != 0 {
		t.Fatalf("inspection = %+v, want repeated page to stop at two candidates", inspection)
	}
}

func TestFetchLiveSkipsInaccessibleChatGPTDetails(t *testing.T) {
	result, err := FetchLiveWithCursor(context.Background(), fakeStatusFetcher{
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
			Body:   []byte(`{"items":[{"id":"chatgpt-live-1"},{"id":"chatgpt-live-2"}]}`),
		},
	}, LiveOptions{MaxConversations: 2, PageSize: 2})
	if err != nil {
		t.Fatalf("FetchLiveWithCursor: %v", err)
	}
	var conversations []map[string]any
	if err := json.Unmarshal(result.Payload, &conversations); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(conversations) != 1 || conversations[0]["id"] != "chatgpt-live-1" {
		t.Fatalf("conversations = %+v, want only accessible detail", conversations)
	}
	if len(result.SkippedDetails) != 1 || result.SkippedDetails[0].ID != "chatgpt-live-2" || result.SkippedDetails[0].Status != 404 {
		t.Fatalf("skipped details = %+v, want inaccessible second detail", result.SkippedDetails)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("warnings = %+v, want aggregate skipped warning", result.Warnings)
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
