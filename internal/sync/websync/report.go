package websync

import (
	"github.com/openclaw/aicrawl/internal/sync/browser"
	"github.com/openclaw/aicrawl/internal/sync/webdiscover"
)

type Freshness struct {
	State             string `json:"state"`
	SourceKind        string `json:"source_kind"`
	LastImportID      string `json:"last_import_id,omitempty"`
	LastImportAt      string `json:"last_import_at,omitempty"`
	ConversationCount int64  `json:"conversation_count,omitempty"`
	MessageCount      int64  `json:"message_count,omitempty"`
}

type Report struct {
	Provider              string              `json:"provider"`
	SourceKind            string              `json:"source_kind"`
	Mode                  string              `json:"mode"`
	AuthState             string              `json:"auth_state"`
	EndpointContractState string              `json:"endpoint_contract_state"`
	ProfilePath           string              `json:"profile_path,omitempty"`
	CDPURL                string              `json:"cdp_url,omitempty"`
	HomeURL               string              `json:"home_url"`
	Session               browser.SessionPlan `json:"session"`
	Discovery             *webdiscover.Report `json:"discovery,omitempty"`
	Freshness             Freshness           `json:"freshness"`
	PrivacyBoundary       []string            `json:"privacy_boundary"`
	NextActions           []string            `json:"next_actions,omitempty"`
	Warnings              []string            `json:"warnings,omitempty"`
}

func BuildReport(session browser.SessionPlan, discovery *webdiscover.Report, freshness Freshness, dryRun bool) Report {
	mode := "preflight"
	if dryRun {
		mode = "dry_run"
	}
	contractState := "not_checked"
	var warnings []string
	if len(session.Warnings) > 0 {
		warnings = append(warnings, session.Warnings...)
	}
	nextActions := append([]string(nil), session.Actions...)
	if discovery != nil {
		contractState = discovery.State
		warnings = append(warnings, discovery.Warnings...)
		if discovery.State == "matched" {
			nextActions = append(nextActions, "Use this redacted contract to implement provider-specific list/detail sync fixtures.")
		}
	}
	if discovery == nil {
		nextActions = append(nextActions, "Capture a redacted browser network export and pass it with --capture before enabling writes.")
	}
	if freshness.State == "never_synced" {
		nextActions = append(nextActions, "No web sync cursor exists yet; the first write-capable sync should backfill recent conversations.")
	}
	return Report{
		Provider:              session.Provider,
		SourceKind:            session.SourceKind,
		Mode:                  mode,
		AuthState:             session.AuthState,
		EndpointContractState: contractState,
		ProfilePath:           session.ProfilePath,
		CDPURL:                session.CDPURL,
		HomeURL:               session.HomeURL,
		Session:               session,
		Discovery:             discovery,
		Freshness:             freshness,
		PrivacyBoundary: []string{
			"Do not persist cookies, bearer tokens, or request headers.",
			"Keep browser authentication inside the browser profile or attached CDP target.",
			"Store only normalized conversation records, raw provider payloads, source hashes, and sync cursors in the archive.",
		},
		NextActions: nextActions,
		Warnings:    warnings,
	}
}
