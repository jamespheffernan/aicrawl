# Cursor Store Contract

Observed date: 2026-06-07

Cursor local chats have been observed in two SQLite shapes:

- legacy/synthetic session-scoped `store.db` files;
- installed Cursor workspace `state.vscdb` files under the app support directory.

## Observed Shape

The sampled legacy `store.db` database had:

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

The sampled installed Cursor `state.vscdb` databases had:

```sql
CREATE TABLE ItemTable (key TEXT, value BLOB);
CREATE TABLE cursorDiskKV (key TEXT, value BLOB);
```

Conversation message-like rows were found in `cursorDiskKV` keys shaped as `agentKv:blob:<id>`. Values are hex-encoded payloads; after hex decoding, importable rows are JSON objects with keys such as `role`, `content`, `id`, and optional `providerOptions`. Other `cursorDiskKV` rows, including `composer.content.*`, can contain file contents or non-message app state and are not treated as transcript messages.

## Import Contract

The `cursor` importer opens one `store.db` or `state.vscdb` read-only and treats it as one conversation.

- For `store.db`, conversation identity, title, and created time are read from hex-encoded `meta.value` at key `0` when present.
- For `state.vscdb`, conversation identity falls back to the parent workspace-state directory name.
- Importable legacy message rows are JSON `blobs.data` values with `role` and non-empty visible text.
- Importable installed-state message rows are hex-decoded JSON `cursorDiskKV.value` values at `agentKv:blob:*` keys with `role` and non-empty visible text.
- Message order uses SQLite `rowid`, which reflects store insertion order in the observed table shape.
- Supported visible roles are `user`, `assistant`, and `system`.
- Content arrays keep only blocks with `type: "text"`.
- Non-message blobs, undecodable blobs, `tool` rows, tool-call blocks, tool-result blocks, and file-content state rows are skipped to avoid indexing internal command payloads or workspace files as chat text.

Cursor stores do not expose branch/path metadata through the observed message blobs, so imported Cursor messages have path membership marked unknown.
