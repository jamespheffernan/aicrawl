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

Conversation IDs are read from `id`, `uuid`, or `conversation_id`.

Detail endpoint:

```text
GET https://chatgpt.com/backend-api/conversation/<conversation-id>
```

Accepted detail shapes:

- top-level conversation object with `mapping`
- wrapper object with `conversation.mapping`
- wrapper object with `data.mapping`

The fetched detail batch is imported through the existing ChatGPT web parser, which expects the ChatGPT conversation graph shape and preserves raw payloads.

## Drift Behavior

If a list response cannot expose IDs, sync stops without importing. If a detail response lacks `mapping`, sync fails before writing archive rows. Redacted network captures can still be checked with `sync web --capture ... --dry-run`.
