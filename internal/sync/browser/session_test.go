package browser

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestBuildSessionPlanReportsLoginRequiredForMissingProfile(t *testing.T) {
	profilePath := filepath.Join(t.TempDir(), "chatgpt-profile")
	plan, err := BuildSessionPlan(SessionOptions{Provider: "chatgpt", ProfilePath: profilePath})
	if err != nil {
		t.Fatalf("BuildSessionPlan: %v", err)
	}
	if plan.SourceKind != "chatgpt_web" {
		t.Fatalf("source kind = %q, want chatgpt_web", plan.SourceKind)
	}
	if plan.AuthState != "login_required" {
		t.Fatalf("auth state = %q, want login_required", plan.AuthState)
	}
	if len(plan.Warnings) == 0 {
		t.Fatalf("warnings are empty, want login guidance")
	}
}

func TestBuildSessionPlanValidatesProviderAndCDPURL(t *testing.T) {
	if _, err := BuildSessionPlan(SessionOptions{Provider: "gemini", ProfilePath: t.TempDir()}); err == nil {
		t.Fatalf("BuildSessionPlan accepted unsupported provider")
	}
	if _, err := BuildSessionPlan(SessionOptions{Provider: "claude", CDPURL: "file:///tmp/browser"}); err == nil {
		t.Fatalf("BuildSessionPlan accepted unsupported CDP URL scheme")
	}
	plan, err := BuildSessionPlan(SessionOptions{Provider: "claude", CDPURL: "http://127.0.0.1:9222"})
	if err != nil {
		t.Fatalf("BuildSessionPlan with CDP URL: %v", err)
	}
	if plan.AuthState != "cdp_attach_configured" {
		t.Fatalf("auth state = %q, want cdp_attach_configured", plan.AuthState)
	}
}

func TestLaunchProfileStartsBrowserWithDedicatedProfile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake browser script uses sh")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"Browser":"fake"}`))
	}))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	_, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatalf("split server host: %v", err)
	}
	browserPath := filepath.Join(t.TempDir(), "fake-browser")
	script := `#!/bin/sh
profile=""
for arg in "$@"; do
  case "$arg" in
    --user-data-dir=*) profile="${arg#--user-data-dir=}" ;;
  esac
done
mkdir -p "$profile"
printf "%s\n/devtools/browser/fake\n" "$FAKE_CDP_PORT" > "$profile/DevToolsActivePort"
sleep 30
`
	if err := os.WriteFile(browserPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake browser: %v", err)
	}
	t.Setenv("FAKE_CDP_PORT", port)
	profilePath := filepath.Join(t.TempDir(), "profile")
	result, err := LaunchProfile(context.Background(), LaunchOptions{
		Provider:    "chatgpt",
		ProfilePath: profilePath,
		BrowserPath: browserPath,
		Wait:        2 * time.Second,
	})
	if err != nil {
		t.Fatalf("LaunchProfile: %v", err)
	}
	defer result.Stop()
	if result.Endpoint != server.URL {
		t.Fatalf("endpoint = %q, want %q", result.Endpoint, server.URL)
	}
	if result.ProfilePath != profilePath {
		t.Fatalf("profile path = %q, want %q", result.ProfilePath, profilePath)
	}
	if result.PID == 0 {
		t.Fatalf("PID is zero")
	}
	if _, err := os.Stat(filepath.Join(profilePath, "DevToolsActivePort")); err != nil {
		t.Fatalf("DevToolsActivePort was not written: %v", err)
	}
}

func TestLaunchProfileReusesExistingProfileEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"Browser":"fake"}`))
	}))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	_, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatalf("split server host: %v", err)
	}
	profilePath := t.TempDir()
	if err := os.WriteFile(filepath.Join(profilePath, "DevToolsActivePort"), []byte(port+"\n/devtools/browser/existing\n"), 0o600); err != nil {
		t.Fatalf("write DevToolsActivePort: %v", err)
	}
	result, err := LaunchProfile(context.Background(), LaunchOptions{
		Provider:    "chatgpt",
		ProfilePath: profilePath,
		BrowserPath: filepath.Join(t.TempDir(), "missing-browser"),
		Wait:        2 * time.Second,
	})
	if err != nil {
		t.Fatalf("LaunchProfile reuse: %v", err)
	}
	if result.Endpoint != server.URL {
		t.Fatalf("endpoint = %q, want %q", result.Endpoint, server.URL)
	}
	if !result.Reused || result.Launched || result.PID != 0 {
		t.Fatalf("result = %+v, want reused existing endpoint without launch", result)
	}
}
