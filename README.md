# aicrawl

`aicrawl` is a local-first archive for personal AI conversation history. The v0.1 CLI imports official Claude and ChatGPT export ZIP/JSON files, syncs recent ChatGPT and Claude web conversations through an attached or launched authenticated browser target or captured payload files, can seed ChatGPT web detail sync from native ChatGPT macOS app cache conversation IDs, and imports local OpenClaw/Codex/Gemini/Claude Code/Cursor/Hermes session files into a private SQLite archive. It preserves raw JSON payloads, indexes searchable message and extracted attachment/file text with FTS5, and exports conversations as Markdown.

The project follows the OpenClaw pattern: provider-specific parsing lives in `aicrawl`, while reusable local archive mechanics use `github.com/openclaw/crawlkit` where it fits.

## Install

This repository builds with Go 1.26.2 or newer because the current `crawlkit` module requires that toolchain.

```bash
GOWORK=off go build ./cmd/aicrawl
```

## Quickstart

```bash
aicrawl init
aicrawl import ./chatgpt-export.zip --dry-run --json
aicrawl import ./claude-export.zip --provider claude
aicrawl import ./chatgpt-export.zip --provider chatgpt
aicrawl import ./openclaw-session.jsonl --provider openclaw
aicrawl import ./codex-session.jsonl --provider codex
aicrawl import ~/.codex/sessions --provider codex --dry-run --json
aicrawl import ./gemini-session.json --provider gemini
aicrawl import ./claude-code-session.jsonl --provider claude-code
aicrawl import "$HOME/Library/Application Support/Claude" --provider claude-code --dry-run --json
aicrawl import ./store.db --provider cursor
aicrawl import "$HOME/Library/Application Support/Cursor" --provider cursor --dry-run --json
aicrawl import ~/.hermes/state.db --provider hermes
aicrawl import ~/.hermes --provider hermes --dry-run --json
aicrawl sync web --provider chatgpt --source ./chatgpt-web-conversation.json
aicrawl sync web --provider chatgpt --cdp-url http://127.0.0.1:9222 --max-conversations 50
aicrawl sync web --provider chatgpt --cdp-url http://127.0.0.1:9222 --chatgpt-app-cache "$HOME/Library/Application Support/com.openai.chat" --max-conversations 50
aicrawl sync web --provider chatgpt --profile ~/.cache/aicrawl/browser-profiles/chatgpt --max-conversations 50
aicrawl sync web --provider chatgpt --dry-run --json
aicrawl reconcile ./chatgpt-export.zip --provider chatgpt --json
aicrawl conversations --limit 25
aicrawl messages --conversation <conversation-id> --path current
aicrawl search "known phrase"
aicrawl search "finance" --group conversations --sort recent --scope visible
aicrawl messages --conversation <conversation-id> --around <message-id> --context 5
aicrawl sql "select count(*) from messages"
aicrawl export markdown --out ./exported-md
aicrawl export markdown --conversation <conversation-id> --out ./conversation-md
aicrawl schedule launchd --provider chatgpt --profile ~/.cache/aicrawl/browser-profiles/chatgpt --interval-minutes 15
aicrawl crawlbar manifest
```

Automation and CrawlBar control surfaces:

```bash
aicrawl metadata --json
aicrawl status --json
aicrawl doctor --json
```

## Supported Sources

v0.1 supports local official export files, captured ChatGPT and Claude web conversation payloads, and selected local agent transcript files:

- Claude official export ZIPs or extracted JSON containing conversation data.
- ChatGPT official export ZIPs or extracted JSON containing `conversations.json`, export batch JSON, or top-level conversations with `mapping`.
- `aicrawl import <path> --dry-run --json` reports candidate provider, source kind, conversation count, message count, attachment count, and warnings without creating or writing the archive.
- `aicrawl sync web --provider chatgpt|claude --cdp-url <url>` attaches to an already authenticated browser target and fetches bounded recent conversation list/detail payloads from page context under `chatgpt_web` or `claude_web`. Provider update cursors are stored when available so later syncs can skip older list candidates and report no-change checks without rewriting the archive. Detail responses with 403, 404, or 410 are recorded as inaccessible status rows without deleting archived conversations.
- `aicrawl sync web --provider chatgpt --chatgpt-app-cache "$HOME/Library/Application Support/com.openai.chat" --cdp-url <url>` discovers ChatGPT macOS app conversation IDs from `conversations-v3-*/*.data` filenames only, then fetches those exact conversation details through the same authenticated same-origin ChatGPT web API path. It does not read or decode opaque native app cache bodies.
- `aicrawl sync web --provider chatgpt|claude --profile <dir>` launches or reuses a dedicated browser profile with Chrome DevTools enabled. If the profile is new, it opens the provider page and reports `login_required` so you can log in normally and rerun sync.
- `aicrawl sync web --provider chatgpt|claude --source <json-or-zip>` imports captured provider conversation detail payloads under the same source kinds.
- `aicrawl sync web --provider chatgpt|claude --dry-run` validates the browser-profile boundary, checks optional redacted browser network captures for list/detail conversation endpoints, reports captured payload counts, and reports archive freshness. When `--cdp-url` is supplied and a provider page is already open, dry-run also performs list-only same-origin browser fetches to count candidate conversations without fetching detail payloads or writing the archive.
- `aicrawl reconcile <official-export> --provider chatgpt|claude|auto --json` compares a periodic official export against the local archive and reports missing conversation/message coverage plus divergent message projections without writing.
- `aicrawl schedule launchd --provider chatgpt|claude [--cdp-url <url> | --profile <dir>] [--chatgpt-app-cache <dir>]` writes a macOS LaunchAgent plist for recurring bounded web sync. It stores only command arguments, not browser credentials.
- OpenClaw session JSONL with `session` and `message` events, including Discord/Telegram `sourceChannel` and sender metadata when OpenClaw records it.
- Codex rollout JSONL with `session_meta` and `response_item` message events.
- Gemini CLI session JSON with `sessionId` and `messages`.
- Claude Code project JSONL with visible `user` and `assistant` message text. Control events, thinking blocks, tool calls, and tool results are skipped. Directory import can point at `~/.claude/projects` for CLI sessions or `~/Library/Application Support/Claude` for Claude desktop/local-agent sessions that embed Claude Code-style transcript JSONL files.
- Cursor `store.db` and installed Cursor `state.vscdb` SQLite files with visible `user`, `assistant`, and `system` text. Non-message blobs, tool calls, and tool results are skipped.
- Hermes `state.db` SQLite session stores and exported Hermes session `.json`/`.jsonl` files. `state.db` is opened read-only and imports visible `user`, `assistant`, `system`, `developer`, and `tool` message content while skipping session metadata and empty/internal records.
- Directory import for local transcript roots: OpenClaw/Codex/Claude Code discover `*.jsonl`, Gemini discovers session-shaped `*.json`, Cursor discovers `store.db` and `state.vscdb`, and Hermes prefers a root `state.db` before falling back to `state.db`, `*.jsonl`, and `session_*.json` discovery. Directory reports aggregate source counts without emitting full private paths.

`aicrawl` does not call Claude, ChatGPT, Anthropic, or OpenAI network APIs during import, captured source import, profile-only sync preflight, search, SQL, Markdown export, or CrawlBar manifest generation. Live web sync and `sync web --dry-run --cdp-url` use same-origin browser page fetches against the provider web app, with authentication kept inside the attached browser profile.

## Not Supported In v0.1

- Session-token scraping.
- Unattended provider login.
- Browser automation to click export buttons.
- Enterprise compliance API ingestion.
- Embeddings or semantic search.
- TUI, watch daemon, cloud sync, mirror, publish, or backups.
- Importing archived conversations back into Claude or ChatGPT.

## Privacy

Exports and archive databases contain private conversation data. Keep real export files and generated archives in private ignored paths such as `imports/private/` during local testing.

The CLI creates runtime directories, config files, Markdown exports, and CrawlBar manifest files with private permissions where it creates them. It rejects unsafe ZIP paths and opens SQL inspection paths read-only.

`aicrawl import` does not delete source exports. Delete or archive them manually when you no longer need them.

## Docs

- [Architecture](docs/ARCHITECTURE.md)
- [Schema](docs/SCHEMA.md)
- [Commands](docs/COMMANDS.md)
- [Official exports](docs/EXPORTS.md)
- [Live provider smoke](docs/LIVE_PROVIDER_SMOKE.md)
- [OpenClaw handoff](docs/OPENCLAW_HANDOFF.md)
- [Release checklist](docs/RELEASE_CHECKLIST.md)

## Validation

Expected verification before handoff:

```bash
GOWORK=off go mod tidy
git diff --exit-code -- go.mod go.sum
GOWORK=off go vet ./...
GOWORK=off go test -count=1 ./...
```

Synthetic fixtures live under `testdata/redacted/`. Do not commit real exports, local archives, logs, caches, credentials, or private planning docs.
