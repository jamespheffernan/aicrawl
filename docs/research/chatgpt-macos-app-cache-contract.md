# ChatGPT macOS App Cache Contract

Observed date: 2026-06-07

The native ChatGPT macOS app stores local cache files under:

```text
~/Library/Application Support/com.openai.chat/
```

Observed conversation cache directories are named:

```text
conversations-v3-<account-or-install-id>/
project-*/conversations-v3-<account-or-install-id>/
```

Conversation cache files are named `<conversation-id>.data`. Their bodies are opaque high-entropy app cache data and are not treated as a durable public contract. `aicrawl` does not read or decode the `.data` bodies.

## Current Use

`sync web --provider chatgpt --chatgpt-app-cache <dir>` scans for `conversations-v3-*/*.data`, extracts valid conversation IDs from filenames, sorts unique IDs by newest file modification time, and bounds the list with `--max-conversations`.

Those IDs seed the normal authenticated ChatGPT web detail fetch:

```text
GET https://chatgpt.com/backend-api/conversation/<conversation-id>
```

Authentication remains in the attached or launched browser profile. The native app cache path is used only for ID discovery; transcript payloads still come from same-origin ChatGPT web calls and are imported under `chatgpt_web`.

## Drift Behavior

If the cache directory has no valid `conversations-v3-*/*.data` files, write-capable sync fails before falling back to list sync. Dry-run reports `source.kind = chatgpt_app_cache_ids` and a zero candidate count. Invalid filenames are counted in bounded warnings without printing file contents.
