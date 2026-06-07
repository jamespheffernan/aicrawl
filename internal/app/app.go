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
	"github.com/openclaw/aicrawl/internal/ingest/claudeexport"
	"github.com/openclaw/aicrawl/internal/sync/browser"
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
	case "sync":
		return a.sync(ctx, globals, rest)
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
	_, err := io.WriteString(a.stdout, `aicrawl archives official Claude and ChatGPT conversation exports locally.

Usage:
  aicrawl version
  aicrawl init [--json]
  aicrawl doctor [--json]
  aicrawl metadata [--json]
  aicrawl status [--json]
  aicrawl import <zip-or-json> [--provider claude|chatgpt|auto]
  aicrawl sync web --provider chatgpt|claude [--source <json-or-zip>] [--profile <dir> | --cdp-url <url>] [--capture <network.json>] [--dry-run] [--json]
  aicrawl conversations [--provider claude|chatgpt|all] [--since YYYY-MM-DD] [--until YYYY-MM-DD] [--limit 50]
  aicrawl messages --conversation <id> [--path current|all] [--around <message-id>] [--context 5 | --before N --after N]
  aicrawl search <query> [--group messages|conversations] [--provider claude|chatgpt|all] [--scope visible|transcript|attachments|internal|all] [--role user|assistant|system|developer|tool|attachment|unknown|all] [--path current|all] [--sort relevance|recent] [--since YYYY-MM-DD] [--until YYYY-MM-DD] [--limit 25]
  aicrawl sql <readonly-sql> [--json]
  aicrawl export markdown --out <dir> [--provider claude|chatgpt|all] [--conversation <id>] [--query <query>] [--scope visible|transcript|attachments|internal|all] [--role user|assistant|system|developer|tool|attachment|unknown|all] [--path current|all] [--sort relevance|recent] [--since YYYY-MM-DD] [--until YYYY-MM-DD]
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
		return writeJSON(a.stdout, status)
	}
	return writeTextLine(a.stdout, "%s", status.Summary)
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
	parsed, err := parseOptions(args, boolSet("json"), valueSet("provider"))
	if err != nil {
		return withExitCode(2, err)
	}
	if len(parsed.positionals) != 1 {
		return withExitCode(2, fmt.Errorf("import requires exactly one ZIP or JSON path"))
	}
	provider, err := importProvider(parsed.values["provider"])
	if err != nil {
		return withExitCode(2, err)
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
	stats, err := importStream(ctx, ar, parsed.positionals[0], provider)
	if err != nil {
		return err
	}
	if globals.format == "json" || parsed.bools["json"] {
		return writeJSON(a.stdout, stats)
	}
	if err := writeTextLine(a.stdout, "imported %d conversations and %d messages from %s export", stats.Conversations, stats.Messages, stats.Provider); err != nil {
		return err
	}
	return writeTextLine(a.stdout, "%s", stats.PrivacyReminder)
}

func importStream(ctx context.Context, ar *archive.Archive, path, provider string) (archive.ImportStats, error) {
	switch provider {
	case "claude":
		return ar.ImportStream(ctx, path, "claude", "claude_export", func(emit archive.ConversationEmitter) error {
			_, err := claudeexport.StreamFile(path, emit)
			return err
		})
	case "chatgpt":
		return ar.ImportStream(ctx, path, "chatgpt", "chatgpt_export", func(emit archive.ConversationEmitter) error {
			_, err := chatgptexport.StreamFile(path, emit)
			return err
		})
	case "auto", "":
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
	default:
		return archive.ImportStats{}, fmt.Errorf("unsupported provider %q", provider)
	}
}

func (a *App) sync(ctx context.Context, globals globalOptions, args []string) error {
	if len(args) == 0 || args[0] != "web" {
		return withExitCode(2, fmt.Errorf("sync requires subcommand web"))
	}
	return a.syncWeb(ctx, globals, args[1:])
}

func (a *App) syncWeb(ctx context.Context, globals globalOptions, args []string) error {
	parsed, err := parseOptions(args, boolSet("json", "dry-run"), valueSet("provider", "profile", "cdp-url", "capture", "source"))
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
	if !dryRun && sourcePath == "" {
		return withExitCode(2, fmt.Errorf("sync web live browser fetch is not implemented yet; pass --source with captured provider payloads or use --dry-run"))
	}
	rt, err := resolveRuntime(globals.configPath, !dryRun)
	if err != nil {
		return err
	}
	profilePath := parsed.values["profile"]
	if profilePath != "" {
		profilePath = expandPath(profilePath)
	} else if parsed.values["cdp-url"] == "" {
		profilePath = filepath.Join(rt.CacheDir, "browser-profiles", provider)
	}
	session, err := browser.BuildSessionPlan(browser.SessionOptions{
		Provider:    provider,
		ProfilePath: profilePath,
		CDPURL:      parsed.values["cdp-url"],
	})
	if err != nil {
		return withExitCode(2, err)
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
		return withExitCode(2, fmt.Errorf("endpoint contract is %s; refusing to sync captured web payloads", discovery.State))
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
		ar, err := archive.Open(ctx, rt.DBPath)
		if err != nil {
			return err
		}
		defer ar.Close()
		stats, err := syncWebSource(ctx, ar, sourcePath, provider)
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
	if value == "" {
		return fallback, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit <= 0 {
		return 0, fmt.Errorf("--limit must be a positive integer")
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
	case "all", "claude", "chatgpt":
		return value, nil
	default:
		return "", fmt.Errorf("--provider must be claude, chatgpt, or all")
	}
}

func importProvider(value string) (string, error) {
	if value == "" {
		return "auto", nil
	}
	switch value {
	case "auto", "claude", "chatgpt":
		return value, nil
	default:
		return "", fmt.Errorf("--provider must be claude, chatgpt, or auto")
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
