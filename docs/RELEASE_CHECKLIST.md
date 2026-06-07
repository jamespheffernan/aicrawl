# Release Checklist

Use this checklist before a public v0.1.0 source release or OpenClaw maintainer handoff.

## Preconditions

- Real Claude and ChatGPT exports, if used for private validation, live only under ignored private paths such as `imports/private/`.
- Public fixtures are synthetic or redacted.
- No release tag, GitHub publication, package-manager distribution, or Homebrew work happens without explicit approval.

## Required Go Checks

```bash
GOWORK=off go mod tidy
git diff --exit-code -- go.mod go.sum
GOWORK=off go vet ./...
GOWORK=off go test -count=1 ./...
```

Do not claim these passed unless they were run in the current release-prep session.

## Public Temp-Home Smoke

Build into a temp path:

```bash
SMOKE="$(mktemp -d)"
BIN="$SMOKE/aicrawl"
GOWORK=off go build -o "$BIN" ./cmd/aicrawl
```

Use an isolated home:

```bash
TMP_HOME="$SMOKE/home"
mkdir -p "$TMP_HOME"
export HOME="$TMP_HOME"
export XDG_CONFIG_HOME="$TMP_HOME/.config"
export XDG_CACHE_HOME="$TMP_HOME/.cache"
export XDG_DATA_HOME="$TMP_HOME/.local/share"
export XDG_STATE_HOME="$TMP_HOME/.local/state"
```

Run:

```bash
"$BIN" --help
"$BIN" version
"$BIN" init
"$BIN" doctor --json
"$BIN" metadata --json
"$BIN" status --json
"$BIN" import ./testdata/redacted/chatgpt-export.fixture.zip --dry-run --json
"$BIN" import ./testdata/redacted/claude-export.fixture.zip --provider claude --json
"$BIN" import ./testdata/redacted/chatgpt-export.fixture.zip --provider chatgpt --json
"$BIN" reconcile ./testdata/redacted/chatgpt-export.fixture.zip --provider chatgpt --json
"$BIN" import ./testdata/redacted/openclaw-session.fixture.jsonl --provider openclaw --json
"$BIN" import ./testdata/redacted/codex-session.fixture.jsonl --provider codex --json
mkdir -p "$SMOKE/codex-root"
cp ./testdata/redacted/codex-session.fixture.jsonl "$SMOKE/codex-root/session.jsonl"
"$BIN" import "$SMOKE/codex-root" --provider codex --dry-run --json
"$BIN" import ./testdata/redacted/gemini-session.fixture.json --provider gemini --json
"$BIN" import ./testdata/redacted/claude-code-session.fixture.jsonl --provider claude-code --json
"$BIN" sync web --provider chatgpt --source ./testdata/redacted/chatgpt-web-conversation.fixture.json --json
"$BIN" sync web --provider claude --source ./testdata/redacted/claude-web-conversation.fixture.json --json
"$BIN" import ./testdata/redacted/claude-export.fixture.zip --provider claude --json
"$BIN" import ./testdata/redacted/chatgpt-export.fixture.zip --provider chatgpt --json
"$BIN" sync web --provider chatgpt --source ./testdata/redacted/chatgpt-web-conversation.fixture.json --dry-run --json
"$BIN" status --json
"$BIN" conversations --limit 10
"$BIN" messages --conversation chatgpt:chatgpt-conv-branchy --path all
"$BIN" search "openclaw jsonl fixture assistant phrase" --provider openclaw --json
"$BIN" search "codex jsonl fixture assistant phrase" --provider codex --json
"$BIN" search "gemini cli fixture assistant phrase" --provider gemini --json
"$BIN" search "claude code jsonl fixture assistant phrase" --provider claude-code --json
"$BIN" search "web sync claude fixture assistant phrase" --provider claude --json
"$BIN" search "AND OR NOT NEAR *"
"$BIN" sql "select count(*) from messages"
! "$BIN" sql "update messages set role = 'x'"
"$BIN" schedule launchd --provider chatgpt --cdp-url http://127.0.0.1:9222 --out "$SMOKE/aicrawl-sync.plist" --json
"$BIN" schedule launchd --provider chatgpt --profile "$SMOKE/chatgpt-profile" --out "$SMOKE/aicrawl-sync-profile.plist" --json
"$BIN" export markdown --out "$SMOKE/exported-md"
"$BIN" crawlbar manifest --out "$SMOKE/aicrawl.crawlbar.json"
```

Optional live-browser smoke, only when a logged-in provider browser is already running with a Chrome DevTools endpoint:

```bash
"$BIN" sync web --provider chatgpt --cdp-url http://127.0.0.1:9222 --max-conversations 1 --json
```

Optional profile-launch smoke, only when Chrome/Chromium/Microsoft Edge is available and it is acceptable for the command to open a provider browser window:

```bash
"$BIN" sync web --provider chatgpt --profile "$SMOKE/chatgpt-profile" --max-conversations 1 --json
```

Cursor import coverage is exercised by generated SQLite fixtures in the Go test suite rather than a checked-in binary `store.db` fixture.

Expected result:

- JSON commands emit valid JSON.
- Fixture re-imports report already-imported status.
- Reconciliation reports missing and divergent counts without writing the archive.
- Local transcript and captured web payload fixtures are searchable after import.
- Directory dry-runs for local transcript providers report aggregate source counts without writing the archive or emitting private source paths.
- `status --json` reports `web_sync` freshness for ChatGPT and Claude web source kinds.
- Reserved-term search succeeds.
- Read-only SQL succeeds.
- Mutating SQL is rejected.
- CDP and profile LaunchAgent plist generation succeeds without credentials.
- Markdown export writes private files.
- CrawlBar manifest generation succeeds.

## Private Real-Export Smoke

Run only against ignored private files and ignored outputs. Do not paste filenames, account identifiers, message text, raw JSON, Markdown output, or search snippets into public docs or issues.

Procedure:

1. Build the CLI into an ignored private path or temp path.
2. Use a temp or ignored `HOME`, `XDG_CONFIG_HOME`, `XDG_CACHE_HOME`, `XDG_DATA_HOME`, and `XDG_STATE_HOME`.
3. Import the real Claude export.
4. Import the real ChatGPT export.
5. Re-import both files and confirm idempotency.
6. Run aggregate-only checks:
   - `doctor --json`
   - `status --json`
   - `sql "select count(*) from conversations"`
   - `sql "select count(*) from messages"`
   - `sql "select count(*) from message_edges"`
   - a small search with output redirected to an ignored file
   - Markdown export to an ignored private directory
   - CrawlBar manifest generation to an ignored private path
7. Report only pass/fail and aggregate counts.

## Documentation Checks

Compare docs to implementation:

```bash
aicrawl --help
```

- The help text must appear exactly in `docs/COMMANDS.md`.
- `README.md` command examples must exist in the CLI.
- `docs/SCHEMA.md` must name every table created in `internal/schema/schema.go`.
- `docs/EXPORTS.md` must describe only official local export inputs.
- `docs/OPENCLAW_HANDOFF.md` must keep provider-specific parsing downstream in `aicrawl`.

## Git And Privacy Hygiene

Run:

```bash
git status --short --ignored
git check-ignore docs/IMPLEMENTATION_PLAN.md
git check-ignore docs/CODEX_GOAL_PROMPT.md
git check-ignore docs/REAL_EXPORT_E2E_PLAN.private.md
git check-ignore imports/private/<private-file>
```

Check for accidentally unignored private files:

```bash
git status --short --untracked-files=all
```

The public commit boundary should include only public source, public docs, synthetic fixtures, `.gitignore`, `AGENTS.md`, `LICENSE`, `README.md`, `go.mod`, and `go.sum`.

Do not commit:

- real exports;
- extracted provider export JSON;
- SQLite databases and WAL/SHM files;
- generated Markdown exports;
- private plans or prompts;
- logs and caches;
- credentials, tokens, or local environment files.
