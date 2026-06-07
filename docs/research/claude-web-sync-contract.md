# Claude Web Sync Contract

Observed date: 2026-06-07

The live Claude path uses an attached Chrome DevTools target for `https://claude.ai` and runs same-origin `fetch()` calls from that page context. Authentication stays in the browser profile.

## Current Endpoint Assumptions

Organization endpoint:

```text
GET https://claude.ai/api/organizations
```

Accepted organization shapes:

- top-level array
- top-level `organizations` array
- top-level `data` array

Organization IDs are read from `uuid`, `id`, or `conversation_id`.

List endpoint:

```text
GET https://claude.ai/api/organizations/<organization-id>/chat_conversations?limit=<n>&offset=<n>
```

Accepted list shapes:

- top-level `chat_conversations` array
- top-level `conversations` array
- top-level array
- top-level `data` array

Conversation IDs are read from `uuid`, `id`, or `conversation_id`. Cursor timestamps are read from `updated_at` or `created_at` when present.

Detail endpoint:

```text
GET https://claude.ai/api/organizations/<organization-id>/chat_conversations/<conversation-id>
```

Accepted detail shapes:

- top-level conversation object with `chat_messages`
- wrapper object with `conversation.chat_messages`
- wrapper object with `data.chat_messages`

The fetched detail batch is imported through the existing Claude web parser, which expects Claude conversation objects and preserves raw payloads. When list or detail payloads expose timestamps, live sync records a `provider_updated_at` cursor and later skips list candidates at or before that cursor. If nothing new is found, sync updates `last_checked_at` without fetching detail payloads or writing archive rows.

## Drift Behavior

If the organization response has no ID, sync fails before fetching conversations. If a list response cannot expose IDs, sync stops without importing. If a detail response lacks `chat_messages`, sync fails before writing archive rows. Redacted network captures can still be checked with `sync web --capture ... --dry-run`.
