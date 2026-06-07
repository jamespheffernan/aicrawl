package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/aicrawl/internal/archive"
	"github.com/openclaw/aicrawl/internal/ingest/chatgptexport"
	"github.com/openclaw/aicrawl/internal/ingest/claudecodejsonl"
	"github.com/openclaw/aicrawl/internal/ingest/claudeexport"
	"github.com/openclaw/aicrawl/internal/ingest/codexjsonl"
	"github.com/openclaw/aicrawl/internal/ingest/cursorstore"
	"github.com/openclaw/aicrawl/internal/ingest/geminicli"
	"github.com/openclaw/aicrawl/internal/ingest/openclawjsonl"
	"github.com/openclaw/aicrawl/internal/sync/browser"
	"github.com/openclaw/aicrawl/internal/sync/cdp"
	"github.com/openclaw/aicrawl/internal/sync/chatgptweb"
	"github.com/openclaw/aicrawl/internal/sync/claudeweb"
	"github.com/openclaw/aicrawl/internal/sync/webdiscover"
	"github.com/openclaw/aicrawl/internal/sync/websync"
	"github.com/openclaw/aicrawl/internal/timefmt"
	"github.com/openclaw/crawlkit/control"
)

const Version = "0.1.0"

const defaultMessageContext = 5

type App struct {
	stdout io.Writer
	stderr io.Writer
}

func New() *App {
	return &App{stdout: os.Stdout, stderr: os.Stderr}
}

func (a *App) Run(ctx context.Context, args []string) error {
	args, globals, err := extractGlobalOptions(args)
	if err != nil {
		return withExitCode(2, err)
	}
	if globals.version {
		return a.version()
	}
	if globals.help || len(args) == 0 {
		return a.help()
	}
	command := args[0]
	rest := args[1:]
	switch command {
	case "help":
		return a.help()
	case "version":
		return a.version()
	case "init":
		return a.init(ctx, globals, rest)
	case "metadata":
		return a.metadata(ctx, globals, rest)
	case "status":
		return a.status(ctx, globals, rest)
	case "doctor":
		return a.doctor(ctx, globals, rest)
	case "import":
		return a.importSource(ctx, globals, rest)
	case "reconcile":
		return a.reconcile(ctx, globals, rest)
	case "sync":
		return a.sync(ctx, globals, rest)
	case "schedule":
		return a.schedule(ctx, globals, rest)
	case "conversations":
		return a.conversations(ctx, globals, rest)
	case "messages":
		return a.messages(ctx, globals, rest)
	case "search":
		return a.search(ctx, globals, rest)
	case "sql":
		return a.sql(ctx, globals, rest)
	case "export":
		return a.export(ctx, globals, rest)
	case "crawlbar":
		return a.crawlbar(ctx, globals, rest)
	default:
		return withExitCode(2, fmt.Errorf("unknown command %q", command))
	}
}

func (a *App) help() error {
	_, err := io.WriteString(a.stdout, `aicrawl archives AI conversation sources locally.

Usage:
  aicrawl version
  aicrawl init [--json]
  aicrawl doctor [--json]
  aicrawl metadata [--json]
  aicrawl status [--json]
  aicrawl import <zip-json-jsonl-db-or-dir> [--provider claude|chatgpt|openclaw|codex|gemini|claude-code|cursor|auto] [--dry-run] [--json]
  aicrawl reconcile <official-export-zip-or-json> [--provider claude|chatgpt|auto] [--json]
  aicrawl sync web --provider chatgpt|claude [--source <json-or-zip>] [--profile <dir> | --cdp-url <url>] [--browser <path>] [--remote-debugging-port 0] [--capture <network.json>] [--max-conversations 50] [--dry-run] [--json]
  aicrawl schedule launchd --provider chatgpt|claude [--cdp-url <url> | --profile <dir>] [--browser <path>] [--remote-debugging-port 0] [--interval-minutes 15] [--max-conversations 50] [--out <plist>] [--json]
  aicrawl conversations [--provider claude|chatgpt|openclaw|codex|gemini|claude-code|cursor|all] [--since YYYY-MM-DD] [--until YYYY-MM-DD] [--limit 50]
  aicrawl messages --conversation <id> [--path current|all] [--around <message-id>] [--context 5 | --before N --after N]
  aicrawl search <query> [--group messages|conversations] [--provider claude|chatgpt|openclaw|codex|gemini|claude-code|cursor|all] [--scope visible|transcript|attachments|internal|all] [--role user|assistant|system|developer|tool|attachment|unknown|all] [--path current|all] [--sort relevance|recent] [--since YYYY-MM-DD] [--until YYYY-MM-DD] [--limit 25]
  aicrawl sql <readonly-sql> [--json]
  aicrawl export markdown --out <dir> [--provider claude|chatgpt|openclaw|codex|gemini|claude-code|cursor|all] [--conversation <id>] [--query <query>] [--scope visible|transcript|attachments|internal|all] [--role user|assistant|system|developer|tool|attachment|unknown|all] [--path current|all] [--sort relevance|recent] [--since YYYY-MM-DD] [--until YYYY-MM-DD]
  aicrawl crawlbar manifest [--out ~/.crawlbar/apps/aicrawl.json]

Global options:
  --config <path>   config path, default from AICRAWL_CONFIG or platform config dir
  --json            JSON output where supported
  --help            show this help
`)
	return err
}

func (a *App) version() error {
	return writeTextLine(a.stdout, "aicrawl %s", Version)
}

func (a *App) init(ctx context.Context, globals globalOptions, args []string) error {
	parsed, err := parseOptions(args, boolSet("json"), nil)
	if err != nil {
		return withExitCode(2, err)
	}
	rt, created, err := writeDefaultConfig(globals.configPath)
	if err != nil {
		return err
	}
	ar, err := archive.Open(ctx, rt.DBPath)
	if err != nil {
		return err
	}
	defer ar.Close()
	result := map[string]any{
		"initialized":    true,
		"config_created": created,
		"config_path":    rt.ConfigPath,
		"database_path":  rt.DBPath,
		"version":        Version,
	}
	if globals.format == "json" || parsed.bools["json"] {
		return writeJSON(a.stdout, result)
	}
	if created {
		return writeTextLine(a.stdout, "initialized aicrawl at %s", rt.DBPath)
	}
	return writeTextLine(a.stdout, "aicrawl already initialized at %s", rt.DBPath)
}

func (a *App) metadata(ctx context.Context, globals globalOptions, args []string) error {
	parsed, err := parseOptions(args, boolSet("json"), nil)
	if err != nil {
		return withExitCode(2, err)
	}
	rt, err := resolveRuntime(globals.configPath, false)
	if err != nil {
		return err
	}
	manifest := buildManifest(rt)
	if globals.format == "json" || parsed.bools["json"] {
		return writeJSON(a.stdout, manifest)
	}
	return writeTextLine(a.stdout, "%s (%s)", manifest.DisplayName, manifest.ID)
}

func (a *App) status(ctx context.Context, globals globalOptions, args []string) error {
	parsed, err := parseOptions(args, boolSet("json"), nil)
	if err != nil {
		return withExitCode(2, err)
	}
	rt, err := resolveRuntime(globals.configPath, false)
	if err != nil {
		return err
	}
	status := control.NewStatus(appID, "No archive database found. Run `aicrawl init` to create one.")
	status.ConfigPath = rt.ConfigPath
	status.DatabasePath = rt.DBPath
	status.State = "uninitialized"
	status.Databases = []control.Database{control.SQLiteDatabase("primary", "Archive", "archive", rt.DBPath, true, nil)}
	if archive.Exists(rt.DBPath) {
		ar, err := archive.OpenReadOnly(ctx, rt.DBPath)
		if err != nil {
			status.State = "error"
			status.Summary = "Archive database could not be opened."
			status.Errors = append(status.Errors, err.Error())
		} else {
			defer ar.Close()
			counts, countErr := ar.Counts(ctx)
			if countErr != nil {
				status.State = "error"
				status.Summary = "Archive counts could not be read."
				status.Errors = append(status.Errors, countErr.Error())
			} else {
				status.State = "ok"
				status.Summary = fmt.Sprintf("%d conversations, %d messages archived.", counts.Conversations, counts.Messages)
				status.Counts = controlCounts(counts)
				status.Databases = []control.Database{control.SQLiteDatabase("primary", "Archive", "archive", rt.DBPath, true, status.Counts)}
				if lastImport, err := ar.LastImportAt(ctx); err == nil {
					status.LastImportAt = lastImport
				}
			}
		}
	}
	if globals.format == "json" || parsed.bools["json"] {
		return writeJSON(a.stdout, statusReport{
			Status:  status,
			WebSync: a.webSyncStatus(ctx, rt),
		})
	}
	return writeTextLine(a.stdout, "%s", status.Summary)
}

type statusReport struct {
	control.Status
	WebSync []statusWebSync `json:"web_sync,omitempty"`
}

type statusWebSync struct {
	Provider          string `json:"provider"`
	SourceKind        string `json:"source_kind"`
	State             string `json:"state"`
	LastImportID      string `json:"last_import_id,omitempty"`
	LastImportAt      string `json:"last_import_at,omitempty"`
	ConversationCount int64  `json:"conversation_count,omitempty"`
	MessageCount      int64  `json:"message_count,omitempty"`
}

func (a *App) webSyncStatus(ctx context.Context, rt runtime) []statusWebSync {
	providers := []struct {
		provider   string
		sourceKind string
	}{
		{provider: "chatgpt", sourceKind: "chatgpt_web"},
		{provider: "claude", sourceKind: "claude_web"},
	}
	statuses := make([]statusWebSync, 0, len(providers))
	for _, provider := range providers {
		freshness := a.webFreshness(ctx, rt, provider.sourceKind)
		statuses = append(statuses, statusWebSync{
			Provider:          provider.provider,
			SourceKind:        freshness.SourceKind,
			State:             freshness.State,
			LastImportID:      freshness.LastImportID,
			LastImportAt:      freshness.LastImportAt,
			ConversationCount: freshness.ConversationCount,
			MessageCount:      freshness.MessageCount,
		})
	}
	return statuses
}

type doctorReport struct {
	SchemaVersion         string         `json:"schema_version"`
	AppID                 string         `json:"app_id"`
	GeneratedAt           string         `json:"generated_at"`
	State                 string         `json:"state"`
	ConfigPath            string         `json:"config_path"`
	DatabasePath          string         `json:"database_path"`
	DatabaseSchemaVersion int            `json:"database_schema_version,omitempty"`
	LastImportAt          string         `json:"last_import_at,omitempty"`
	Counts                archive.Counts `json:"counts,omitempty"`
	Checks                []doctorCheck  `json:"checks"`
	Warnings              []string       `json:"warnings,omitempty"`
	Errors                []string       `json:"errors,omitempty"`
}

type doctorCheck struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func (a *App) doctor(ctx context.Context, globals globalOptions, args []string) error {
	parsed, err := parseOptions(args, boolSet("json"), nil)
	if err != nil {
		return withExitCode(2, err)
	}
	rt, err := resolveRuntime(globals.configPath, false)
	if err != nil {
		return err
	}
	report := doctorReport{
		SchemaVersion: control.SchemaVersion,
		AppID:         appID,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		State:         "ok",
		ConfigPath:    rt.ConfigPath,
		DatabasePath:  rt.DBPath,
	}
	if _, err := os.Stat(rt.ConfigPath); err == nil {
		report.Checks = append(report.Checks, doctorCheck{"config", "ok", "config file exists"})
	} else if errors.Is(err, os.ErrNotExist) {
		report.Checks = append(report.Checks, doctorCheck{"config", "warning", "config file does not exist"})
		report.Warnings = append(report.Warnings, "run `aicrawl init` to write a config file")
	} else {
		report.Checks = append(report.Checks, doctorCheck{"config", "error", "config file cannot be inspected"})
		report.Errors = append(report.Errors, err.Error())
		report.State = "error"
	}
	manifestPath := expandPath(defaultCrawlBarManifestPath())
	if _, err := os.Stat(manifestPath); err == nil {
		report.Checks = append(report.Checks, doctorCheck{"crawlbar_manifest", "ok", "CrawlBar manifest exists"})
	} else if errors.Is(err, os.ErrNotExist) {
		report.Checks = append(report.Checks, doctorCheck{"crawlbar_manifest", "warning", "CrawlBar manifest has not been generated"})
		report.Warnings = append(report.Warnings, "run `aicrawl crawlbar manifest` to generate a CrawlBar manifest")
	} else {
		report.Checks = append(report.Checks, doctorCheck{"crawlbar_manifest", "error", "CrawlBar manifest cannot be inspected"})
		report.Errors = append(report.Errors, err.Error())
		report.State = "error"
	}
	if !archive.Exists(rt.DBPath) {
		report.Checks = append(report.Checks, doctorCheck{"database", "warning", "archive database does not exist"})
		report.Warnings = append(report.Warnings, "run `aicrawl init` to create the archive database")
		if report.State == "ok" {
			report.State = "warning"
		}
	} else {
		ar, err := archive.OpenReadOnly(ctx, rt.DBPath)
		if err != nil {
			report.Checks = append(report.Checks, doctorCheck{"database", "error", "archive database cannot be opened read-only"})
			report.Errors = append(report.Errors, err.Error())
			report.State = "error"
		} else {
			defer ar.Close()
			report.Checks = append(report.Checks, doctorCheck{"database", "ok", "archive database opens read-only"})
			if version, err := ar.SchemaVersion(ctx); err == nil {
				report.DatabaseSchemaVersion = version
			} else {
				report.Checks = append(report.Checks, doctorCheck{"database_schema", "error", "archive schema version cannot be read"})
				report.Errors = append(report.Errors, err.Error())
				report.State = "error"
			}
			if err := ar.FTSReady(ctx); err != nil {
				report.Checks = append(report.Checks, doctorCheck{"fts5", "error", "FTS5 table is missing or unreadable"})
				report.Errors = append(report.Errors, err.Error())
				report.State = "error"
			} else {
				report.Checks = append(report.Checks, doctorCheck{"fts5", "ok", "FTS5 index is available"})
			}
			if counts, err := ar.Counts(ctx); err == nil {
				report.Counts = counts
			} else {
				report.Errors = append(report.Errors, err.Error())
				report.State = "error"
			}
			if lastImport, err := ar.LastImportAt(ctx); err == nil {
				report.LastImportAt = lastImport
			} else {
				report.Errors = append(report.Errors, err.Error())
				report.State = "error"
			}
		}
	}
	if report.State == "ok" && len(report.Warnings) > 0 {
		report.State = "warning"
	}
	if globals.format == "json" || parsed.bools["json"] {
		return writeJSON(a.stdout, report)
	}
	for _, check := range report.Checks {
		if err := writeTextLine(a.stdout, "%s: %s", check.ID, check.Status); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) importSource(ctx context.Context, globals globalOptions, args []string) error {
	parsed, err := parseOptions(args, boolSet("json", "dry-run"), valueSet("provider"))
	if err != nil {
		return withExitCode(2, err)
	}
	if len(parsed.positionals) != 1 {
		return withExitCode(2, fmt.Errorf("import requires exactly one ZIP, JSON, JSONL, DB, or directory path"))
	}
	provider, err := importProvider(parsed.values["provider"])
	if err != nil {
		return withExitCode(2, err)
	}
	sourcePath := expandPath(parsed.positionals[0])
	isDir, err := importSourceIsDir(sourcePath)
	if err != nil {
		return err
	}
	if parsed.bools["dry-run"] {
		report, err := inspectImportSource(sourcePath, provider)
		if err != nil {
			return err
		}
		if globals.format == "json" || parsed.bools["json"] {
			return writeJSON(a.stdout, report)
		}
		if err := writeTextLine(a.stdout, "dry-run: found %d conversations and %d messages from %s source", report.Conversations, report.Messages, report.Provider); err != nil {
			return err
		}
		return writeTextLine(a.stdout, "%s", report.PrivacyReminder)
	}
	if isDir && !supportsDirectoryImport(provider) {
		return withExitCode(2, fmt.Errorf("directory import requires provider openclaw, codex, gemini, claude-code, or cursor"))
	}
	rt, err := resolveRuntime(globals.configPath, true)
	if err != nil {
		return err
	}
	ar, err := archive.Open(ctx, rt.DBPath)
	if err != nil {
		return err
	}
	defer ar.Close()
	if isDir {
		stats, err := importDirectory(ctx, ar, sourcePath, provider)
		if err != nil {
			return err
		}
		if globals.format == "json" || parsed.bools["json"] {
			return writeJSON(a.stdout, stats)
		}
		if err := writeTextLine(a.stdout, "imported %d conversations and %d messages from %d %s sources", stats.Conversations, stats.Messages, stats.Sources, stats.Provider); err != nil {
			return err
		}
		return writeTextLine(a.stdout, "%s", stats.PrivacyReminder)
	}
	stats, err := importStream(ctx, ar, sourcePath, provider)
	if err != nil {
		return err
	}
	if globals.format == "json" || parsed.bools["json"] {
		return writeJSON(a.stdout, stats)
	}
	if err := writeTextLine(a.stdout, "imported %d conversations and %d messages from %s source", stats.Conversations, stats.Messages, stats.Provider); err != nil {
		return err
	}
	return writeTextLine(a.stdout, "%s", stats.PrivacyReminder)
}

type importDryRunReport struct {
	DryRun          bool     `json:"dry_run"`
	Provider        string   `json:"provider"`
	SourceKind      string   `json:"source_kind"`
	Sources         int      `json:"sources,omitempty"`
	Conversations   int      `json:"conversations"`
	Messages        int      `json:"messages"`
	Attachments     int      `json:"attachments"`
	Warnings        []string `json:"warnings,omitempty"`
	PrivacyReminder string   `json:"privacy_reminder"`
}

func inspectImportSource(path, provider string) (importDryRunReport, error) {
	isDir, err := importSourceIsDir(path)
	if err != nil {
		return importDryRunReport{}, err
	}
	if isDir {
		return inspectImportDirectory(path, provider)
	}
	return inspectImportFile(path, provider)
}

func inspectImportFile(path, provider string) (importDryRunReport, error) {
	report := importDryRunReport{DryRun: true, PrivacyReminder: "Source files contain private conversation data. Dry-run does not write the archive."}
	emit := func(conversation archive.Conversation, warnings []string) error {
		report.Conversations++
		report.Messages += len(conversation.Messages)
		report.Attachments += len(conversation.Attachments)
		report.Warnings = append(report.Warnings, warnings...)
		return nil
	}
	header, err := streamImportSource(path, provider, emit)
	if err != nil {
		return importDryRunReport{}, err
	}
	report.Provider = header.Provider
	report.SourceKind = header.SourceKind
	return report, nil
}

func importStream(ctx context.Context, ar *archive.Archive, path, provider string) (archive.ImportStats, error) {
	header, err := importSourceIdentity(provider)
	if err != nil {
		if provider == "auto" || provider == "" {
			return importStreamAuto(ctx, ar, path)
		}
		return archive.ImportStats{}, err
	}
	return ar.ImportStream(ctx, path, header.Provider, header.SourceKind, func(emit archive.ConversationEmitter) error {
		_, err := streamImportSource(path, provider, emit)
		return err
	})
}

func streamImportSource(path, provider string, emit archive.ConversationEmitter) (archive.ParsedSource, error) {
	switch provider {
	case "claude":
		return claudeexport.StreamFile(path, emit)
	case "chatgpt":
		return chatgptexport.StreamFile(path, emit)
	case "openclaw":
		return openclawjsonl.StreamFile(path, emit)
	case "codex":
		return codexjsonl.StreamFile(path, emit)
	case "claude-code":
		return claudecodejsonl.StreamFile(path, emit)
	case "cursor":
		return cursorstore.StreamFile(path, emit)
	case "gemini":
		return geminicli.StreamFile(path, emit)
	case "auto", "":
		header, err := streamImportSource(path, "claude", emit)
		if err == nil {
			return header, nil
		}
		if !errors.Is(err, claudeexport.ErrNotClaude) {
			return archive.ParsedSource{}, err
		}
		header, err = streamImportSource(path, "chatgpt", emit)
		if err == nil {
			return header, nil
		}
		if errors.Is(err, chatgptexport.ErrNotChatGPT) {
			return archive.ParsedSource{}, fmt.Errorf("could not detect provider from official export shape")
		}
		return archive.ParsedSource{}, err
	default:
		return archive.ParsedSource{}, fmt.Errorf("unsupported provider %q", provider)
	}
}

func importSourceIdentity(provider string) (archive.ParsedSource, error) {
	switch provider {
	case "claude":
		return archive.ParsedSource{Provider: "claude", SourceKind: "claude_export"}, nil
	case "chatgpt":
		return archive.ParsedSource{Provider: "chatgpt", SourceKind: "chatgpt_export"}, nil
	case "openclaw":
		return archive.ParsedSource{Provider: openclawjsonl.Provider, SourceKind: openclawjsonl.SourceKind}, nil
	case "codex":
		return archive.ParsedSource{Provider: codexjsonl.Provider, SourceKind: codexjsonl.SourceKind}, nil
	case "claude-code":
		return archive.ParsedSource{Provider: claudecodejsonl.Provider, SourceKind: claudecodejsonl.SourceKind}, nil
	case "cursor":
		return archive.ParsedSource{Provider: cursorstore.Provider, SourceKind: cursorstore.SourceKind}, nil
	case "gemini":
		return archive.ParsedSource{Provider: geminicli.Provider, SourceKind: geminicli.SourceKind}, nil
	default:
		return archive.ParsedSource{}, fmt.Errorf("unsupported provider %q", provider)
	}
}

func importStreamAuto(ctx context.Context, ar *archive.Archive, path string) (archive.ImportStats, error) {
	stats, err := importStream(ctx, ar, path, "claude")
	if err == nil {
		return stats, nil
	}
	if !errors.Is(err, claudeexport.ErrNotClaude) {
		return archive.ImportStats{}, err
	}
	stats, err = importStream(ctx, ar, path, "chatgpt")
	if err == nil {
		return stats, nil
	}
	if errors.Is(err, chatgptexport.ErrNotChatGPT) {
		return archive.ImportStats{}, fmt.Errorf("could not detect provider from official export shape")
	}
	return archive.ImportStats{}, err
}

func (a *App) sync(ctx context.Context, globals globalOptions, args []string) error {
	if len(args) == 0 || args[0] != "web" {
		return withExitCode(2, fmt.Errorf("sync requires subcommand web"))
	}
	return a.syncWeb(ctx, globals, args[1:])
}

func (a *App) syncWeb(ctx context.Context, globals globalOptions, args []string) error {
	parsed, err := parseOptions(args, boolSet("json", "dry-run"), valueSet("provider", "profile", "cdp-url", "browser", "remote-debugging-port", "capture", "source", "max-conversations"))
	if err != nil {
		return withExitCode(2, err)
	}
	if len(parsed.positionals) != 0 {
		return withExitCode(2, fmt.Errorf("sync web does not accept positional arguments"))
	}
	provider, err := webProvider(parsed.values["provider"])
	if err != nil {
		return withExitCode(2, err)
	}
	sourcePath := parsed.values["source"]
	if sourcePath != "" {
		sourcePath = expandPath(sourcePath)
	}
	dryRun := parsed.bools["dry-run"]
	maxConversations, err := parsePositiveOption("max-conversations", parsed.values["max-conversations"], 50)
	if err != nil {
		return withExitCode(2, err)
	}
	remoteDebuggingPort, hasRemoteDebuggingPort, err := parseNonNegativeOption("remote-debugging-port", parsed.values["remote-debugging-port"])
	if err != nil {
		return withExitCode(2, err)
	}
	rt, err := resolveRuntime(globals.configPath, !dryRun)
	if err != nil {
		return err
	}
	browserPath := parsed.values["browser"]
	if browserPath != "" {
		browserPath = expandPath(browserPath)
	}
	profilePath := parsed.values["profile"]
	if profilePath != "" {
		profilePath = expandPath(profilePath)
	} else if parsed.values["cdp-url"] == "" {
		profilePath = filepath.Join(rt.CacheDir, "browser-profiles", provider)
	}
	session, err := browser.BuildSessionPlan(browser.SessionOptions{
		Provider:            provider,
		ProfilePath:         profilePath,
		CDPURL:              parsed.values["cdp-url"],
		BrowserPath:         browserPath,
		RemoteDebuggingPort: remoteDebuggingPort,
	})
	if err != nil {
		return withExitCode(2, err)
	}
	if parsed.values["cdp-url"] != "" && (browserPath != "" || hasRemoteDebuggingPort) {
		session.Warnings = append(session.Warnings, "--browser and --remote-debugging-port are ignored when --cdp-url is set")
	}
	var discovery *webdiscover.Report
	if parsed.values["capture"] != "" {
		capturePath := expandPath(parsed.values["capture"])
		capture, err := os.Open(capturePath)
		if err != nil {
			return fmt.Errorf("open web capture: %w", err)
		}
		report, err := webdiscover.Discover(provider, capture)
		closeErr := capture.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		discovery = &report
	}
	if discovery != nil && discovery.State != "matched" && !dryRun {
		return withExitCode(2, contractWriteError(discovery.State))
	}
	freshness := a.webFreshness(ctx, rt, session.SourceKind)
	var sourceStats *websync.SourceStats
	if sourcePath != "" {
		stats, err := inspectWebSource(sourcePath, provider)
		if err != nil {
			return err
		}
		sourceStats = &stats
	}
	if !dryRun {
		if sourcePath == "" && session.CDPURL == "" {
			firstRunLoginRequired := session.AuthState == "login_required"
			launch, err := browser.LaunchProfile(ctx, browser.LaunchOptions{
				Provider:            provider,
				ProfilePath:         profilePath,
				BrowserPath:         browserPath,
				RemoteDebuggingPort: remoteDebuggingPort,
			})
			if err != nil {
				return err
			}
			session.CDPURL = launch.Endpoint
			session.BrowserPath = launch.BrowserPath
			session.Launched = launch.Launched
			session.LaunchPID = launch.PID
			session.RemoteDebuggingPort = launch.RemoteDebuggingPort
			if firstRunLoginRequired {
				session.AuthState = "login_required"
				session.Warnings = append(session.Warnings, "browser launched with a new dedicated profile; log in to "+session.DisplayName+" and rerun sync")
				report := websync.BuildReport(session, discovery, freshness, sourceStats, false)
				if globals.format == "json" || parsed.bools["json"] {
					return writeJSON(a.stdout, report)
				}
				if err := writeTextLine(a.stdout, "%s web sync: login required; browser launched at %s", report.Provider, report.CDPURL); err != nil {
					return err
				}
				for _, warning := range report.Warnings {
					if err := writeTextLine(a.stdout, "warning: %s", warning); err != nil {
						return err
					}
				}
				return nil
			}
			if launch.Reused {
				session.AuthState = "browser_reused"
			} else {
				session.AuthState = "browser_launched"
			}
		}
		ar, err := archive.Open(ctx, rt.DBPath)
		if err != nil {
			return err
		}
		defer ar.Close()
		var stats archive.ImportStats
		if sourcePath != "" {
			stats, err = syncWebSource(ctx, ar, sourcePath, provider)
		} else {
			stats, err = syncWebLive(ctx, ar, rt, provider, session, maxConversations)
		}
		if err != nil {
			return err
		}
		if globals.format == "json" || parsed.bools["json"] {
			return writeJSON(a.stdout, stats)
		}
		if err := writeTextLine(a.stdout, "synced %d conversations and %d messages from %s web payloads", stats.Conversations, stats.Messages, stats.Provider); err != nil {
			return err
		}
		return writeTextLine(a.stdout, "%s", stats.PrivacyReminder)
	}
	report := websync.BuildReport(session, discovery, freshness, sourceStats, true)
	if globals.format == "json" || parsed.bools["json"] {
		return writeJSON(a.stdout, report)
	}
	if err := writeTextLine(a.stdout, "%s web sync: %s auth, %s contract, %s freshness", report.Provider, report.AuthState, report.EndpointContractState, report.Freshness.State); err != nil {
		return err
	}
	for _, warning := range report.Warnings {
		if err := writeTextLine(a.stdout, "warning: %s", warning); err != nil {
			return err
		}
	}
	return nil
}

func inspectWebSource(path, provider string) (websync.SourceStats, error) {
	stats := websync.SourceStats{Path: path}
	emit := func(conversation archive.Conversation, warnings []string) error {
		stats.Conversations++
		stats.Messages += len(conversation.Messages)
		stats.Attachments += len(conversation.Attachments)
		stats.Warnings = append(stats.Warnings, warnings...)
		return nil
	}
	switch provider {
	case chatgptweb.Provider:
		if _, err := chatgptweb.StreamFile(path, emit); err != nil {
			return websync.SourceStats{}, err
		}
	case claudeweb.Provider:
		if _, err := claudeweb.StreamFile(path, emit); err != nil {
			return websync.SourceStats{}, err
		}
	default:
		return websync.SourceStats{}, fmt.Errorf("unsupported web provider %q", provider)
	}
	return stats, nil
}

func contractWriteError(state string) error {
	if state == "" {
		state = "unknown"
	}
	return fmt.Errorf("contract_%s: endpoint contract is %s; refusing to sync before archive writes", state, state)
}

func syncWebSource(ctx context.Context, ar *archive.Archive, path, provider string) (archive.ImportStats, error) {
	switch provider {
	case chatgptweb.Provider:
		return ar.ImportStream(ctx, path, chatgptweb.Provider, chatgptweb.SourceKind, func(emit archive.ConversationEmitter) error {
			_, err := chatgptweb.StreamFile(path, emit)
			return err
		})
	case claudeweb.Provider:
		return ar.ImportStream(ctx, path, claudeweb.Provider, claudeweb.SourceKind, func(emit archive.ConversationEmitter) error {
			_, err := claudeweb.StreamFile(path, emit)
			return err
		})
	default:
		return archive.ImportStats{}, fmt.Errorf("unsupported web provider %q", provider)
	}
}

func syncWebLive(ctx context.Context, ar *archive.Archive, rt runtime, provider string, session browser.SessionPlan, maxConversations int) (archive.ImportStats, error) {
	spec, err := browser.Provider(provider)
	if err != nil {
		return archive.ImportStats{}, err
	}
	cdpSession, err := cdp.Open(ctx, cdp.Options{
		Endpoint:       session.CDPURL,
		HomeURL:        spec.HomeURL,
		AllowedOrigins: spec.Origins,
	})
	if err != nil {
		return archive.ImportStats{}, err
	}
	defer cdpSession.Close(1000, "")
	var payload []byte
	switch provider {
	case chatgptweb.Provider:
		payload, err = chatgptweb.FetchLive(ctx, chatGPTCDPFetcher{session: cdpSession}, chatgptweb.LiveOptions{MaxConversations: maxConversations})
	case claudeweb.Provider:
		payload, err = claudeweb.FetchLive(ctx, claudeCDPFetcher{session: cdpSession}, claudeweb.LiveOptions{MaxConversations: maxConversations})
	default:
		err = fmt.Errorf("unsupported web provider %q", provider)
	}
	if err != nil {
		return archive.ImportStats{}, err
	}
	tempPath, cleanup, err := writeLiveSyncTemp(rt, provider, payload)
	if err != nil {
		return archive.ImportStats{}, err
	}
	defer cleanup()
	return syncWebSource(ctx, ar, tempPath, provider)
}

type chatGPTCDPFetcher struct {
	session *cdp.Session
}

func (f chatGPTCDPFetcher) Fetch(ctx context.Context, requestURL string) (chatgptweb.FetchResponse, error) {
	resp, err := f.session.Fetch(ctx, requestURL)
	if err != nil {
		return chatgptweb.FetchResponse{}, err
	}
	return chatgptweb.FetchResponse{Status: resp.Status, URL: resp.URL, Body: resp.Body}, nil
}

type claudeCDPFetcher struct {
	session *cdp.Session
}

func (f claudeCDPFetcher) Fetch(ctx context.Context, requestURL string) (claudeweb.FetchResponse, error) {
	resp, err := f.session.Fetch(ctx, requestURL)
	if err != nil {
		return claudeweb.FetchResponse{}, err
	}
	return claudeweb.FetchResponse{Status: resp.Status, URL: resp.URL, Body: resp.Body}, nil
}

func writeLiveSyncTemp(rt runtime, provider string, payload []byte) (string, func(), error) {
	dir := filepath.Join(rt.CacheDir, "live-sync")
	if err := ensurePrivateDir(dir); err != nil {
		return "", nil, err
	}
	file, err := os.CreateTemp(dir, provider+"-*.json")
	if err != nil {
		return "", nil, fmt.Errorf("create private live sync temp file: %w", err)
	}
	path := file.Name()
	cleanup := func() {
		_ = os.Remove(path)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, fmt.Errorf("chmod live sync temp file: %w", err)
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, fmt.Errorf("write live sync temp file: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close live sync temp file: %w", err)
	}
	return path, cleanup, nil
}

func (a *App) webFreshness(ctx context.Context, rt runtime, sourceKind string) websync.Freshness {
	freshness := websync.Freshness{State: "archive_missing", SourceKind: sourceKind}
	if !archive.Exists(rt.DBPath) {
		return freshness
	}
	ar, err := archive.OpenReadOnly(ctx, rt.DBPath)
	if err != nil {
		freshness.State = "archive_unreadable"
		return freshness
	}
	defer ar.Close()
	state, ok, err := ar.SyncState(ctx, sourceKind)
	if err != nil {
		freshness.State = "sync_state_unreadable"
		return freshness
	}
	if !ok {
		freshness.State = "never_synced"
		return freshness
	}
	return websync.Freshness{
		State:             "seen",
		SourceKind:        state.SourceKind,
		LastImportID:      state.LastImportID,
		LastImportAt:      state.LastImportAt,
		ConversationCount: state.ConversationCount,
		MessageCount:      state.MessageCount,
	}
}

func (a *App) conversations(ctx context.Context, globals globalOptions, args []string) error {
	parsed, err := parseOptions(args, boolSet("json"), valueSet("provider", "limit", "since", "until"))
	if err != nil {
		return withExitCode(2, err)
	}
	limit, err := parseLimit(parsed.values["limit"], 50)
	if err != nil {
		return withExitCode(2, err)
	}
	provider, err := providerOrAll(parsed.values["provider"])
	if err != nil {
		return withExitCode(2, err)
	}
	since, until, err := parseDateBounds(parsed.values["since"], parsed.values["until"])
	if err != nil {
		return withExitCode(2, err)
	}
	ar, err := a.openReadOnlyArchive(ctx, globals)
	if err != nil {
		return err
	}
	defer ar.Close()
	rows, err := ar.ConversationsFiltered(ctx, archive.ConversationFilter{Provider: provider, Since: since, Until: until}, limit)
	if err != nil {
		return err
	}
	if globals.format == "json" || parsed.bools["json"] {
		return writeJSON(a.stdout, rows)
	}
	for _, row := range rows {
		if err := writeTextLine(a.stdout, "%s\t%s\t%d\t%s", row.Provider, row.ID, row.MessageCount, row.Title); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) messages(ctx context.Context, globals globalOptions, args []string) error {
	parsed, err := parseOptions(args, boolSet("json"), valueSet("conversation", "path", "around", "context", "before", "after"))
	if err != nil {
		return withExitCode(2, err)
	}
	conversationID := parsed.values["conversation"]
	if conversationID == "" {
		return withExitCode(2, fmt.Errorf("--conversation is required"))
	}
	pathMode := parsed.values["path"]
	if pathMode == "" {
		pathMode = "current"
	}
	if pathMode != "current" && pathMode != "all" {
		return withExitCode(2, fmt.Errorf("--path must be current or all"))
	}
	aroundID := parsed.values["around"]
	before, after, err := messageContextBounds(parsed.values["context"], parsed.values["before"], parsed.values["after"], aroundID != "")
	if err != nil {
		return withExitCode(2, err)
	}
	ar, err := a.openReadOnlyArchive(ctx, globals)
	if err != nil {
		return err
	}
	defer ar.Close()
	rows, err := ar.MessagesWithOptions(ctx, archive.MessageOptions{
		ConversationID: conversationID,
		PathMode:       pathMode,
		AroundID:       aroundID,
		Before:         before,
		After:          after,
	})
	if err != nil {
		return err
	}
	if globals.format == "json" || parsed.bools["json"] {
		return writeJSON(a.stdout, rows)
	}
	for _, row := range rows {
		if err := writeTextLine(a.stdout, "[%s] %s", row.Role, row.Text); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) search(ctx context.Context, globals globalOptions, args []string) error {
	parsed, err := parseOptions(args, boolSet("json"), valueSet("provider", "limit", "role", "scope", "group", "sort", "path", "since", "until"))
	if err != nil {
		return withExitCode(2, err)
	}
	if len(parsed.positionals) == 0 {
		return withExitCode(2, fmt.Errorf("search query is required"))
	}
	query := strings.Join(parsed.positionals, " ")
	limit, err := parseLimit(parsed.values["limit"], 25)
	if err != nil {
		return withExitCode(2, err)
	}
	provider, err := providerOrAll(parsed.values["provider"])
	if err != nil {
		return withExitCode(2, err)
	}
	role, err := searchRole(parsed.values["role"])
	if err != nil {
		return withExitCode(2, err)
	}
	scope, err := searchScope(parsed.values["scope"])
	if err != nil {
		return withExitCode(2, err)
	}
	group, err := searchGroup(parsed.values["group"])
	if err != nil {
		return withExitCode(2, err)
	}
	sortMode, err := searchSort(parsed.values["sort"])
	if err != nil {
		return withExitCode(2, err)
	}
	pathMode, err := pathModeOrDefault(parsed.values["path"], "current")
	if err != nil {
		return withExitCode(2, err)
	}
	since, until, err := parseDateBounds(parsed.values["since"], parsed.values["until"])
	if err != nil {
		return withExitCode(2, err)
	}
	ar, err := a.openReadOnlyArchive(ctx, globals)
	if err != nil {
		return err
	}
	defer ar.Close()
	opts := archive.SearchOptions{
		Query:    query,
		Provider: provider,
		Limit:    limit,
		Role:     role,
		Scope:    scope,
		PathMode: pathMode,
		Sort:     sortMode,
		Since:    since,
		Until:    until,
	}
	if group == "conversations" {
		hits, err := ar.SearchConversations(ctx, opts)
		if err != nil {
			return err
		}
		if globals.format == "json" || parsed.bools["json"] {
			return writeJSON(a.stdout, hits)
		}
		for _, hit := range hits {
			if err := writeTextLine(a.stdout, "%s\t%s\t%d\t%s\t%s", hit.Provider, hit.ID, hit.MatchCount, hit.NewestMatchAt, hit.Snippet); err != nil {
				return err
			}
		}
		return nil
	}
	hits, err := ar.SearchMessages(ctx, opts)
	if err != nil {
		return err
	}
	if globals.format == "json" || parsed.bools["json"] {
		return writeJSON(a.stdout, hits)
	}
	for _, hit := range hits {
		if err := writeTextLine(a.stdout, "%s\t%s\t%s\t%s\t%s", hit.Provider, hit.ConversationID, hit.Role, hit.Scope, hit.Text); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) sql(ctx context.Context, globals globalOptions, args []string) error {
	parsed, err := parseOptions(args, boolSet("json"), nil)
	if err != nil {
		return withExitCode(2, err)
	}
	if len(parsed.positionals) != 1 {
		return withExitCode(2, fmt.Errorf("sql requires exactly one read-only SQL statement"))
	}
	query, err := archive.ValidateReadOnlySQL(parsed.positionals[0])
	if err != nil {
		return withExitCode(2, err)
	}
	ar, err := a.openReadOnlyArchive(ctx, globals)
	if err != nil {
		return err
	}
	defer ar.Close()
	result, err := ar.Query(ctx, query)
	if err != nil {
		return err
	}
	if globals.format == "json" || parsed.bools["json"] {
		return writeJSON(a.stdout, result)
	}
	if len(result.Columns) > 0 {
		if err := writeTextLine(a.stdout, "%s", strings.Join(result.Columns, "\t")); err != nil {
			return err
		}
	}
	for _, row := range result.Rows {
		values := make([]string, len(row))
		for i, value := range row {
			values[i] = fmt.Sprint(value)
		}
		if err := writeTextLine(a.stdout, "%s", strings.Join(values, "\t")); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) export(ctx context.Context, globals globalOptions, args []string) error {
	if len(args) == 0 || args[0] != "markdown" {
		return withExitCode(2, fmt.Errorf("export requires subcommand markdown"))
	}
	parsed, err := parseOptions(args[1:], boolSet("json"), valueSet("out", "provider", "conversation", "query", "role", "scope", "path", "sort", "since", "until"))
	if err != nil {
		return withExitCode(2, err)
	}
	outDir := parsed.values["out"]
	if outDir == "" {
		return withExitCode(2, fmt.Errorf("export markdown requires --out"))
	}
	outDir = expandPath(outDir)
	provider, err := providerOrAll(parsed.values["provider"])
	if err != nil {
		return withExitCode(2, err)
	}
	pathMode, err := pathModeOrDefault(parsed.values["path"], "all")
	if err != nil {
		return withExitCode(2, err)
	}
	role, err := searchRole(parsed.values["role"])
	if err != nil {
		return withExitCode(2, err)
	}
	scope, err := searchScope(parsed.values["scope"])
	if err != nil {
		return withExitCode(2, err)
	}
	sortMode, err := searchSort(parsed.values["sort"])
	if err != nil {
		return withExitCode(2, err)
	}
	since, until, err := parseDateBounds(parsed.values["since"], parsed.values["until"])
	if err != nil {
		return withExitCode(2, err)
	}
	if parsed.values["query"] == "" && (parsed.values["role"] != "" || parsed.values["scope"] != "" || parsed.values["sort"] != "") {
		return withExitCode(2, fmt.Errorf("--role, --scope, and --sort require --query for markdown export"))
	}
	ar, err := a.openReadOnlyArchive(ctx, globals)
	if err != nil {
		return err
	}
	defer ar.Close()
	count, err := ar.ExportMarkdownWithOptions(ctx, archive.MarkdownExportOptions{
		OutDir:         outDir,
		Provider:       provider,
		ConversationID: parsed.values["conversation"],
		Query:          parsed.values["query"],
		Role:           role,
		Scope:          scope,
		PathMode:       pathMode,
		Sort:           sortMode,
		Since:          since,
		Until:          until,
	})
	if err != nil {
		return err
	}
	result := map[string]any{"format": "markdown", "out": outDir, "conversations": count}
	if globals.format == "json" || parsed.bools["json"] {
		return writeJSON(a.stdout, result)
	}
	return writeTextLine(a.stdout, "exported %d conversations to %s", count, outDir)
}

func (a *App) crawlbar(ctx context.Context, globals globalOptions, args []string) error {
	if len(args) == 0 || args[0] != "manifest" {
		return withExitCode(2, fmt.Errorf("crawlbar requires subcommand manifest"))
	}
	parsed, err := parseOptions(args[1:], boolSet("json"), valueSet("out"))
	if err != nil {
		return withExitCode(2, err)
	}
	outPath := parsed.values["out"]
	if outPath == "" {
		outPath = defaultCrawlBarManifestPath()
	}
	outPath = expandPath(outPath)
	rt, err := resolveRuntime(globals.configPath, false)
	if err != nil {
		return err
	}
	manifest := buildManifest(rt)
	if err := ensurePrivateDir(filepath.Dir(outPath)); err != nil {
		return fmt.Errorf("prepare CrawlBar manifest dir: %w", err)
	}
	dataWriter, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("write CrawlBar manifest: %w", err)
	}
	if err := dataWriter.Chmod(0o600); err != nil {
		_ = dataWriter.Close()
		return fmt.Errorf("chmod CrawlBar manifest: %w", err)
	}
	if err := writeJSON(dataWriter, manifest); err != nil {
		_ = dataWriter.Close()
		return err
	}
	if err := dataWriter.Close(); err != nil {
		return err
	}
	if globals.format == "json" || parsed.bools["json"] {
		return writeJSON(a.stdout, map[string]any{"path": outPath, "manifest": manifest})
	}
	_ = ctx
	return writeTextLine(a.stdout, "wrote CrawlBar manifest to %s", outPath)
}

func (a *App) openReadOnlyArchive(ctx context.Context, globals globalOptions) (*archive.Archive, error) {
	rt, err := resolveRuntime(globals.configPath, false)
	if err != nil {
		return nil, err
	}
	if !archive.Exists(rt.DBPath) {
		return nil, fmt.Errorf("archive database does not exist, run `aicrawl init` first")
	}
	return archive.OpenReadOnly(ctx, rt.DBPath)
}

func controlCounts(counts archive.Counts) []control.Count {
	return []control.Count{
		control.NewCount("providers", "Providers", counts.Providers),
		control.NewCount("imports", "Imports", counts.Imports),
		control.NewCount("conversations", "Conversations", counts.Conversations),
		control.NewCount("messages", "Messages", counts.Messages),
		control.NewCount("edges", "Message edges", counts.Edges),
		control.NewCount("attachments", "Attachments", counts.Attachments),
	}
}

func parseLimit(value string, fallback int) (int, error) {
	return parsePositiveOption("limit", value, fallback)
}

func parsePositiveOption(name, value string, fallback int) (int, error) {
	if value == "" {
		return fallback, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit <= 0 {
		return 0, fmt.Errorf("--%s must be a positive integer", name)
	}
	return limit, nil
}

func parseNonNegativeOption(name, value string) (int, bool, error) {
	if value == "" {
		return 0, false, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, false, fmt.Errorf("--%s must be a non-negative integer", name)
	}
	return parsed, true, nil
}

func messageContextBounds(contextValue, beforeValue, afterValue string, aroundSet bool) (int, int, error) {
	contextCount, hasContext, err := parseNonNegativeOption("context", contextValue)
	if err != nil {
		return 0, 0, err
	}
	before, hasBefore, err := parseNonNegativeOption("before", beforeValue)
	if err != nil {
		return 0, 0, err
	}
	after, hasAfter, err := parseNonNegativeOption("after", afterValue)
	if err != nil {
		return 0, 0, err
	}
	if !aroundSet {
		if hasContext || hasBefore || hasAfter {
			return 0, 0, fmt.Errorf("--context, --before, and --after require --around")
		}
		return 0, 0, nil
	}
	if hasContext {
		if !hasBefore {
			before = contextCount
		}
		if !hasAfter {
			after = contextCount
		}
	} else {
		if !hasBefore {
			before = defaultMessageContext
		}
		if !hasAfter {
			after = defaultMessageContext
		}
	}
	return before, after, nil
}

func pathModeOrDefault(value, fallback string) (string, error) {
	if value == "" {
		value = fallback
	}
	switch value {
	case "current", "all":
		return value, nil
	default:
		return "", fmt.Errorf("--path must be current or all")
	}
}

func searchGroup(value string) (string, error) {
	if value == "" {
		return "messages", nil
	}
	switch value {
	case "messages", "conversations":
		return value, nil
	default:
		return "", fmt.Errorf("--group must be messages or conversations")
	}
}

func searchSort(value string) (string, error) {
	if value == "" {
		return "relevance", nil
	}
	switch value {
	case "relevance", "recent":
		return value, nil
	default:
		return "", fmt.Errorf("--sort must be relevance or recent")
	}
}

func searchScope(value string) (string, error) {
	if value == "" {
		return "visible", nil
	}
	switch value {
	case "visible", "transcript", "attachments", "internal", "all":
		return value, nil
	default:
		return "", fmt.Errorf("--scope must be visible, transcript, attachments, internal, or all")
	}
}

func searchRole(value string) (string, error) {
	if value == "" {
		return "all", nil
	}
	switch value {
	case "all", "user", "assistant", "system", "developer", "tool", "attachment", "unknown":
		return value, nil
	default:
		return "", fmt.Errorf("--role must be user, assistant, system, developer, tool, attachment, unknown, or all")
	}
}

func parseDateBounds(sinceValue, untilValue string) (string, string, error) {
	since, err := parseDateBound("since", sinceValue, false)
	if err != nil {
		return "", "", err
	}
	until, err := parseDateBound("until", untilValue, true)
	if err != nil {
		return "", "", err
	}
	if since != "" && until != "" && since > until {
		return "", "", fmt.Errorf("--since must be before or equal to --until")
	}
	return since, until, nil
}

func parseDateBound(name, value string, endOfDay bool) (string, error) {
	if value == "" {
		return "", nil
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return timefmt.FormatUTC(t), nil
	}
	date, err := time.Parse("2006-01-02", value)
	if err != nil {
		return "", fmt.Errorf("--%s must be YYYY-MM-DD or RFC3339", name)
	}
	if endOfDay {
		date = date.Add(24 * time.Hour).Add(-time.Nanosecond)
	}
	return timefmt.FormatUTC(date), nil
}

func providerOrAll(value string) (string, error) {
	if value == "" {
		return "all", nil
	}
	switch value {
	case "all", "claude", "chatgpt", "openclaw", "codex", "gemini", "claude-code", "cursor":
		return value, nil
	default:
		return "", fmt.Errorf("--provider must be claude, chatgpt, openclaw, codex, gemini, claude-code, cursor, or all")
	}
}

func importProvider(value string) (string, error) {
	if value == "" {
		return "auto", nil
	}
	switch value {
	case "auto", "claude", "chatgpt", "openclaw", "codex", "gemini", "claude-code", "cursor":
		return value, nil
	default:
		return "", fmt.Errorf("--provider must be claude, chatgpt, openclaw, codex, gemini, claude-code, cursor, or auto")
	}
}

func webProvider(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("--provider is required")
	}
	switch value {
	case "chatgpt", "claude":
		return value, nil
	default:
		return "", fmt.Errorf("--provider must be chatgpt or claude")
	}
}
