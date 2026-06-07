package schema

import (
	"context"
	"database/sql"
	"fmt"
)

const Version = 2

var migrationV1 = []string{
	`create table if not exists providers (
		id text primary key,
		display_name text not null,
		created_at text not null default (strftime('%Y-%m-%dT%H:%M:%f','now') || '000000Z')
	)`,
	`create table if not exists accounts (
		id text primary key,
		provider text not null references providers(id),
		raw_id text not null,
		display_name text,
		raw_payload text,
		created_at text not null default (strftime('%Y-%m-%dT%H:%M:%f','now') || '000000Z'),
		updated_at text not null default (strftime('%Y-%m-%dT%H:%M:%f','now') || '000000Z'),
		unique(provider, raw_id)
	)`,
	`create table if not exists imports (
		id text primary key,
		source_kind text not null,
		provider text not null references providers(id),
		source_hash text not null,
		source_label text not null,
		started_at text not null,
		completed_at text,
		conversation_count integer not null default 0,
		message_count integer not null default 0,
		attachment_count integer not null default 0,
		warning_count integer not null default 0,
		first_seen_at text not null default (strftime('%Y-%m-%dT%H:%M:%f','now') || '000000Z'),
		last_seen_at text not null default (strftime('%Y-%m-%dT%H:%M:%f','now') || '000000Z'),
		unique(source_kind, provider, source_hash)
	)`,
	`create table if not exists import_warnings (
		import_id text not null references imports(id) on delete cascade,
		ordinal integer not null,
		warning text not null,
		primary key(import_id, ordinal)
	)`,
	`create table if not exists conversations (
		id text primary key,
		provider text not null references providers(id),
		account_id text references accounts(id),
		raw_id text not null,
		title text,
		created_at text,
		updated_at text,
		current_node_id text,
		raw_payload text not null,
		first_import_id text not null references imports(id),
		last_import_id text not null references imports(id),
		first_seen_at text not null default (strftime('%Y-%m-%dT%H:%M:%f','now') || '000000Z'),
		last_seen_at text not null default (strftime('%Y-%m-%dT%H:%M:%f','now') || '000000Z'),
		message_count integer not null default 0,
		unique(provider, raw_id)
	)`,
	`create index if not exists conversations_provider_updated_idx on conversations(provider, updated_at desc, created_at desc)`,
	`create table if not exists messages (
		id text primary key,
		provider text not null references providers(id),
		conversation_id text not null references conversations(id) on delete cascade,
		raw_id text not null,
		parent_id text,
		role text not null,
		sender text,
		created_at text,
		updated_at text,
		ordinal integer not null default 0,
		is_current_path integer not null default 0,
		is_path_known integer not null default 0,
		text text not null default '',
		raw_payload text not null,
		first_import_id text not null references imports(id),
		last_import_id text not null references imports(id),
		first_seen_at text not null default (strftime('%Y-%m-%dT%H:%M:%f','now') || '000000Z'),
		last_seen_at text not null default (strftime('%Y-%m-%dT%H:%M:%f','now') || '000000Z'),
		unique(provider, conversation_id, raw_id)
	)`,
	`create index if not exists messages_conversation_order_idx on messages(conversation_id, ordinal, created_at, id)`,
	`create index if not exists messages_provider_role_idx on messages(provider, role)`,
	`create table if not exists message_edges (
		provider text not null references providers(id),
		conversation_id text not null references conversations(id) on delete cascade,
		parent_message_id text not null,
		child_message_id text not null,
		raw_parent_id text not null,
		raw_child_id text not null,
		edge_kind text not null default 'parent_child',
		first_import_id text not null references imports(id),
		last_import_id text not null references imports(id),
		first_seen_at text not null default (strftime('%Y-%m-%dT%H:%M:%f','now') || '000000Z'),
		last_seen_at text not null default (strftime('%Y-%m-%dT%H:%M:%f','now') || '000000Z'),
		primary key(provider, conversation_id, raw_parent_id, raw_child_id, edge_kind)
	)`,
	`create table if not exists message_versions (
		id text primary key,
		message_id text not null references messages(id) on delete cascade,
		content_hash text not null,
		text text not null default '',
		raw_payload text not null,
		first_import_id text not null references imports(id),
		last_import_id text not null references imports(id),
		first_seen_at text not null default (strftime('%Y-%m-%dT%H:%M:%f','now') || '000000Z'),
		last_seen_at text not null default (strftime('%Y-%m-%dT%H:%M:%f','now') || '000000Z'),
		unique(message_id, content_hash)
	)`,
	`create table if not exists attachments (
		id text primary key,
		provider text not null references providers(id),
		conversation_id text not null references conversations(id) on delete cascade,
		message_id text references messages(id) on delete cascade,
		kind text not null,
		filename text,
		mime_type text,
		text text not null default '',
		raw_payload text not null,
		first_import_id text not null references imports(id),
		last_import_id text not null references imports(id),
		first_seen_at text not null default (strftime('%Y-%m-%dT%H:%M:%f','now') || '000000Z'),
		last_seen_at text not null default (strftime('%Y-%m-%dT%H:%M:%f','now') || '000000Z')
	)`,
	`create index if not exists attachments_message_idx on attachments(message_id)`,
	`create virtual table if not exists messages_fts using fts5(
		message_id unindexed,
		conversation_id unindexed,
		provider unindexed,
		role unindexed,
		body,
		tokenize='unicode61'
	)`,
	`create table if not exists sync_state (
		source_kind text primary key,
		last_import_id text references imports(id),
		last_import_at text,
		conversation_count integer not null default 0,
		message_count integer not null default 0,
		updated_at text not null default (strftime('%Y-%m-%dT%H:%M:%f','now') || '000000Z')
	)`,
}

var migrationV2 = []string{
	`alter table sync_state add column last_checked_at text`,
	`alter table sync_state add column cursor_kind text not null default ''`,
	`alter table sync_state add column cursor_value text not null default ''`,
	`alter table sync_state add column cursor_at text`,
	`alter table sync_state add column last_candidate_count integer not null default 0`,
	`update sync_state set last_checked_at = coalesce(last_checked_at, last_import_at, updated_at)`,
}

func Migrate(ctx context.Context, db *sql.DB) error {
	current, err := UserVersion(ctx, db)
	if err != nil {
		return err
	}
	if current > Version {
		return fmt.Errorf("database schema version %d is newer than supported version %d", current, Version)
	}
	if current == Version {
		return nil
	}
	for next := current + 1; next <= Version; next++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration v%d: %w", next, err)
		}
		for _, stmt := range migrationStatements(next) {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("apply schema v%d: %w", next, err)
			}
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("pragma user_version = %d", next)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("set user_version %d: %w", next, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration v%d: %w", next, err)
		}
	}
	return nil
}

func migrationStatements(version int) []string {
	switch version {
	case 1:
		return migrationV1
	case 2:
		return migrationV2
	default:
		return nil
	}
}

func UserVersion(ctx context.Context, db *sql.DB) (int, error) {
	var version int
	if err := db.QueryRowContext(ctx, "pragma user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("read user_version: %w", err)
	}
	return version, nil
}
