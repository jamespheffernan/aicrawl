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

## Import Contract

The `cursor` importer opens one `store.db` read-only and treats it as one conversation.

- Conversation identity, title, and created time are read from hex-encoded `meta.value` at key `0` when present.
- If metadata cannot be decoded, the parent directory name is used as the session ID fallback.
- Importable message rows are JSON `blobs.data` values with `role` and non-empty visible text.
- Message order uses SQLite `rowid`, which reflects store insertion order in the observed table shape.
- Supported visible roles are `user`, `assistant`, and `system`.
- Content arrays keep only blocks with `type: "text"`.
- Non-JSON blobs, `tool` rows, tool-call blocks, and tool-result blocks are skipped to avoid indexing internal command payloads as chat text.

Cursor stores do not expose branch/path metadata through the observed message blobs, so imported Cursor messages have path membership marked unknown.
