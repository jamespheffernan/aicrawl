# OpenClaw Handoff

This document summarizes the v0.1 handoff posture for OpenClaw maintainer review.

## Intent

`aicrawl` is intended to be donated to the OpenClaw organization as a local-first archive for personal AI conversation history. The first source release should prioritize correctness, privacy, and maintainability over more features.

## Boundary With `crawlkit`

`aicrawl` uses `github.com/openclaw/crawlkit` where behavior is reusable across local archive tools:

- platform-aware config and runtime path defaults;
- SQLite store opening, including read-only opens;
- control payload and manifest types.

Provider-specific behavior remains downstream in `aicrawl`:

- Claude and ChatGPT export schemas;
- provider detection;
- source-specific raw payload preservation;
- ChatGPT graph handling;
- Claude attachment/file parsing;
- import UX and privacy reminders;
- app-specific database schema and search defaults.

Move code into `crawlkit` only when it is clearly reusable across multiple OpenClaw archive apps and can be added with a small additive API.

## v0.1 Review Surfaces

Maintainers should review:

- CLI behavior in `internal/app/`.
- SQLite schema and migration behavior in `internal/schema/`.
- import idempotency and raw payload preservation in `internal/archive/`.
- Claude and ChatGPT parsing under `internal/ingest/`.
- OpenClaw, Codex, and Gemini local transcript parsing under `internal/ingest/`.
- ZIP safety and source limits in `internal/security/`.
- browser-profile sync preflight under `internal/sync/`.
- FTS query handling in `internal/textnorm/`.
- public docs and fixture hygiene.

## Privacy Posture

v0.1 imports local official exports, captured ChatGPT/Claude web detail payload files, and OpenClaw/Codex/Gemini local session files. It can also run read-only web-sync preflight. It does not make network calls for import, sync source import, sync preflight, search, SQL, Markdown export, or CrawlBar manifest generation. It does not implement session-token scraping, live CDP/page-context fetching, browser automation, background sync, or cloud storage.

Private data is protected by:

- private runtime directories and files where the CLI creates them;
- ignored import/export/runtime paths;
- read-only opens for inspection commands;
- read-only SQL validation;
- ZIP-slip protection;
- synthetic/redacted fixtures only;
- redacted warning labels rather than private message text.

## Maintainer Review Checklist

- Confirm the module path is `github.com/openclaw/aicrawl`.
- Confirm the first public release version is `0.1.0`.
- Run the validation commands in `docs/RELEASE_CHECKLIST.md`.
- Confirm public docs match implemented behavior.
- Confirm `docs/IMPLEMENTATION_PLAN.md`, `docs/CODEX_GOAL_PROMPT.md`, and private real-export plans are ignored and not required for build or tests.
- Confirm no real exports, databases, logs, generated Markdown exports, local config, or private transcripts are tracked.
- Confirm fixtures under `testdata/redacted/` are synthetic or redacted.

## Known Future Work

Future work should stay out of v0.1 unless separately approved:

- local Claude Code transcript ingestion;
- Cursor local store ingestion;
- live ChatGPT/Claude browser-profile sync after endpoint contracts are proven current;
- richer attachment extraction;
- schema migrations beyond v1;
- packaged releases;
- backup/restore;
- TUI;
- embeddings or semantic search;
- watch/scheduler;
- mirror or publish flows;
- enterprise compliance ingestion.

Each future feature should include a privacy review and fixture strategy before implementation.
