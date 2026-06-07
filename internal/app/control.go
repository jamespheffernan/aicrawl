package app

import (
	"path/filepath"

	"github.com/openclaw/crawlkit/control"
)

func buildManifest(rt runtime) control.Manifest {
	manifest := control.NewManifest(appID, displayName, "aicrawl")
	manifest.Description = "Local-first archive for AI conversation exports, authenticated web syncs, and local agent transcript stores."
	manifest.Branding = control.Branding{
		SymbolName:       "archivebox",
		AccentColor:      "#2563eb",
		BundleIdentifier: "ai.openclaw.aicrawl",
	}
	manifest.Paths = control.Paths{
		DefaultConfig:   rt.ConfigPath,
		ConfigEnv:       configEnv,
		DefaultDatabase: rt.DBPath,
		DefaultCache:    rt.CacheDir,
		DefaultLogs:     rt.LogDir,
		DefaultShare:    rt.ShareDir,
	}
	manifest.Commands = map[string]control.Command{
		"metadata": {Title: "Metadata", Argv: []string{"aicrawl", "metadata", "--json"}, JSON: true},
		"status":   {Title: "Status", Argv: []string{"aicrawl", "status", "--json"}, JSON: true},
		"doctor":   {Title: "Doctor", Argv: []string{"aicrawl", "doctor", "--json"}, JSON: true},
		"init":     {Title: "Initialize", Argv: []string{"aicrawl", "init"}, Mutates: true},
		"import":   {Title: "Import source", Argv: []string{"aicrawl", "import"}, Mutates: true},
		"reconcile": {
			Title: "Reconcile official export",
			Argv:  []string{"aicrawl", "reconcile"},
			JSON:  true,
		},
		"sync-web": {
			Title:   "Sync web conversations",
			Argv:    []string{"aicrawl", "sync", "web"},
			JSON:    true,
			Mutates: true,
		},
		"schedule-launchd": {
			Title:   "Write LaunchAgent",
			Argv:    []string{"aicrawl", "schedule", "launchd"},
			JSON:    true,
			Mutates: true,
		},
		"search": {Title: "Search", Argv: []string{"aicrawl", "search"}},
		"sql":    {Title: "Read-only SQL", Argv: []string{"aicrawl", "sql"}},
	}
	manifest.Capabilities = []string{
		"local-first",
		"official-exports",
		"authenticated-browser-web-sync",
		"local-agent-transcript-import",
		"official-export-reconciliation",
		"launchd-scheduling",
		"sqlite",
		"fts5",
		"markdown-export",
		"readonly-sql",
	}
	manifest.Privacy = control.Privacy{
		ContainsPrivateMessages: true,
		ExportsSecrets:          false,
		LocalOnlyScopes: []string{
			"claude_export",
			"chatgpt_export",
			"claude_web",
			"chatgpt_web",
			"openclaw_jsonl",
			"codex_jsonl",
			"gemini_cli",
			"claude_code_jsonl",
			"cursor_store",
			"hermes_session",
		},
	}
	return manifest
}

func defaultCrawlBarManifestPath() string {
	return filepath.Join("~", ".crawlbar", "apps", "aicrawl.json")
}
