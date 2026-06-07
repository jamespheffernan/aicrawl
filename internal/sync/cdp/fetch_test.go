package cdp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nhooyr.io/websocket"
)

func TestSessionFetchEvaluatesSameOriginRequest(t *testing.T) {
	var wsURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/list":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"type":                 "page",
				"url":                  "https://chatgpt.com/",
				"webSocketDebuggerUrl": wsURL,
			}})
		case "/devtools/page/1":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "")
			_, data, err := conn.Read(context.Background())
			if err != nil {
				t.Errorf("read command: %v", err)
				return
			}
			var cmd struct {
				ID     int            `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if err := json.Unmarshal(data, &cmd); err != nil {
				t.Errorf("decode command: %v", err)
				return
			}
			if cmd.Method != "Runtime.evaluate" {
				t.Errorf("method = %q, want Runtime.evaluate", cmd.Method)
			}
			expression, _ := cmd.Params["expression"].(string)
			if !strings.Contains(expression, "https://chatgpt.com/backend-api/conversations") {
				t.Errorf("expression did not include fetch URL: %s", expression)
			}
			response := map[string]any{
				"id": cmd.ID,
				"result": map[string]any{
					"result": map[string]any{
						"type": "object",
						"value": map[string]any{
							"status": 200,
							"url":    "https://chatgpt.com/backend-api/conversations",
							"text":   `{"items":[]}`,
						},
					},
				},
			}
			data, _ = json.Marshal(response)
			if err := conn.Write(context.Background(), websocket.MessageText, data); err != nil {
				t.Errorf("write response: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	wsURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/page/1"

	session, err := Open(context.Background(), Options{
		Endpoint:       server.URL,
		HomeURL:        "https://chatgpt.com/",
		AllowedOrigins: []string{"https://chatgpt.com"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer session.Close(websocket.StatusNormalClosure, "")
	resp, err := session.Fetch(context.Background(), "https://chatgpt.com/backend-api/conversations")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if resp.Status != 200 || string(resp.Body) != `{"items":[]}` {
		t.Fatalf("response = %+v", resp)
	}
}

func TestSessionFetchRejectsDisallowedOrigin(t *testing.T) {
	var wsURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/list":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"type":                 "page",
				"url":                  "https://chatgpt.com/",
				"webSocketDebuggerUrl": wsURL,
			}})
		case "/devtools/page/1":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	wsURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/page/1"

	session, err := Open(context.Background(), Options{
		Endpoint:       server.URL,
		HomeURL:        "https://chatgpt.com/",
		AllowedOrigins: []string{"https://chatgpt.com"},
	})
	if err == nil {
		defer session.Close(websocket.StatusNormalClosure, "")
		_, err = session.Fetch(context.Background(), "https://example.invalid/backend-api/conversations")
	}
	if err == nil || !strings.Contains(err.Error(), "outside provider allowlist") {
		t.Fatalf("Fetch error = %v, want allowlist rejection", err)
	}
}
