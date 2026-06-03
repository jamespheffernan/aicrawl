# Commands

This reference matches the `aicrawl --help` command surface for v0.1.

```text
aicrawl archives official Claude and ChatGPT conversation exports locally.

Usage:
  aicrawl version
  aicrawl init [--json]
  aicrawl doctor [--json]
  aicrawl metadata [--json]
  aicrawl status [--json]
  aicrawl import <zip-or-json> [--provider claude|chatgpt|auto]
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

## `doctor`

Checks config presence, CrawlBar manifest presence, database readability, schema version, and FTS availability. This command is read-only.

```bash
aicrawl doctor
aicrawl doctor --json
```

## `import`

Imports one local official export ZIP or JSON file.

```bash
aicrawl import ./claude-export.zip --provider claude
aicrawl import ./chatgpt-export.zip --provider chatgpt
aicrawl import ./export.zip --provider auto
```

Provider defaults to auto-detection when omitted or set to `auto`. Imports are idempotent by source kind, provider, and source hash. Text output includes a reminder that source exports still contain private data.

## `conversations`

Lists conversations in newest-first order.

```bash
aicrawl conversations --limit 25
aicrawl conversations --provider chatgpt --since 2026-01-01 --until 2026-06-01
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
