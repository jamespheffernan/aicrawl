# Architecture

`aicrawl` is a local-first archive for AI conversation data. The v0.1 product is a CLI that imports local official export ZIP/JSON files, captured web conversation detail payloads, and selected local agent session files into SQLite, preserves the original JSON payloads, builds a normalized text index, and exposes read-only retrieval, search, SQL, Markdown export, and CrawlBar metadata.

## User Workflow

1. Collect an official export, captured web payload, or local session file in a private local directory.
2. Run `aicrawl init` to create a private config and SQLite archive.
3. Run `aicrawl import <path> --provider claude|chatgpt|openclaw|codex|gemini|claude-code|auto`.
4. Use `conversations`, `messages`, `search`, `sql`, or `export markdown` against the local archive.
5. Optionally run `aicrawl crawlbar manifest` so CrawlBar can discover the local control surface.

For captured web payloads, run `aicrawl sync web --provider chatgpt|claude --source <json-or-zip>`. Dry-run mode reports auth state, contract state, source counts, and freshness without writing.

The importer does not delete source files. Those files still contain private data after import.

## Package Layout

```text
cmd/aicrawl/              CLI entry point
internal/app/             command parsing, runtime paths, JSON/text output, CrawlBar manifest
internal/archive/         SQLite reads/writes, import ledger, sync-state reads, search, read-only SQL, Markdown export
internal/schema/          schema migrations using PRAGMA user_version
internal/ingest/
  claudeexport/           official Claude export parser
  chatgptexport/          official ChatGPT export parser
  openclawjsonl/          OpenClaw session JSONL parser
  codexjsonl/             Codex rollout JSONL parser
  geminicli/              Gemini CLI session JSON parser
  claudecodejsonl/        Claude Code project JSONL parser
  localtext/              shared local transcript text/timestamp helpers
internal/sync/
  browser/                browser-profile and CDP preflight for web sync
  webdiscover/            redacted network capture contract discovery
  chatgptweb/             captured ChatGPT web payload adapter
  claudeweb/              captured Claude web payload adapter
  websync/                web sync report model, source counts, and privacy boundary
internal/security/        ZIP safety, source size guards, private file handling helpers
internal/textnorm/        searchable text normalization and safe FTS query construction
testdata/redacted/        small synthetic fixtures only
```

Provider-specific parsing stays under `internal/ingest/*`. Reusable local archive mechanics come from `github.com/openclaw/crawlkit` where they naturally fit, especially config paths, SQLite store opening, and control payload types.

## Import Flow

```mermaid
flowchart LR
  User["Export ZIP/JSON or local session file"] --> Source["source reader"]
  Source --> Detect["Provider selection or official export auto-detect"]
  Detect --> Parser["internal/ingest provider parser"]
  Parser --> Canonical["Canonical conversations, messages, edges, attachments"]
  Canonical --> Archive["internal/archive import transaction"]
  Archive --> SQLite["SQLite schema v1"]
  SQLite --> FTS["messages_fts"]
```

The source reader accepts local JSON files and ZIP files with JSON entries. It rejects unsafe ZIP entries such as absolute paths, traversal, and symlinks. Official top-level conversation arrays are streamed so large exports do not have to be buffered as one JSON blob.

Every import is keyed by source kind, provider, and source hash. Re-importing the same file returns the existing ledger row and does not duplicate conversations, messages, edges, attachments, or FTS entries.

## Web Sync Preflight

```mermaid
flowchart LR
  BrowserProfile["Dedicated browser profile or CDP URL"] --> SessionPlan["browser session preflight"]
  NetworkCapture["Redacted network JSON capture"] --> ContractDiscovery["endpoint contract discovery"]
  ArchiveState["SQLite sync_state"] --> Freshness["freshness report"]
  SourcePayload["Captured detail payload JSON/ZIP"] --> WebAdapter["web payload adapter"]
  WebAdapter --> Archive["archive import transaction"]
  SessionPlan --> Report["sync web report"]
  ContractDiscovery --> Report
  Freshness --> Report
```

`aicrawl sync web` is the first browser-profile lane. It validates the intended auth boundary and reports whether a ChatGPT or Claude capture still exposes recognizable conversation list/detail calls. With `--source`, it imports captured conversation detail payloads under `chatgpt_web` or `claude_web`; without `--source`, non-dry-run mode exits because live browser page-context fetching is not implemented yet.

The network discovery parser consumes structured JSON captures, walks nested request objects, and emits only sanitized origins and paths. Query strings, fragments, headers, cookies, and authorization values are not included in the report.

## Data Model

The archive stores provider-prefixed stable IDs for conversations and messages. It does not expose SQLite row IDs as stable user-facing IDs.

Raw JSON is preserved in `raw_payload` columns for conversations, messages, message versions, attachments, and accounts where available. Parsed columns are used for listing, filtering, and search. Normalized text is only the searchable projection; it does not replace raw payloads.

ChatGPT exports are treated as a graph. The importer stores every mapping node as a message when possible and stores explicit parent/child edges in `message_edges`. The current path is marked separately so users can choose `--path current` or `--path all`.

Claude exports are parsed defensively. Unknown fields are tolerated and preserved in raw payloads. Missing or malformed optional metadata produces warnings where useful, while missing required stable IDs fail clearly.

## Search Flow

```mermaid
flowchart LR
  Query["search query"] --> Terms["internal/textnorm terms"]
  Terms --> FTS["safe FTS5 MATCH terms"]
  Terms --> Literal["literal fallback for reserved-only terms"]
  FTS --> Filters["provider, role, scope, path, dates"]
  Literal --> Filters
  Filters --> Results["message or conversation results"]
```

`aicrawl search` defaults to visible current-path content. Visible content includes user, assistant, unknown transcript text, and attachment text. Internal roles such as system, developer, and tool are searchable only when requested with `--scope internal` or `--scope all`.

FTS reserved words and operators such as `AND`, `OR`, `NOT`, `NEAR`, and `*` are handled as user search terms rather than accidental FTS syntax.

## Read And Export Flow

`conversations`, `messages`, `search`, `sql`, `metadata`, `status`, and `doctor` open the archive read-only when they touch the database. `sql` accepts one statement and only allows read-only `SELECT`, `WITH`, and a small allowlist of safe `PRAGMA` statements.

`export markdown` writes private Markdown files with mode `0600` into a directory created with mode `0700` when the directory does not already exist. It can export all conversations, one conversation, date-filtered conversations, or query-filtered conversations.

## Privacy Boundary

`aicrawl` v0.1 imports official local exports and captured web payload files, and can run read-only web-sync preflight checks. It does not use session-token scraping, live CDP/page-context fetching, browser automation, cloud sync, embeddings, background watches, or network import/search/export. Import, sync source import, sync preflight, search, SQL, Markdown export, and CrawlBar manifest generation are local operations.

Private data should stay in ignored local paths such as `imports/private/`, platform runtime directories, SQLite files, logs, and generated Markdown export directories. Public fixtures must be synthetic or redacted.

## CrawlBar And Control Surface

`metadata --json`, `status --json`, and `doctor --json` emit JSON control payloads safe for automation and CrawlBar. `crawlbar manifest` writes a manifest containing command metadata, runtime paths, capabilities, and privacy flags.

The manifest is generated from the current runtime config, so users can override the config path with `AICRAWL_CONFIG` or `--config` before generating it.
