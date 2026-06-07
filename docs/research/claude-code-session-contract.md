# Claude Code Session Contract

Observed date: 2026-06-07

Claude Code stores project sessions as JSONL files under a project-scoped local directory. The importer targets one JSONL session file at a time, and directory import recursively discovers JSONL transcript files.

Observed local roots:

- `~/.claude/projects` for Claude Code CLI sessions.
- `~/Library/Application Support/Claude` for Claude desktop/local-agent sessions that embed Claude Code-style transcript trees.

Claude desktop also stores `local_*.json` session metadata. Those files can include useful titles, model names, working directories, and initial prompts, but the current importer intentionally uses the companion JSONL transcript files rather than indexing metadata-only sessions as shallow conversations.

## Importable Records

Visible transcript records have:

- `type`: `user` or `assistant`
- `sessionId`: stable session ID
- `uuid`: stable message/event ID
- `parentUuid`: optional parent event ID
- `timestamp`: RFC3339 timestamp
- `message.role`: provider role
- `message.content`: either a string or an array of typed content blocks

The `claude-code` importer keeps only visible user/assistant text:

- string `message.content`
- content blocks with `type: "text"`

## Skipped Records

The local store also contains operational records that should not be indexed as chat transcript text:

- queue/control events such as `queue-operation`
- attachment metadata records
- last-prompt pointers
- hook/system records without message text
- assistant `thinking` blocks
- assistant `tool_use` blocks
- user `tool_result` blocks

This keeps search focused on the conversational transcript and avoids pulling command output or internal tool payloads into the visible message index.
