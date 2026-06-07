package webdiscover

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/openclaw/aicrawl/internal/sync/browser"
)

type Endpoint struct {
	Kind   string `json:"kind"`
	Method string `json:"method"`
	URL    string `json:"url"`
	Status int    `json:"status,omitempty"`
}

type Report struct {
	Provider         string     `json:"provider"`
	SourceKind       string     `json:"source_kind"`
	State            string     `json:"state"`
	RequestsScanned  int        `json:"requests_scanned"`
	ProviderRequests int        `json:"provider_requests"`
	ListEndpoints    []Endpoint `json:"list_endpoints,omitempty"`
	DetailEndpoints  []Endpoint `json:"detail_endpoints,omitempty"`
	Warnings         []string   `json:"warnings,omitempty"`
}

type capturedRequest struct {
	URL    string
	Method string
	Status int
}

func Discover(provider string, r io.Reader) (Report, error) {
	spec, err := browser.Provider(provider)
	if err != nil {
		return Report{}, err
	}
	var payload any
	dec := json.NewDecoder(r)
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return Report{}, fmt.Errorf("decode web capture JSON: %w", err)
	}
	report := Report{
		Provider:   spec.ID,
		SourceKind: spec.SourceKind,
	}
	requests := uniqueRequests(findRequests(payload))
	report.RequestsScanned = len(requests)
	for _, req := range requests {
		kind, sanitized, ok := classify(spec, req.URL)
		if sameProviderOrigin(spec, req.URL) {
			report.ProviderRequests++
		}
		if !ok {
			continue
		}
		endpoint := Endpoint{
			Kind:   kind,
			Method: methodOrDefault(req.Method),
			URL:    sanitized,
			Status: req.Status,
		}
		if kind == "list" {
			report.ListEndpoints = append(report.ListEndpoints, endpoint)
		} else {
			report.DetailEndpoints = append(report.DetailEndpoints, endpoint)
		}
	}
	sortEndpoints(report.ListEndpoints)
	sortEndpoints(report.DetailEndpoints)
	report.State, report.Warnings = contractState(report)
	return report, nil
}

func findRequests(value any) []capturedRequest {
	var out []capturedRequest
	walk(value, &out)
	return out
}

func walk(value any, out *[]capturedRequest) {
	switch typed := value.(type) {
	case []any:
		for _, child := range typed {
			walk(child, out)
		}
	case map[string]any:
		if req, ok := requestFromObject(typed); ok {
			*out = append(*out, req)
		}
		for _, child := range typed {
			walk(child, out)
		}
	}
}

func requestFromObject(obj map[string]any) (capturedRequest, bool) {
	rawURL := firstString(obj, "url", "requestURL")
	method := firstString(obj, "method", "httpMethod")
	status := firstInt(obj, "status", "statusCode")
	if rawURL == "" {
		if req, ok := obj["request"].(map[string]any); ok {
			rawURL = firstString(req, "url", "requestURL")
			if method == "" {
				method = firstString(req, "method", "httpMethod")
			}
		}
	}
	if status == 0 {
		if res, ok := obj["response"].(map[string]any); ok {
			status = firstInt(res, "status", "statusCode")
		}
	}
	if rawURL == "" {
		return capturedRequest{}, false
	}
	return capturedRequest{URL: rawURL, Method: method, Status: status}, true
}

func firstString(obj map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := obj[key].(string); ok {
			value = strings.TrimSpace(value)
			if value != "" {
				return value
			}
		}
	}
	return ""
}

func firstInt(obj map[string]any, keys ...string) int {
	for _, key := range keys {
		switch value := obj[key].(type) {
		case json.Number:
			parsed, _ := value.Int64()
			return int(parsed)
		case float64:
			return int(value)
		case int:
			return value
		}
	}
	return 0
}

func uniqueRequests(requests []capturedRequest) []capturedRequest {
	seen := map[string]bool{}
	out := make([]capturedRequest, 0, len(requests))
	for _, req := range requests {
		key := methodOrDefault(req.Method) + " " + req.URL
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, req)
	}
	return out
}

func classify(spec browser.ProviderSpec, raw string) (string, string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || !originAllowed(spec, parsed) {
		return "", "", false
	}
	path := parsed.EscapedPath()
	switch spec.ID {
	case "chatgpt":
		if path == "/backend-api/conversations" {
			return "list", sanitizedURL(parsed), true
		}
		if strings.HasPrefix(path, "/backend-api/conversation/") {
			return "detail", sanitizedURLWithPath(parsed, "/backend-api/conversation/:id"), true
		}
	case "claude":
		if strings.HasSuffix(path, "/chat_conversations") {
			return "list", sanitizedURLWithPath(parsed, "/api/organizations/:organization_id/chat_conversations"), true
		}
		if strings.Contains(path, "/chat_conversations/") {
			return "detail", sanitizedURLWithPath(parsed, "/api/organizations/:organization_id/chat_conversations/:id"), true
		}
	}
	return "", "", false
}

func sameProviderOrigin(spec browser.ProviderSpec, raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return originAllowed(spec, parsed)
}

func originAllowed(spec browser.ProviderSpec, parsed *url.URL) bool {
	origin := parsed.Scheme + "://" + parsed.Host
	for _, allowed := range spec.Origins {
		if origin == allowed {
			return true
		}
	}
	return false
}

func sanitizedURL(parsed *url.URL) string {
	return sanitizedURLWithPath(parsed, parsed.EscapedPath())
}

func sanitizedURLWithPath(parsed *url.URL, path string) string {
	copy := *parsed
	copy.Path = path
	copy.RawPath = ""
	copy.RawQuery = ""
	copy.Fragment = ""
	copy.User = nil
	return copy.String()
}

func methodOrDefault(method string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		return "GET"
	}
	return method
}

func sortEndpoints(endpoints []Endpoint) {
	sort.Slice(endpoints, func(i, j int) bool {
		if endpoints[i].URL == endpoints[j].URL {
			return endpoints[i].Method < endpoints[j].Method
		}
		return endpoints[i].URL < endpoints[j].URL
	})
}

func contractState(report Report) (string, []string) {
	switch {
	case report.RequestsScanned == 0:
		return "empty", []string{"capture contained no request URLs"}
	case len(report.ListEndpoints) > 0 && len(report.DetailEndpoints) > 0:
		return "matched", nil
	case len(report.ListEndpoints) > 0 || len(report.DetailEndpoints) > 0:
		return "partial", []string{"capture found only part of the expected conversation list/detail contract"}
	case report.ProviderRequests > 0:
		return "stale", []string{"provider traffic was present but expected conversation endpoints were not found"}
	default:
		return "missing", []string{"capture did not include traffic for this provider origin"}
	}
}
