package archive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/openclaw/aicrawl/internal/textnorm"
	"github.com/openclaw/aicrawl/internal/timefmt"
)

const privacyReminder = "Source files contain private conversation data. aicrawl does not delete them after import."

type ConversationEmitter func(conversation Conversation, warnings []string) error

func (a *Archive) ImportParsed(ctx context.Context, sourcePath string, parsed ParsedSource) (ImportStats, error) {
	if a == nil || a.store == nil {
		return ImportStats{}, fmt.Errorf("archive is not open")
	}
	if parsed.Provider == "" || parsed.SourceKind == "" {
		return ImportStats{}, fmt.Errorf("parsed source is missing provider or source kind")
	}
	sourceHash, err := fileSHA256(sourcePath)
	if err != nil {
		return ImportStats{}, err
	}
	importID := deterministicID("import", parsed.SourceKind, parsed.Provider, sourceHash)
	now := timefmt.FormatUTC(time.Now())
	stats := ImportStats{
		ImportID:        importID,
		Provider:        parsed.Provider,
		SourceKind:      parsed.SourceKind,
		SourceHash:      sourceHash,
		Conversations:   len(parsed.Conversations),
		Warnings:        append([]string(nil), parsed.Warnings...),
		CompletedAt:     now,
		PrivacyReminder: privacyReminder,
	}
	for _, conversation := range parsed.Conversations {
		stats.Messages += len(conversation.Messages)
		stats.Attachments += len(conversation.Attachments)
	}
	err = a.store.WithTx(ctx, func(tx *sql.Tx) error {
		existing, ok, err := completedImportStats(ctx, tx, importID)
		if err != nil {
			return err
		}
		if ok {
			stats = existing
			if err := markImportSeen(ctx, tx, importID, now); err != nil {
				return err
			}
			return upsertSyncState(ctx, tx, parsed.SourceKind, importID, now, stats.Conversations, stats.Messages, sourceHashCursor(sourceHash))
		}
		if err := upsertProvider(ctx, tx, parsed.Provider); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `insert into imports (
			id, source_kind, provider, source_hash, source_label, started_at, completed_at,
			conversation_count, message_count, attachment_count, warning_count, last_seen_at
		) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(id) do update set
			completed_at = excluded.completed_at,
			conversation_count = excluded.conversation_count,
			message_count = excluded.message_count,
			attachment_count = excluded.attachment_count,
			warning_count = excluded.warning_count,
			last_seen_at = excluded.last_seen_at`,
			importID, parsed.SourceKind, parsed.Provider, sourceHash, sourceLabel(sourceHash), now, now,
			stats.Conversations, stats.Messages, stats.Attachments, len(stats.Warnings), now); err != nil {
			return fmt.Errorf("record import: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `delete from import_warnings where import_id = ?`, importID); err != nil {
			return err
		}
		for i, warning := range stats.Warnings {
			if _, err := tx.ExecContext(ctx, `insert into import_warnings(import_id, ordinal, warning) values(?, ?, ?)`, importID, i, warning); err != nil {
				return err
			}
		}
		for _, conversation := range parsed.Conversations {
			if err := upsertConversation(ctx, tx, parsed.SourceKind, importID, now, conversation); err != nil {
				return err
			}
		}
		if err := upsertSyncState(ctx, tx, parsed.SourceKind, importID, now, stats.Conversations, stats.Messages, sourceHashCursor(sourceHash)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return ImportStats{}, err
	}
	return stats, nil
}

func (a *Archive) ImportStream(ctx context.Context, sourcePath, provider, sourceKind string, parse func(ConversationEmitter) error) (ImportStats, error) {
	if a == nil || a.store == nil {
		return ImportStats{}, fmt.Errorf("archive is not open")
	}
	if provider == "" || sourceKind == "" {
		return ImportStats{}, fmt.Errorf("stream source is missing provider or source kind")
	}
	if parse == nil {
		return ImportStats{}, fmt.Errorf("stream parser is required")
	}
	sourceHash, err := fileSHA256(sourcePath)
	if err != nil {
		return ImportStats{}, err
	}
	importID := deterministicID("import", sourceKind, provider, sourceHash)
	startedAt := timefmt.FormatUTC(time.Now())
	stats := ImportStats{
		ImportID:        importID,
		Provider:        provider,
		SourceKind:      sourceKind,
		SourceHash:      sourceHash,
		PrivacyReminder: privacyReminder,
	}
	err = a.store.WithTx(ctx, func(tx *sql.Tx) error {
		existing, ok, err := completedImportStats(ctx, tx, importID)
		if err != nil {
			return err
		}
		if ok {
			stats = existing
			if err := markImportSeen(ctx, tx, importID, startedAt); err != nil {
				return err
			}
			return upsertSyncState(ctx, tx, sourceKind, importID, startedAt, stats.Conversations, stats.Messages, sourceHashCursor(sourceHash))
		}
		if err := upsertProvider(ctx, tx, provider); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `insert into imports (
			id, source_kind, provider, source_hash, source_label, started_at, completed_at,
			conversation_count, message_count, attachment_count, warning_count, last_seen_at
		) values (?, ?, ?, ?, ?, ?, null, 0, 0, 0, 0, ?)
		on conflict(id) do update set
			started_at = excluded.started_at,
			completed_at = null,
			last_seen_at = excluded.last_seen_at`,
			importID, sourceKind, provider, sourceHash, sourceLabel(sourceHash), startedAt, startedAt); err != nil {
			return fmt.Errorf("record import start: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `delete from import_warnings where import_id = ?`, importID); err != nil {
			return err
		}
		emit := func(conversation Conversation, warnings []string) error {
			stats.Conversations++
			stats.Messages += len(conversation.Messages)
			stats.Attachments += len(conversation.Attachments)
			stats.Warnings = append(stats.Warnings, warnings...)
			if err := upsertConversation(ctx, tx, sourceKind, importID, startedAt, conversation); err != nil {
				return err
			}
			return nil
		}
		if err := parse(emit); err != nil {
			return err
		}
		completedAt := timefmt.FormatUTC(time.Now())
		stats.CompletedAt = completedAt
		for i, warning := range stats.Warnings {
			if _, err := tx.ExecContext(ctx, `insert into import_warnings(import_id, ordinal, warning) values(?, ?, ?)`, importID, i, warning); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `update imports set
			completed_at = ?,
			conversation_count = ?,
			message_count = ?,
			attachment_count = ?,
			warning_count = ?,
			last_seen_at = ?
			where id = ?`,
			completedAt, stats.Conversations, stats.Messages, stats.Attachments, len(stats.Warnings), completedAt, importID); err != nil {
			return fmt.Errorf("record import completion: %w", err)
		}
		if err := upsertSyncState(ctx, tx, sourceKind, importID, completedAt, stats.Conversations, stats.Messages, sourceHashCursor(sourceHash)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return ImportStats{}, err
	}
	return stats, nil
}

func (a *Archive) UpdateSyncCursor(ctx context.Context, sourceKind string, cursor SyncCursor) error {
	if a == nil || a.store == nil {
		return fmt.Errorf("archive is not open")
	}
	if sourceKind == "" {
		return fmt.Errorf("source kind is required")
	}
	checkedAt := timefmt.FormatUTC(time.Now())
	_, err := a.DB().ExecContext(ctx, `insert into sync_state (
		source_kind, last_checked_at, cursor_kind, cursor_value, cursor_at, last_candidate_count, updated_at
	) values (?, ?, ?, ?, nullif(?, ''), ?, ?)
	on conflict(source_kind) do update set
		last_checked_at = excluded.last_checked_at,
		cursor_kind = excluded.cursor_kind,
		cursor_value = excluded.cursor_value,
		cursor_at = excluded.cursor_at,
		last_candidate_count = excluded.last_candidate_count,
		updated_at = excluded.updated_at`,
		sourceKind, checkedAt, cursor.Kind, cursor.Value, cursor.At, cursor.CandidateCount, checkedAt)
	if err != nil {
		return fmt.Errorf("update sync cursor: %w", err)
	}
	return nil
}

func (a *Archive) RecordConversationSyncStatuses(ctx context.Context, statuses []ConversationSyncStatus) error {
	if a == nil || a.store == nil {
		return fmt.Errorf("archive is not open")
	}
	if len(statuses) == 0 {
		return nil
	}
	checkedAt := timefmt.FormatUTC(time.Now())
	return a.store.WithTx(ctx, func(tx *sql.Tx) error {
		for _, status := range statuses {
			if status.SourceKind == "" || status.Provider == "" || status.RawID == "" || status.Status == "" {
				return fmt.Errorf("conversation sync status is missing required identity")
			}
			if err := upsertProvider(ctx, tx, status.Provider); err != nil {
				return err
			}
			conversationID := status.ConversationID
			if conversationID == "" {
				conversationID = status.Provider + ":" + status.RawID
			}
			if _, err := tx.ExecContext(ctx, `insert into conversation_sync_status (
				source_kind, provider, raw_id, conversation_id, status, http_status, last_import_id, last_seen_at, last_checked_at
			) values (?, ?, ?, ?, ?, nullif(?, 0), null, ?, ?)
			on conflict(source_kind, provider, raw_id) do update set
				conversation_id = excluded.conversation_id,
				status = excluded.status,
				http_status = excluded.http_status,
				last_seen_at = excluded.last_seen_at,
				last_checked_at = excluded.last_checked_at`,
				status.SourceKind, status.Provider, status.RawID, conversationID, status.Status, status.HTTPStatus, checkedAt, checkedAt); err != nil {
				return fmt.Errorf("record conversation sync status: %w", err)
			}
		}
		return nil
	})
}

func upsertSyncState(ctx context.Context, tx *sql.Tx, sourceKind, importID, seenAt string, conversations, messages int, cursor SyncCursor) error {
	_, err := tx.ExecContext(ctx, `insert into sync_state (
		source_kind, last_import_id, last_import_at, last_checked_at, conversation_count, message_count,
		cursor_kind, cursor_value, cursor_at, last_candidate_count, updated_at
	) values (?, ?, ?, ?, ?, ?, ?, ?, nullif(?, ''), ?, ?)
	on conflict(source_kind) do update set
		last_import_id = excluded.last_import_id,
		last_import_at = excluded.last_import_at,
		last_checked_at = excluded.last_checked_at,
		conversation_count = excluded.conversation_count,
		message_count = excluded.message_count,
		cursor_kind = excluded.cursor_kind,
		cursor_value = excluded.cursor_value,
		cursor_at = excluded.cursor_at,
		last_candidate_count = excluded.last_candidate_count,
		updated_at = excluded.updated_at`,
		sourceKind, importID, seenAt, seenAt, conversations, messages,
		cursor.Kind, cursor.Value, cursor.At, cursor.CandidateCount, seenAt)
	if err != nil {
		return fmt.Errorf("upsert sync state: %w", err)
	}
	return nil
}

func sourceHashCursor(sourceHash string) SyncCursor {
	return SyncCursor{Kind: "source_hash", Value: sourceHash}
}

func completedImportStats(ctx context.Context, tx *sql.Tx, importID string) (ImportStats, bool, error) {
	stats := ImportStats{ImportID: importID, AlreadyImported: true, PrivacyReminder: privacyReminder}
	err := tx.QueryRowContext(ctx, `select provider, source_kind, source_hash,
		conversation_count, message_count, attachment_count, completed_at
		from imports
		where id = ? and completed_at is not null`, importID).Scan(
		&stats.Provider,
		&stats.SourceKind,
		&stats.SourceHash,
		&stats.Conversations,
		&stats.Messages,
		&stats.Attachments,
		&stats.CompletedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ImportStats{}, false, nil
	}
	if err != nil {
		return ImportStats{}, false, err
	}
	warnings, err := importWarnings(ctx, tx, importID)
	if err != nil {
		return ImportStats{}, false, err
	}
	stats.Warnings = warnings
	return stats, true, nil
}

func importWarnings(ctx context.Context, tx *sql.Tx, importID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `select warning from import_warnings where import_id = ? order by ordinal`, importID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var warnings []string
	for rows.Next() {
		var warning string
		if err := rows.Scan(&warning); err != nil {
			return nil, err
		}
		warnings = append(warnings, warning)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return warnings, nil
}

func markImportSeen(ctx context.Context, tx *sql.Tx, importID, seenAt string) error {
	_, err := tx.ExecContext(ctx, `update imports set last_seen_at = ? where id = ?`, seenAt, importID)
	if err != nil {
		return fmt.Errorf("record import seen: %w", err)
	}
	return nil
}

func upsertProvider(ctx context.Context, tx *sql.Tx, provider string) error {
	display := provider
	switch provider {
	case "claude":
		display = "Claude"
	case "chatgpt":
		display = "ChatGPT"
	}
	_, err := tx.ExecContext(ctx, `insert into providers(id, display_name) values(?, ?)
		on conflict(id) do update set display_name = excluded.display_name`, provider, display)
	return err
}

func upsertConversation(ctx context.Context, tx *sql.Tx, sourceKind, importID, now string, conversation Conversation) error {
	if conversation.ID == "" || conversation.Provider == "" || conversation.RawID == "" {
		return fmt.Errorf("conversation is missing a stable ID")
	}
	raw := string(conversation.RawPayload)
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	if _, err := tx.ExecContext(ctx, `insert into conversations (
		id, provider, account_id, raw_id, title, created_at, updated_at, current_node_id,
		raw_payload, first_import_id, last_import_id, last_seen_at, message_count
	) values (?, ?, nullif(?, ''), ?, nullif(?, ''), nullif(?, ''), nullif(?, ''), nullif(?, ''), ?, ?, ?, ?, ?)
	on conflict(id) do update set
		account_id = excluded.account_id,
		title = excluded.title,
		created_at = coalesce(excluded.created_at, conversations.created_at),
		updated_at = coalesce(excluded.updated_at, conversations.updated_at),
		current_node_id = excluded.current_node_id,
		raw_payload = excluded.raw_payload,
		last_import_id = excluded.last_import_id,
		last_seen_at = excluded.last_seen_at,
		message_count = excluded.message_count`,
		conversation.ID, conversation.Provider, conversation.AccountID, conversation.RawID, conversation.Title,
		conversation.CreatedAt, conversation.UpdatedAt, conversation.CurrentNodeID, raw, importID, importID, now, len(conversation.Messages)); err != nil {
		return fmt.Errorf("upsert conversation %s: %w", conversation.ID, err)
	}
	if err := upsertConversationSyncStatus(ctx, tx, sourceKind, conversation.Provider, conversation.RawID, conversation.ID, "seen", 0, importID, now); err != nil {
		return err
	}
	for _, message := range conversation.Messages {
		if err := upsertMessage(ctx, tx, importID, now, conversation.ID, message); err != nil {
			return err
		}
	}
	if err := refreshConversationMessageCount(ctx, tx, conversation.ID); err != nil {
		return err
	}
	for _, edge := range conversation.Edges {
		if err := upsertEdge(ctx, tx, importID, now, conversation.ID, edge); err != nil {
			return err
		}
	}
	for _, attachment := range conversation.Attachments {
		if err := upsertAttachment(ctx, tx, importID, now, conversation.ID, attachment); err != nil {
			return err
		}
	}
	if err := rebuildConversationFTS(ctx, tx, conversation.ID); err != nil {
		return err
	}
	return nil
}

func upsertConversationSyncStatus(ctx context.Context, tx *sql.Tx, sourceKind, provider, rawID, conversationID, status string, httpStatus int, importID, now string) error {
	if sourceKind == "" || provider == "" || rawID == "" || conversationID == "" || status == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `insert into conversation_sync_status (
		source_kind, provider, raw_id, conversation_id, status, http_status, last_import_id, last_seen_at, last_checked_at
	) values (?, ?, ?, ?, ?, nullif(?, 0), nullif(?, ''), ?, ?)
	on conflict(source_kind, provider, raw_id) do update set
		conversation_id = excluded.conversation_id,
		status = excluded.status,
		http_status = excluded.http_status,
		last_import_id = excluded.last_import_id,
		last_seen_at = excluded.last_seen_at,
		last_checked_at = excluded.last_checked_at`,
		sourceKind, provider, rawID, conversationID, status, httpStatus, importID, now, now)
	if err != nil {
		return fmt.Errorf("upsert conversation sync status %s/%s: %w", provider, rawID, err)
	}
	return nil
}

func refreshConversationMessageCount(ctx context.Context, tx *sql.Tx, conversationID string) error {
	_, err := tx.ExecContext(ctx, `update conversations
		set message_count = (select count(*) from messages where conversation_id = ?)
		where id = ?`, conversationID, conversationID)
	if err != nil {
		return fmt.Errorf("refresh message count for conversation %s: %w", conversationID, err)
	}
	return nil
}

func upsertMessage(ctx context.Context, tx *sql.Tx, importID, now, conversationID string, message Message) error {
	if message.ID == "" || message.Provider == "" || message.RawID == "" {
		return fmt.Errorf("message is missing a stable ID")
	}
	text := textnorm.Normalize(message.Text)
	raw := string(message.RawPayload)
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	if message.Role == "" {
		message.Role = "unknown"
	}
	if _, err := tx.ExecContext(ctx, `insert into messages (
		id, provider, conversation_id, raw_id, parent_id, role, sender, created_at, updated_at,
		ordinal, is_current_path, is_path_known, text, raw_payload, first_import_id, last_import_id, last_seen_at
	) values (?, ?, ?, ?, nullif(?, ''), ?, nullif(?, ''), nullif(?, ''), nullif(?, ''), ?, ?, ?, ?, ?, ?, ?, ?)
	on conflict(id) do update set
		parent_id = excluded.parent_id,
		role = excluded.role,
		sender = excluded.sender,
		created_at = coalesce(excluded.created_at, messages.created_at),
		updated_at = coalesce(excluded.updated_at, messages.updated_at),
		ordinal = excluded.ordinal,
		is_current_path = excluded.is_current_path,
		is_path_known = excluded.is_path_known,
		text = excluded.text,
		raw_payload = excluded.raw_payload,
		last_import_id = excluded.last_import_id,
		last_seen_at = excluded.last_seen_at`,
		message.ID, message.Provider, conversationID, message.RawID, message.ParentID, message.Role, message.Sender,
		message.CreatedAt, message.UpdatedAt, message.Ordinal, boolInt(message.IsCurrentPath), boolInt(message.IsPathKnown),
		text, raw, importID, importID, now); err != nil {
		return fmt.Errorf("upsert message %s: %w", message.ID, err)
	}
	contentHash := sha256.Sum256([]byte(text + "\x00" + raw))
	versionID := deterministicID("message-version", message.ID, hex.EncodeToString(contentHash[:]))
	if _, err := tx.ExecContext(ctx, `insert into message_versions (
		id, message_id, content_hash, text, raw_payload, first_import_id, last_import_id, last_seen_at
	) values (?, ?, ?, ?, ?, ?, ?, ?)
	on conflict(message_id, content_hash) do update set
		last_import_id = excluded.last_import_id,
		last_seen_at = excluded.last_seen_at`,
		versionID, message.ID, hex.EncodeToString(contentHash[:]), text, raw, importID, importID, now); err != nil {
		return fmt.Errorf("upsert message version %s: %w", message.ID, err)
	}
	return nil
}

func rebuildConversationFTS(ctx context.Context, tx *sql.Tx, conversationID string) error {
	if _, err := tx.ExecContext(ctx, `delete from messages_fts where conversation_id = ?`, conversationID); err != nil {
		return fmt.Errorf("clear FTS for conversation %s: %w", conversationID, err)
	}
	if _, err := tx.ExecContext(ctx, `insert into messages_fts(message_id, conversation_id, provider, role, body)
		select id, conversation_id, provider, role, text
		from messages
		where conversation_id = ? and text <> ''`, conversationID); err != nil {
		return fmt.Errorf("index message text for conversation %s: %w", conversationID, err)
	}
	if _, err := tx.ExecContext(ctx, `insert into messages_fts(message_id, conversation_id, provider, role, body)
		select message_id, conversation_id, provider, 'attachment', text
		from attachments
		where conversation_id = ? and message_id is not null and text <> ''`, conversationID); err != nil {
		return fmt.Errorf("index attachment text for conversation %s: %w", conversationID, err)
	}
	return nil
}

func upsertEdge(ctx context.Context, tx *sql.Tx, importID, now, conversationID string, edge MessageEdge) error {
	if edge.Kind == "" {
		edge.Kind = "parent_child"
	}
	_, err := tx.ExecContext(ctx, `insert into message_edges (
		provider, conversation_id, parent_message_id, child_message_id, raw_parent_id, raw_child_id,
		edge_kind, first_import_id, last_import_id, last_seen_at
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	on conflict(provider, conversation_id, raw_parent_id, raw_child_id, edge_kind) do update set
		parent_message_id = excluded.parent_message_id,
		child_message_id = excluded.child_message_id,
		last_import_id = excluded.last_import_id,
		last_seen_at = excluded.last_seen_at`,
		edge.Provider, conversationID, edge.ParentMessageID, edge.ChildMessageID, edge.RawParentID, edge.RawChildID,
		edge.Kind, importID, importID, now)
	if err != nil {
		return fmt.Errorf("upsert message edge %s -> %s: %w", edge.RawParentID, edge.RawChildID, err)
	}
	return nil
}

func upsertAttachment(ctx context.Context, tx *sql.Tx, importID, now, conversationID string, attachment Attachment) error {
	raw := string(attachment.RawPayload)
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	text := textnorm.Normalize(attachment.Text)
	_, err := tx.ExecContext(ctx, `insert into attachments (
		id, provider, conversation_id, message_id, kind, filename, mime_type, text, raw_payload,
		first_import_id, last_import_id, last_seen_at
	) values (?, ?, ?, nullif(?, ''), ?, nullif(?, ''), nullif(?, ''), ?, ?, ?, ?, ?)
	on conflict(id) do update set
		message_id = excluded.message_id,
		kind = excluded.kind,
		filename = excluded.filename,
		mime_type = excluded.mime_type,
		text = excluded.text,
		raw_payload = excluded.raw_payload,
		last_import_id = excluded.last_import_id,
		last_seen_at = excluded.last_seen_at`,
		attachment.ID, attachment.Provider, conversationID, attachment.MessageID, attachment.Kind,
		attachment.Filename, attachment.MimeType, text, raw, importID, importID, now)
	if err != nil {
		return fmt.Errorf("upsert attachment %s: %w", attachment.ID, err)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open source for hashing: %w", err)
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("hash source file: %w", err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func deterministicID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return parts[0] + ":" + hex.EncodeToString(sum[:])
}

func sourceLabel(hash string) string {
	if len(hash) > 12 {
		return "sha256:" + hash[:12]
	}
	return "sha256:" + hash
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
