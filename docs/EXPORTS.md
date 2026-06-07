# Official Exports

`aicrawl` v0.1 imports official local Claude and ChatGPT export files. It does not log in for you, click export buttons, call private product APIs, or scrape browser sessions.

## Supported Files

### Claude

Supported Claude inputs:

- an official export ZIP containing JSON files, including conversation data;
- an extracted JSON file with top-level conversations containing stable `uuid` values and `chat_messages`.

The parser tolerates unknown fields and preserves raw payloads. It requires stable IDs so re-imports remain idempotent.

### ChatGPT

Supported ChatGPT inputs:

- an official export ZIP containing `conversations.json` or export batch JSON files;
- an extracted JSON file with top-level conversations containing a `mapping`.

The parser treats ChatGPT `mapping` as a graph. Branches and regenerations are stored with explicit edges instead of being flattened into one transcript.

## Where To Put Private Exports

For local testing in this repository, use the ignored private directory:

```bash
mkdir -p imports/private
chmod 700 imports/private
```

Place downloaded export ZIPs in `imports/private/`. That directory is intentionally ignored by git.

Before committing, verify private files are ignored:

```bash
git check-ignore imports/private/<your-export-file>.zip
git status --short --ignored
```

Do not copy real exports into `testdata/`, `docs/`, logs, public Markdown exports, or screenshots.

## Basic Import

```bash
aicrawl init
aicrawl import imports/private/<claude-export>.zip --provider claude
aicrawl import imports/private/<chatgpt-export>.zip --provider chatgpt
```

Use `--provider auto` only when you want the CLI to infer the provider from export shape.

## After Import

The source export still contains private data. Keep it in a private ignored directory, move it to another private archive location, or delete it manually. `aicrawl` never deletes source exports automatically.

The SQLite archive also contains private conversation data. Runtime database, WAL, SHM, cache, log, and Markdown export paths should remain ignored and private.

## Reconciliation

Use official exports as periodic backfill and validation, not as the high-frequency sync lane:

```bash
aicrawl reconcile imports/private/<chatgpt-export>.zip --provider chatgpt --json
aicrawl reconcile imports/private/<claude-export>.zip --provider claude --json
```

`reconcile` opens the existing archive read-only, streams the official export through the same parser used by `import`, and reports source, archived, missing, and divergent conversation/message counts. Divergence means an existing archived message ID has a different normalized text projection than the export parser currently produces. It does not write any archive rows or emit message bodies. If the report shows missing or divergent coverage, run `aicrawl import` with the same export to backfill or refresh through the normal idempotent import path.

## v0.1 Non-Goals

v0.1 does not support:

- session-token scraping;
- browser automation for export download;
- private Claude, Anthropic, ChatGPT, or OpenAI APIs;
- enterprise compliance API ingestion;
- cloud sync;
- embeddings or semantic search;
- TUI or background watch modes;
- mirror, publish, backup, or restore flows;
- importing archived conversations back into Claude or ChatGPT.

Those features should not be added without a separate design and privacy review.
