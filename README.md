# aicrawl

`aicrawl` is a local-first archive for personal AI conversation history. The v0.1 CLI imports official Claude and ChatGPT export ZIP/JSON files, captured ChatGPT and Claude web conversation payloads, and local OpenClaw/Codex/Gemini session files into a private SQLite archive, preserves raw JSON payloads, indexes searchable message and extracted attachment/file text with FTS5, and exports conversations as Markdown.

The project follows the OpenClaw pattern: provider-specific parsing lives in `aicrawl`, while reusable local archive mechanics use `github.com/openclaw/crawlkit` where it fits.

## Install

This repository builds with Go 1.26.2 or newer because the current `crawlkit` module requires that toolchain.

```bash
GOWORK=off go build ./cmd/aicrawl
```

## Quickstart

```bash
aicrawl init
aicrawl import ./claude-export.zip --provider claude
aicrawl import ./chatgpt-export.zip --provider chatgpt
aicrawl import ./openclaw-session.jsonl --provider openclaw
aicrawl import ./codex-session.jsonl --provider codex
aicrawl import ./gemini-session.json --provider gemini
aicrawl sync web --provider chatgpt --source ./chatgpt-web-conversation.json
aicrawl sync web --provider chatgpt --dry-run --json
aicrawl conversations --limit 25
aicrawl messages --conversation <conversation-id> --path current
aicrawl search "known phrase"
aicrawl search "finance" --group conversations --sort recent --scope visible
aicrawl messages --conversation <conversation-id> --around <message-id> --context 5
aicrawl sql "select count(*) from messages"
aicrawl export markdown --out ./exported-md
aicrawl export markdown --conversation <conversation-id> --out ./conversation-md
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
- `aicrawl sync web --provider chatgpt|claude --source <json-or-zip>` imports captured provider conversation detail payloads under `chatgpt_web` or `claude_web`.
- `aicrawl sync web --provider chatgpt|claude --dry-run` validates the browser-profile boundary, checks optional redacted browser network captures for list/detail conversation endpoints, reports captured payload counts, and reports archive freshness.
- OpenClaw session JSONL with `session` and `message` events.
- Codex rollout JSONL with `session_meta` and `response_item` message events.
- Gemini CLI session JSON with `sessionId` and `messages`.

`aicrawl` does not call Claude, ChatGPT, Anthropic, or OpenAI network APIs during import, sync preflight, search, SQL, Markdown export, or CrawlBar manifest generation.

## Not Supported In v0.1

- Session-token scraping.
- Browser automation to click export buttons.
- Live browser fetching through CDP/page-context calls.
- Claude Code and Cursor local store ingestion.
- Enterprise compliance API ingestion.
- Embeddings or semantic search.
- TUI, watch daemon, scheduler, cloud sync, mirror, publish, or backups.
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
