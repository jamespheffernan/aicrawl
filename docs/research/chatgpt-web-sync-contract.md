# ChatGPT Web Sync Contract

Observed date: 2026-06-07

The live ChatGPT path uses an attached Chrome DevTools target for `https://chatgpt.com` and runs same-origin `fetch()` calls from that page context. Authentication stays in the browser profile.

## Current Endpoint Assumptions

List endpoint:

```text
GET https://chatgpt.com/backend-api/conversations?offset=<n>&limit=<n>&order=updated
```

Accepted list shapes:

- top-level `items` array
- top-level `conversations` array
- top-level array

Conversation IDs are read from `id`, `uuid`, or `conversation_id`. Cursor timestamps are read from `update_time`, `updated_at`, `updateTime`, `last_message_at`, `create_time`, or `created_at` when present.

Detail endpoint:

```text
GET https://chatgpt.com/backend-api/conversation/<conversation-id>
```

Accepted detail shapes:

- top-level conversation object with `mapping`
- wrapper object with `conversation.mapping`
- wrapper object with `data.mapping`

The fetched detail batch is imported through the existing ChatGPT web parser, which expects the ChatGPT conversation graph shape and preserves raw payloads. When list or detail payloads expose timestamps, live sync records a `provider_updated_at` cursor and later skips list candidates at or before that cursor. If nothing new is found, sync updates `last_checked_at` without fetching detail payloads or writing archive rows.

Conversation detail responses with 403, 404, or 410 are skipped and recorded in `conversation_sync_status` as `inaccessible` with the provider HTTP status. Successful imports record the conversation status as `seen`. Neither status path deletes existing archive rows.

## Drift Behavior

If a list response cannot expose IDs, sync stops without importing. If a detail response lacks `mapping`, sync fails before writing archive rows unless the response is one of the non-destructive inaccessible statuses above. Repeated list pages stop pagination once no new candidate IDs are observed. Redacted network captures can still be checked with `sync web --capture ... --dry-run`.
