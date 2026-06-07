package browser

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

type ProviderSpec struct {
	ID          string
	DisplayName string
	SourceKind  string
	HomeURL     string
	Origins     []string
}

type SessionOptions struct {
	Provider            string
	ProfilePath         string
	CDPURL              string
	BrowserPath         string
	RemoteDebuggingPort int
}

type SessionPlan struct {
	Provider            string   `json:"provider"`
	DisplayName         string   `json:"display_name"`
	SourceKind          string   `json:"source_kind"`
	HomeURL             string   `json:"home_url"`
	ProfilePath         string   `json:"profile_path,omitempty"`
	CDPURL              string   `json:"cdp_url,omitempty"`
	BrowserPath         string   `json:"browser_path,omitempty"`
	Launched            bool     `json:"launched,omitempty"`
	LaunchPID           int      `json:"launch_pid,omitempty"`
	RemoteDebuggingPort int      `json:"remote_debugging_port,omitempty"`
	AuthState           string   `json:"auth_state"`
	Actions             []string `json:"actions,omitempty"`
	Warnings            []string `json:"warnings,omitempty"`
}

func Provider(value string) (ProviderSpec, error) {
	switch value {
	case "chatgpt":
		return ProviderSpec{
			ID:          "chatgpt",
			DisplayName: "ChatGPT",
			SourceKind:  "chatgpt_web",
			HomeURL:     "https://chatgpt.com/",
			Origins:     []string{"https://chatgpt.com", "https://chat.openai.com"},
		}, nil
	case "claude":
		return ProviderSpec{
			ID:          "claude",
			DisplayName: "Claude",
			SourceKind:  "claude_web",
			HomeURL:     "https://claude.ai/",
			Origins:     []string{"https://claude.ai"},
		}, nil
	default:
		return ProviderSpec{}, fmt.Errorf("--provider must be chatgpt or claude")
	}
}

func BuildSessionPlan(opts SessionOptions) (SessionPlan, error) {
	spec, err := Provider(opts.Provider)
	if err != nil {
		return SessionPlan{}, err
	}
	plan := SessionPlan{
		Provider:            spec.ID,
		DisplayName:         spec.DisplayName,
		SourceKind:          spec.SourceKind,
		HomeURL:             spec.HomeURL,
		ProfilePath:         opts.ProfilePath,
		CDPURL:              opts.CDPURL,
		BrowserPath:         opts.BrowserPath,
		RemoteDebuggingPort: opts.RemoteDebuggingPort,
		Actions: []string{
			"Use a dedicated authenticated browser profile for this provider.",
			"Replay provider data calls only from the authenticated page context.",
			"Do not copy cookies, bearer tokens, or session headers into aicrawl config.",
		},
	}
	if strings.TrimSpace(opts.CDPURL) != "" {
		if err := validateCDPURL(opts.CDPURL); err != nil {
			return SessionPlan{}, err
		}
		plan.AuthState = "cdp_attach_configured"
		return plan, nil
	}
	if strings.TrimSpace(opts.ProfilePath) == "" {
		return SessionPlan{}, fmt.Errorf("--profile is required when --cdp-url is not set")
	}
	info, err := os.Stat(opts.ProfilePath)
	switch {
	case err == nil && info.IsDir():
		plan.AuthState = "profile_available"
	case err == nil && !info.IsDir():
		return SessionPlan{}, fmt.Errorf("browser profile path exists and is not a directory: %s", opts.ProfilePath)
	case os.IsNotExist(err):
		plan.AuthState = "login_required"
		plan.Warnings = append(plan.Warnings, "browser profile does not exist yet; launch the browser with this profile and log in normally")
	default:
		return SessionPlan{}, fmt.Errorf("inspect browser profile: %w", err)
	}
	return plan, nil
}

func validateCDPURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("--cdp-url is not a valid URL: %w", err)
	}
	switch parsed.Scheme {
	case "http", "https", "ws", "wss":
	default:
		return fmt.Errorf("--cdp-url must use http, https, ws, or wss")
	}
	if parsed.Host == "" {
		return fmt.Errorf("--cdp-url must include a host")
	}
	return nil
}
