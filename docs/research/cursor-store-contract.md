# Cursor Store Contract

Observed date: 2026-06-07

Cursor local chats were found under a session-scoped directory containing `store.db` SQLite files.

## Observed Shape

The sampled SQLite database had:

```sql
CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB);
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT);
```

Some `blobs.data` values are valid JSON objects with message-like keys:

- `role`
- `content`
- `id`
- `providerOptions`

Other blob values are non-JSON binary/index payloads. JSON message blobs include roles such as `user`, `system`, `assistant`, and `tool`, with `content` represented as text or arrays.

## Import Gate

Cursor ingestion is not enabled yet because a safe importer needs a deterministic way to reconstruct:

- conversation/session identity
- message ordering
- parent/branch relationships, if present
- which blobs are visible transcript text versus tool/internal payloads
- stable title and timestamps

Until that contract is understood, adding a parser would risk importing unordered fragments or indexing private tool payloads as ordinary chat text.
