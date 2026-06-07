# Schema

`aicrawl` uses SQLite schema version 3. Migrations are managed with `PRAGMA user_version`; binaries fail fast when a database has a newer schema version than they support.

## Migration Contract

- `internal/schema.Version` is the supported schema version.
- New databases start at `user_version = 0`.
- Migration v1 creates the initial v0.1 tables and indexes.
- Migration v2 adds durable sync cursor columns to `sync_state` and backfills `last_checked_at` from existing import freshness.
- Migration v3 adds `conversation_sync_status` for non-destructive live web detail state such as inaccessible conversations.
- Read-write opens run migrations.
- Read-only opens verify that the database is not newer than the binary.
- Stored timestamps use fixed-width UTC text with nanosecond precision so SQLite text ordering matches chronological ordering.

## Tables

### `providers`

Provider registry keyed by provider ID. v0.1 uses `claude`, `chatgpt`, `openclaw`, `codex`, `gemini`, `claude-code`, `cursor`, and `hermes`.

### `accounts`

Optional account/workspace identity rows when an export exposes stable account metadata. The raw account payload is preserved when available.

Important columns:

- `id`: stable provider-prefixed account ID.
- `provider`: provider ID.
- `raw_id`: provider account/workspace ID.
- `display_name`: optional display label.
- `raw_payload`: original JSON payload when present.

### `imports`

Import ledger keyed by `source_kind`, `provider`, and `source_hash`. This is the main idempotency boundary.

Important columns:

- `id`: stable import ID.
- `source_kind`: provider source type such as `claude_export`, `chatgpt_web`, `openclaw_jsonl`, `codex_jsonl`, `gemini_cli`, `claude_code_jsonl`, `cursor_store`, or `hermes_session`.
- `provider`: provider ID.
- `source_hash`: hash of the source file.
- `source_label`: redacted source label for operator visibility.
- count columns: conversations, messages, attachments, warnings.
- `first_seen_at` and `last_seen_at`: re-import tracking.

### `import_warnings`

Warnings emitted during import, keyed by import ID and ordinal. Warnings are intended to describe parse issues without logging private message text.

### `conversations`

Canonical conversation rows.

Important columns:

- `id`: stable provider-prefixed conversation ID.
- `provider`: provider ID.
- `account_id`: optional account reference.
- `raw_id`: provider conversation ID.
- `title`: optional title.
- `created_at` and `updated_at`: provider timestamps when available.
- `current_node_id`: provider current node when available.
- `raw_payload`: original conversation JSON.
- import tracking columns for first and last import.
- `message_count`: current stored message count.

`unique(provider, raw_id)` prevents duplicate conversations across overlapping exports.

### `messages`

Canonical message/node rows.

Important columns:

- `id`: stable provider-prefixed message ID.
- `provider`: provider ID.
- `conversation_id`: stable conversation ID.
- `raw_id`: provider message or node ID.
- `parent_id`: best-effort direct parent reference when present.
- `role`: normalized role.
- `sender`: optional provider sender display value.
- `ordinal`: deterministic order within the conversation.
- `is_current_path`: whether the message is on the known current path.
- `is_path_known`: whether path membership is known.
- `text`: normalized searchable transcript projection.
- `raw_payload`: original message/node JSON.

`unique(provider, conversation_id, raw_id)` prevents duplicate messages on re-import.

### `message_edges`

Explicit graph edges, used especially for ChatGPT `mapping` branches and regenerations.

Important columns:

- `provider`.
- `conversation_id`.
- `parent_message_id` and `child_message_id`: canonical IDs.
- `raw_parent_id` and `raw_child_id`: provider IDs.
- `edge_kind`: currently `parent_child`.
- import tracking columns.

The primary key includes provider, conversation, raw parent, raw child, and edge kind. This preserves branch structure without collapsing a graph into a single transcript.

### `message_versions`

Content-version rows keyed by message and content hash. This lets the archive record changed message payloads across overlapping exports while keeping the canonical message stable.

Important columns:

- `id`: stable version ID.
- `message_id`: canonical message ID.
- `content_hash`: hash of normalized content and raw payload.
- `text`: normalized version text.
- `raw_payload`: raw version payload.

### `attachments`

Attachment and extracted file metadata.

Important columns:

- `id`: stable attachment ID.
- `provider`.
- `conversation_id`.
- `message_id`: optional message reference.
- `kind`: provider-specific attachment kind normalized for storage.
- `filename` and `mime_type`: optional metadata.
- `text`: extracted searchable text when available.
- `raw_payload`: original attachment/file JSON.

Attachment text is indexed separately in FTS using role `attachment`.

### `messages_fts`

FTS5 virtual table for searchable message and attachment text.

Columns:

- `message_id` unindexed.
- `conversation_id` unindexed.
- `provider` unindexed.
- `role` unindexed.
- `body`: indexed normalized text.

The tokenizer is `unicode61`. User queries are normalized and safely quoted by `internal/textnorm` so reserved FTS terms are treated as search terms.

### `sync_state`

Local summary table for source-kind status. v0.1 writes it during imports, including `sync web --source` and live `sync web --cdp-url` or profile-launched imports with source kinds such as `chatgpt_web` and `claude_web`, and reads it during `sync web --dry-run` and `status --json` to report freshness.

Important columns:

- `source_kind`: source family such as `chatgpt_web`, `claude_web`, or `codex_jsonl`.
- `last_import_id` and `last_import_at`: most recent import that wrote or confirmed rows for this source kind.
- `last_checked_at`: most recent import or sync check, including no-change live web syncs.
- `conversation_count` and `message_count`: counts from the last import batch for this source kind.
- `cursor_kind`, `cursor_value`, and `cursor_at`: durable cursor metadata. File and captured-payload imports record `source_hash`; live web syncs record `provider_updated_at` when provider list/detail payloads expose update timestamps.
- `last_candidate_count`: count of list candidates observed during the last cursor-aware live web check when available.

### `conversation_sync_status`

Per-conversation live sync status keyed by source kind, provider, and provider raw conversation ID. This table is used to remember provider-list conversations whose detail payloads were skipped without deleting archived data.

Important columns:

- `source_kind`: web source family such as `chatgpt_web` or `claude_web`.
- `provider`: provider ID.
- `raw_id`: provider conversation ID observed in a web list response.
- `conversation_id`: stable provider-prefixed archive ID when known.
- `status`: currently `seen` for imported conversations or `inaccessible` for detail responses that returned 403, 404, or 410.
- `http_status`: provider HTTP status for inaccessible detail responses.
- `last_import_id`: import ledger ID for successful imported rows when available.
- `first_seen_at`, `last_seen_at`, and `last_checked_at`: local observation timestamps.

Successful imports upsert `status = seen`. Live detail responses with 403, 404, or 410 upsert `status = inaccessible` and do not delete conversations, messages, or raw payloads already in the archive.

## Raw Payload Preservation

Raw JSON payloads are stored as text columns in v0.1. Parser bugs can be repaired later by revisiting the raw payloads without needing the original export file, although users should still keep source exports if they want an independent copy.

Raw payloads are not normalized, redacted, or rewritten. Normalization applies only to the searchable `text` projection and FTS body.

## Stable IDs

Stable IDs are deterministic and provider-prefixed. CLI commands use these IDs for conversations and messages. SQLite row IDs are not part of the public interface.

## Search Semantics

Search supports:

- provider filter: `claude`, `chatgpt`, or `all`;
- role filter: user, assistant, system, developer, tool, attachment, unknown, or all;
- scope filter: visible, transcript, attachments, internal, or all;
- path filter: current or all;
- date bounds using `YYYY-MM-DD`;
- grouping by messages or conversations.

Default search scope is visible content on the current path.
