package cdp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"nhooyr.io/websocket"
)

type Options struct {
	Endpoint       string
	HomeURL        string
	AllowedOrigins []string
	HTTPClient     *http.Client
}

type Session struct {
	conn           *websocket.Conn
	allowedOrigins []string
	nextID         int
}

type Response struct {
	Status int
	URL    string
	Body   []byte
}

type targetInfo struct {
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

type command struct {
	ID     int    `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type commandError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type runtimeEvaluateResponse struct {
	ID     int           `json:"id"`
	Result evalResult    `json:"result"`
	Error  *commandError `json:"error,omitempty"`
}

type evalResult struct {
	Result           remoteObject    `json:"result"`
	ExceptionDetails json.RawMessage `json:"exceptionDetails,omitempty"`
}

type remoteObject struct {
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value,omitempty"`
}

type fetchValue struct {
	Status int    `json:"status"`
	URL    string `json:"url"`
	Text   string `json:"text"`
}

func Open(ctx context.Context, opts Options) (*Session, error) {
	if strings.TrimSpace(opts.Endpoint) == "" {
		return nil, fmt.Errorf("CDP endpoint is required")
	}
	if len(opts.AllowedOrigins) == 0 {
		return nil, fmt.Errorf("at least one allowed origin is required")
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	wsURL, err := resolveWebSocketURL(ctx, httpClient, opts)
	if err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{HTTPClient: httpClient})
	if err != nil {
		return nil, fmt.Errorf("connect CDP websocket: %w", err)
	}
	return &Session{conn: conn, allowedOrigins: normalizedOrigins(opts.AllowedOrigins)}, nil
}

func (s *Session) Close(status websocket.StatusCode, reason string) error {
	if s == nil || s.conn == nil {
		return nil
	}
	return s.conn.Close(status, reason)
}

func (s *Session) Fetch(ctx context.Context, requestURL string) (Response, error) {
	if s == nil || s.conn == nil {
		return Response{}, fmt.Errorf("CDP session is not open")
	}
	if !originAllowed(requestURL, s.allowedOrigins) {
		return Response{}, fmt.Errorf("refusing CDP fetch for origin outside provider allowlist")
	}
	urlLiteral, err := json.Marshal(requestURL)
	if err != nil {
		return Response{}, fmt.Errorf("encode fetch URL: %w", err)
	}
	expression := fmt.Sprintf(`(async () => {
  const response = await fetch(%s, {
    credentials: "include",
    headers: {"accept": "application/json"}
  });
  const text = await response.text();
  return {status: response.status, url: response.url, text};
})()`, string(urlLiteral))
	s.nextID++
	id := s.nextID
	req := command{
		ID:     id,
		Method: "Runtime.evaluate",
		Params: map[string]any{
			"expression":    expression,
			"awaitPromise":  true,
			"returnByValue": true,
		},
	}
	data, err := json.Marshal(req)
	if err != nil {
		return Response{}, fmt.Errorf("encode CDP command: %w", err)
	}
	if err := s.conn.Write(ctx, websocket.MessageText, data); err != nil {
		return Response{}, fmt.Errorf("write CDP command: %w", err)
	}
	for {
		messageType, data, err := s.conn.Read(ctx)
		if err != nil {
			return Response{}, fmt.Errorf("read CDP response: %w", err)
		}
		if messageType != websocket.MessageText {
			continue
		}
		var resp runtimeEvaluateResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			return Response{}, fmt.Errorf("decode CDP response: %w", err)
		}
		if resp.ID != id {
			continue
		}
		if resp.Error != nil {
			return Response{}, fmt.Errorf("CDP Runtime.evaluate failed: %s", resp.Error.Message)
		}
		if len(resp.Result.ExceptionDetails) > 0 {
			return Response{}, fmt.Errorf("CDP Runtime.evaluate raised a page exception")
		}
		var value fetchValue
		if len(resp.Result.Result.Value) == 0 {
			return Response{}, fmt.Errorf("CDP fetch returned no value")
		}
		if err := json.Unmarshal(resp.Result.Result.Value, &value); err != nil {
			return Response{}, fmt.Errorf("decode CDP fetch value: %w", err)
		}
		if value.Status < 200 || value.Status >= 300 {
			return Response{}, fmt.Errorf("provider fetch returned HTTP status %d", value.Status)
		}
		return Response{Status: value.Status, URL: value.URL, Body: []byte(value.Text)}, nil
	}
}

func resolveWebSocketURL(ctx context.Context, httpClient *http.Client, opts Options) (string, error) {
	parsed, err := url.Parse(opts.Endpoint)
	if err != nil {
		return "", fmt.Errorf("parse CDP endpoint: %w", err)
	}
	switch parsed.Scheme {
	case "ws", "wss":
		return parsed.String(), nil
	case "http", "https":
	default:
		return "", fmt.Errorf("CDP endpoint must use http, https, ws, or wss")
	}
	targets, err := listTargets(ctx, httpClient, parsed)
	if err != nil {
		return "", err
	}
	origins := normalizedOrigins(opts.AllowedOrigins)
	for _, target := range targets {
		if target.Type == "page" && target.WebSocketDebuggerURL != "" && originAllowed(target.URL, origins) {
			return target.WebSocketDebuggerURL, nil
		}
	}
	if strings.TrimSpace(opts.HomeURL) == "" {
		return "", fmt.Errorf("no provider page target found in CDP browser")
	}
	opened, err := openTarget(ctx, httpClient, parsed, opts.HomeURL)
	if err != nil {
		return "", err
	}
	if opened.WebSocketDebuggerURL != "" {
		return opened.WebSocketDebuggerURL, nil
	}
	targets, err = listTargets(ctx, httpClient, parsed)
	if err != nil {
		return "", err
	}
	for _, target := range targets {
		if target.Type == "page" && target.WebSocketDebuggerURL != "" && originAllowed(target.URL, origins) {
			return target.WebSocketDebuggerURL, nil
		}
	}
	return "", fmt.Errorf("no attachable provider page target found in CDP browser")
}

func listTargets(ctx context.Context, httpClient *http.Client, endpoint *url.URL) ([]targetInfo, error) {
	listURL := *endpoint
	listURL.Path = "/json/list"
	listURL.RawQuery = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list CDP targets: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list CDP targets returned HTTP status %d", resp.StatusCode)
	}
	var targets []targetInfo
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, fmt.Errorf("decode CDP targets: %w", err)
	}
	return targets, nil
}

func openTarget(ctx context.Context, httpClient *http.Client, endpoint *url.URL, homeURL string) (targetInfo, error) {
	openURL := *endpoint
	openURL.Path = "/json/new"
	openURL.RawQuery = url.QueryEscape(homeURL)
	for _, method := range []string{http.MethodPut, http.MethodGet} {
		req, err := http.NewRequestWithContext(ctx, method, openURL.String(), nil)
		if err != nil {
			return targetInfo{}, err
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return targetInfo{}, fmt.Errorf("open CDP target: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusMethodNotAllowed && method == http.MethodPut {
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return targetInfo{}, fmt.Errorf("open CDP target returned HTTP status %d", resp.StatusCode)
		}
		var target targetInfo
		if err := json.NewDecoder(resp.Body).Decode(&target); err != nil {
			return targetInfo{}, fmt.Errorf("decode opened CDP target: %w", err)
		}
		return target, nil
	}
	return targetInfo{}, fmt.Errorf("open CDP target failed")
}

func normalizedOrigins(origins []string) []string {
	out := make([]string, 0, len(origins))
	for _, raw := range origins {
		origin := originOf(raw)
		if origin != "" {
			out = append(out, origin)
		}
	}
	return out
}

func originAllowed(raw string, origins []string) bool {
	origin := originOf(raw)
	if origin == "" {
		return false
	}
	for _, allowed := range origins {
		if origin == allowed {
			return true
		}
	}
	return false
}

func originOf(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}
