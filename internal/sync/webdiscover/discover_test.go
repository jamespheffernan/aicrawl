package webdiscover

import (
	"strings"
	"testing"
)

func TestDiscoverChatGPTCaptureRedactsQuerySecrets(t *testing.T) {
	capture := `{
		"log": {
			"entries": [
				{
					"request": {
						"method": "GET",
						"url": "https://chatgpt.com/backend-api/conversations?offset=0&access_token=secret"
					},
					"response": {"status": 200}
				},
				{
					"request": {
						"method": "GET",
						"url": "https://chatgpt.com/backend-api/conversation/abc123?access_token=secret"
					},
					"response": {"status": 200}
				},
				{
					"request": {
						"headers": {"authorization": "Bearer secret", "cookie": "session=secret"},
						"url": "https://example.com/ignored"
					}
				}
			]
		}
	}`
	report, err := Discover("chatgpt", strings.NewReader(capture))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if report.State != "matched" {
		t.Fatalf("state = %q, want matched; report = %+v", report.State, report)
	}
	if len(report.ListEndpoints) != 1 || len(report.DetailEndpoints) != 1 {
		t.Fatalf("endpoints = %+v / %+v, want one list and one detail", report.ListEndpoints, report.DetailEndpoints)
	}
	for _, endpoint := range append(report.ListEndpoints, report.DetailEndpoints...) {
		if strings.Contains(endpoint.URL, "access_token") || strings.Contains(endpoint.URL, "secret") || strings.Contains(endpoint.URL, "abc123") {
			t.Fatalf("endpoint URL was not redacted: %s", endpoint.URL)
		}
	}
}

func TestDiscoverClaudeCaptureNestedRequests(t *testing.T) {
	capture := `[
		{
			"message": {
				"method": "Network.requestWillBeSent",
				"params": {
					"request": {
						"method": "GET",
						"url": "https://claude.ai/api/organizations/org_1/chat_conversations?limit=30"
					}
				}
			}
		},
		{
			"message": {
				"method": "Network.requestWillBeSent",
				"params": {
					"request": {
						"method": "GET",
						"url": "https://claude.ai/api/organizations/org_1/chat_conversations/chat_1"
					}
				}
			}
		}
	]`
	report, err := Discover("claude", strings.NewReader(capture))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if report.State != "matched" {
		t.Fatalf("state = %q, want matched; report = %+v", report.State, report)
	}
	if report.ProviderRequests != 2 {
		t.Fatalf("provider requests = %d, want 2", report.ProviderRequests)
	}
	for _, endpoint := range append(report.ListEndpoints, report.DetailEndpoints...) {
		if strings.Contains(endpoint.URL, "org_1") || strings.Contains(endpoint.URL, "chat_1") {
			t.Fatalf("endpoint URL was not redacted: %s", endpoint.URL)
		}
	}
}

func TestDiscoverMarksStaleProviderTraffic(t *testing.T) {
	capture := `[{"request":{"url":"https://chatgpt.com/api/changed-shape","method":"GET"}}]`
	report, err := Discover("chatgpt", strings.NewReader(capture))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if report.State != "stale" {
		t.Fatalf("state = %q, want stale", report.State)
	}
	if len(report.Warnings) == 0 {
		t.Fatalf("warnings are empty, want stale contract warning")
	}
}
