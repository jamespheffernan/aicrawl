package browser

import (
	"path/filepath"
	"testing"
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
