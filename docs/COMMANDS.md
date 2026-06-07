# Commands

This reference matches the `aicrawl --help` command surface for v0.1.

```text
aicrawl archives AI conversation sources locally.

Usage:
  aicrawl version
  aicrawl init [--json]
  aicrawl doctor [--json]
  aicrawl metadata [--json]
  aicrawl status [--json]
  aicrawl import <zip-json-or-jsonl-db> [--provider claude|chatgpt|openclaw|codex|gemini|claude-code|cursor|auto] [--dry-run] [--json]
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
```

## Global Options

- `--config <path>`: read or write a specific config file. If omitted, `AICRAWL_CONFIG` or platform defaults are used.
- `--json`: request JSON output where the command supports it.
- `--help`: print help and exit.

## `version`

Prints the CLI version.

```bash
aicrawl version
```

## `init`

Creates the config file when missing, creates private runtime directories, opens the SQLite archive, and applies schema migrations.

```bash
aicrawl init
aicrawl init --json
```

## `metadata`

Prints local app metadata and CrawlBar-compatible manifest details. This command is read-only and can run before `init`.

```bash
aicrawl metadata --json
```

## `status`

Reports archive state and counts. This command is read-only and can run before `init`.

```bash
aicrawl status
aicrawl status --json
```

JSON output includes a `web_sync` array for `chatgpt_web` and `claude_web` with freshness state, last import timestamp, and synced conversation/message counts. This lets operators see stale or never-run web syncs from the normal status surface without running provider-specific dry-runs.

## `doctor`

Checks config presence, CrawlBar manifest presence, database readability, schema version, and FTS availability. This command is read-only.

```bash
aicrawl doctor
aicrawl doctor --json
```

## `import`

Imports one local source ZIP, JSON, JSONL, or Cursor `store.db` file.

```bash
aicrawl import ./chatgpt-export.zip --dry-run --json
aicrawl import ./claude-export.zip --provider claude
aicrawl import ./chatgpt-export.zip --provider chatgpt
aicrawl import ./openclaw-session.jsonl --provider openclaw
aicrawl import ./codex-session.jsonl --provider codex
aicrawl import ./gemini-session.json --provider gemini
aicrawl import ./claude-code-session.jsonl --provider claude-code
aicrawl import ./store.db --provider cursor
aicrawl import ./export.zip --provider auto
```

Provider defaults to official Claude/ChatGPT export auto-detection when omitted or set to `auto`. Local agent transcript providers must be selected explicitly. Claude Code imports keep visible user/assistant text and skip control events, thinking blocks, tool calls, and tool results. Cursor imports open `store.db` read-only, use meta identity/title when available, order visible message blobs by SQLite `rowid`, and skip non-JSON blobs, tool calls, and tool results. Imports are idempotent by source kind, provider, and source hash. Text output includes a reminder that source files still contain private data.

With `--dry-run`, import parses the source and reports candidate counts without opening, creating, or writing the archive. JSON output includes provider, source kind, conversation count, message count, attachment count, and bounded parser warnings. It does not include message bodies or full source paths.

## `reconcile`

Compares an official Claude or ChatGPT export against an existing local archive without writing to the archive.

```bash
aicrawl reconcile ./chatgpt-export.zip --provider chatgpt --json
aicrawl reconcile ./claude-export.zip --provider claude
aicrawl reconcile ./export.zip --provider auto --json
```

The report includes source conversation/message counts, archived conversation/message counts, missing counts, parser warnings, and a next step when backfill is needed. If rows are missing, run `aicrawl import` with the same official export to backfill through the normal idempotent import path.

## `sync web`

Runs browser-profile preflight, optional redacted endpoint-contract discovery, captured payload import, or live CDP page-context fetch for ChatGPT or Claude web data.

```bash
aicrawl sync web --provider chatgpt --dry-run --json
aicrawl sync web --provider chatgpt --source ./chatgpt-web-conversation.json
aicrawl sync web --provider claude --source ./claude-web-conversation.json --json
aicrawl sync web --provider chatgpt --cdp-url http://127.0.0.1:9222 --max-conversations 50 --json
aicrawl sync web --provider chatgpt --profile ~/.cache/aicrawl/browser-profiles/chatgpt --max-conversations 50 --json
aicrawl sync web --provider claude --profile ~/.cache/aicrawl/browser-profiles/claude --dry-run
aicrawl sync web --provider chatgpt --cdp-url http://127.0.0.1:9222 --capture ./chatgpt-network.json --dry-run --json
```

With `--source`, the command imports captured provider conversation detail payloads into the local archive using source kinds `chatgpt_web` or `claude_web`. Imports are idempotent by source kind, provider, and source hash.

With `--cdp-url` and no `--source`, non-dry-run mode attaches to an already running browser's Chrome DevTools endpoint, finds or opens a provider page target, and runs same-origin `fetch()` calls from that page context. Authentication stays inside the browser profile. The fetched detail batch is written to a private temporary cache file, imported through the normal archive path, then removed.

With `--profile` and no `--cdp-url`, non-dry-run mode launches Chrome/Chromium/Microsoft Edge with a dedicated `--user-data-dir`, local remote debugging bound to `127.0.0.1`, and the provider home page. Chrome's ephemeral `--remote-debugging-port=0` behavior is the default; pass `--remote-debugging-port <port>` only when you need a stable local port. If the profile is new, the command reports `login_required`, leaves the archive untouched, and tells you to log in normally before rerunning sync. Pass `--browser <path>` or set `AICRAWL_BROWSER` when the browser executable is not in a common location.

`--max-conversations` bounds live web sync to the most recent list/detail records fetched in one run. It defaults to `50`.

With `--dry-run`, the command does not write the archive. It reports:

- `auth_state`: whether a dedicated browser profile exists, a CDP target is configured, or login is still required.
- `endpoint_contract_state`: `matched`, `partial`, `stale`, `missing`, `empty`, or `not_checked` for a redacted network capture.
- `source`: candidate conversation, message, attachment, and warning counts when `--source` is present.
- `freshness`: whether the archive has seen `chatgpt_web` or `claude_web` sync rows.

`--capture` accepts a JSON browser network export or similar structured event dump. Only request URLs, methods, and status codes are inspected. Query strings, fragments, headers, cookies, and bearer tokens are not emitted in the report.

Without `--source`, non-dry-run `sync web` uses `--cdp-url` when provided; otherwise it launches the dedicated provider profile and discovers the local CDP endpoint from Chrome's `DevToolsActivePort` file. It does not read browser cookies, tokens, headers, or session storage.

## `schedule launchd`

Writes a macOS LaunchAgent plist for recurring bounded web sync. The command does not load or start the agent.

```bash
aicrawl schedule launchd --provider chatgpt --cdp-url http://127.0.0.1:9222 --interval-minutes 15
aicrawl schedule launchd --provider chatgpt --profile ~/.cache/aicrawl/browser-profiles/chatgpt --interval-minutes 15
aicrawl schedule launchd --provider claude --cdp-url http://127.0.0.1:9222 --max-conversations 25 --out ~/Library/LaunchAgents/com.openclaw.aicrawl.sync.claude.plist --json
```

The generated plist runs one of these shapes:

```bash
aicrawl sync web --provider <provider> --cdp-url <url> --max-conversations <n> --json
aicrawl sync web --provider <provider> --profile <dir> --max-conversations <n> --json
```

It stores the CDP URL or profile path and normal command arguments, but no cookies, bearer tokens, session headers, or browser storage. For `--profile`, the first run may open the browser and require a normal interactive login. Then load the plist when ready:

```bash
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.openclaw.aicrawl.sync.chatgpt.plist
```

## `conversations`

Lists conversations in newest-first order.

```bash
aicrawl conversations --limit 25
aicrawl conversations --provider chatgpt --since 2026-01-01 --until 2026-06-01
aicrawl conversations --provider codex --json
aicrawl conversations --provider claude-code --json
aicrawl conversations --provider cursor --json
aicrawl conversations --json
```

Defaults:

- provider: `all`
- limit: `50`

## `messages`

Lists messages for one conversation.

```bash
aicrawl messages --conversation <conversation-id>
aicrawl messages --conversation <conversation-id> --path all
aicrawl messages --conversation <conversation-id> --around <message-id> --context 8
aicrawl messages --conversation <conversation-id> --around <message-id> --before 3 --after 10
```

Defaults:

- path: `current`
- context around a message: `5` before and `5` after

Use `--path all` to include non-current branches when they are stored.

## `search`

Searches the FTS index.

```bash
aicrawl search "known phrase"
aicrawl search "known phrase" --group conversations --sort recent
aicrawl search "known phrase" --provider claude --role user
aicrawl search "known phrase" --provider openclaw
aicrawl search "known phrase" --scope attachments
aicrawl search "known phrase" --scope internal --path all
aicrawl search "AND OR NOT NEAR *"
```

Defaults:

- group: `messages`
- provider: `all`
- scope: `visible`
- role: `all`
- path: `current`
- sort: `relevance`
- limit: `25`

Scope meanings:

- `visible`: user, assistant, unknown transcript text, and attachment text.
- `transcript`: user, assistant, and unknown transcript text.
- `attachments`: attachment/file text.
- `internal`: roles outside visible content, such as system, developer, and tool.
- `all`: all indexed text.

## `sql`

Runs one read-only SQL statement against the archive.

```bash
aicrawl sql "select count(*) from messages"
aicrawl sql "pragma user_version" --json
```

Allowed statement families are `SELECT`, `WITH`, and a small allowlist of safe `PRAGMA` statements. Mutating SQL is rejected before execution, and the database is opened read-only.

## `export markdown`

Exports conversations as private Markdown files.

```bash
aicrawl export markdown --out ./exported-md
aicrawl export markdown --out ./one-md --conversation <conversation-id>
aicrawl export markdown --out ./topic-md --query "known phrase" --sort recent
aicrawl export markdown --out ./claude-md --provider claude --since 2026-01-01
```

Defaults:

- provider: `all`
- path: `all`
- query scope: `visible` when `--query` is used
- query sort: `recent` when `--query` is used

`--role`, `--scope`, and `--sort` require `--query` for Markdown export.

## `crawlbar manifest`

Writes a CrawlBar manifest.

```bash
aicrawl crawlbar manifest
aicrawl crawlbar manifest --out ~/.crawlbar/apps/aicrawl.json
aicrawl crawlbar manifest --out ./aicrawl.crawlbar.json --json
```

The manifest file is written with private file permissions.
