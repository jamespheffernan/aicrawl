package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/openclaw/aicrawl/internal/textnorm"
	"github.com/openclaw/crawlkit/store"
)

// conversationIterationBatchSize bounds each SQLite result set while iterating
// every conversation. It is not an archive capacity limit.
const conversationIterationBatchSize = 512

func (a *Archive) Counts(ctx context.Context) (Counts, error) {
	var counts Counts
	queries := []struct {
		query string
		dest  *int64
	}{
		{`select count(*) from providers`, &counts.Providers},
		{`select count(*) from imports`, &counts.Imports},
		{`select count(*) from conversations`, &counts.Conversations},
		{`select count(*) from messages`, &counts.Messages},
		{`select count(*) from message_edges`, &counts.Edges},
		{`select count(*) from attachments`, &counts.Attachments},
	}
	for _, q := range queries {
		if err := a.DB().QueryRowContext(ctx, q.query).Scan(q.dest); err != nil {
			return Counts{}, err
		}
	}
	return counts, nil
}

func (a *Archive) LastImportAt(ctx context.Context) (string, error) {
	var value sql.NullString
	err := a.DB().QueryRowContext(ctx, `select max(completed_at) from imports`).Scan(&value)
	if err != nil {
		return "", err
	}
	return value.String, nil
}

func (a *Archive) SyncState(ctx context.Context, sourceKind string) (SyncState, bool, error) {
	version, err := a.SchemaVersion(ctx)
	if err != nil {
		return SyncState{}, false, err
	}
	if version < 2 {
		return a.legacySyncState(ctx, sourceKind)
	}
	var state SyncState
	var lastImportID sql.NullString
	var lastImportAt sql.NullString
	var lastCheckedAt sql.NullString
	var cursorKind sql.NullString
	var cursorValue sql.NullString
	var cursorAt sql.NullString
	err = a.DB().QueryRowContext(ctx, `select source_kind, last_import_id, last_import_at,
		last_checked_at, cursor_kind, cursor_value, cursor_at, last_candidate_count,
		conversation_count, message_count, updated_at
		from sync_state
		where source_kind = ?`, sourceKind).Scan(
		&state.SourceKind,
		&lastImportID,
		&lastImportAt,
		&lastCheckedAt,
		&cursorKind,
		&cursorValue,
		&cursorAt,
		&state.CandidateCount,
		&state.ConversationCount,
		&state.MessageCount,
		&state.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SyncState{}, false, nil
	}
	if err != nil {
		return SyncState{}, false, err
	}
	state.LastImportID = lastImportID.String
	state.LastImportAt = lastImportAt.String
	state.LastCheckedAt = lastCheckedAt.String
	state.CursorKind = cursorKind.String
	state.CursorValue = cursorValue.String
	state.CursorAt = cursorAt.String
	return state, true, nil
}

func (a *Archive) legacySyncState(ctx context.Context, sourceKind string) (SyncState, bool, error) {
	var state SyncState
	var lastImportID sql.NullString
	var lastImportAt sql.NullString
	err := a.DB().QueryRowContext(ctx, `select source_kind, last_import_id, last_import_at,
		conversation_count, message_count, updated_at
		from sync_state
		where source_kind = ?`, sourceKind).Scan(
		&state.SourceKind,
		&lastImportID,
		&lastImportAt,
		&state.ConversationCount,
		&state.MessageCount,
		&state.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SyncState{}, false, nil
	}
	if err != nil {
		return SyncState{}, false, err
	}
	state.LastImportID = lastImportID.String
	state.LastImportAt = lastImportAt.String
	state.LastCheckedAt = lastImportAt.String
	return state, true, nil
}

func (a *Archive) FTSReady(ctx context.Context) error {
	var name string
	return a.DB().QueryRowContext(ctx, `select name from sqlite_master where type = 'table' and name = 'messages_fts'`).Scan(&name)
}

func (a *Archive) Conversations(ctx context.Context, provider string, limit int) ([]ConversationRow, error) {
	if limit <= 0 {
		limit = 50
	}
	return a.ConversationsFiltered(ctx, ConversationFilter{Provider: provider}, limit)
}

func (a *Archive) ConversationsFiltered(ctx context.Context, filter ConversationFilter, limit int) ([]ConversationRow, error) {
	if limit <= 0 {
		limit = 50
	}
	args := []any{}
	whereSQL, err := conversationWhere(filter, "", &args)
	if err != nil {
		return nil, err
	}
	args = append(args, limit)
	rows, err := a.DB().QueryContext(ctx, `select id, provider, raw_id, coalesce(title, ''), coalesce(created_at, ''),
		coalesce(updated_at, ''), message_count
		from conversations `+whereSQL+`
		order by coalesce(updated_at, created_at, last_seen_at) desc, id
		limit ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConversationRow
	for rows.Next() {
		var row ConversationRow
		if err := rows.Scan(&row.ID, &row.Provider, &row.RawID, &row.Title, &row.CreatedAt, &row.UpdatedAt, &row.MessageCount); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (a *Archive) EachConversation(ctx context.Context, provider string, fn func(ConversationRow) error) error {
	return a.EachConversationFiltered(ctx, ConversationFilter{Provider: provider}, fn)
}

func (a *Archive) EachConversationFiltered(ctx context.Context, filter ConversationFilter, fn func(ConversationRow) error) error {
	if fn == nil {
		return fmt.Errorf("conversation callback is required")
	}
	lastID := ""
	for {
		args := []any{}
		whereSQL, err := conversationWhere(filter, lastID, &args)
		if err != nil {
			return err
		}
		args = append(args, conversationIterationBatchSize)
		rows, err := a.DB().QueryContext(ctx, `select id, provider, raw_id, coalesce(title, ''), coalesce(created_at, ''),
			coalesce(updated_at, ''), message_count
			from conversations `+whereSQL+`
			order by id
			limit ?`, args...)
		if err != nil {
			return err
		}
		var batch []ConversationRow
		for rows.Next() {
			var row ConversationRow
			if err := rows.Scan(&row.ID, &row.Provider, &row.RawID, &row.Title, &row.CreatedAt, &row.UpdatedAt, &row.MessageCount); err != nil {
				_ = rows.Close()
				return err
			}
			batch = append(batch, row)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		for _, row := range batch {
			if err := fn(row); err != nil {
				return err
			}
		}
		if len(batch) < conversationIterationBatchSize {
			return nil
		}
		lastID = batch[len(batch)-1].ID
	}
}

func (a *Archive) Messages(ctx context.Context, conversationID, pathMode string) ([]MessageRow, error) {
	return a.MessagesWithOptions(ctx, MessageOptions{ConversationID: conversationID, PathMode: pathMode})
}

func (a *Archive) MessagesWithOptions(ctx context.Context, opts MessageOptions) ([]MessageRow, error) {
	rows, err := a.conversationMessages(ctx, opts.ConversationID, opts.PathMode)
	if err != nil {
		return nil, err
	}
	if opts.AroundID == "" {
		return rows, nil
	}
	if opts.Before < 0 || opts.After < 0 {
		return nil, fmt.Errorf("message context bounds must be non-negative")
	}
	aroundIndex := -1
	for i, row := range rows {
		if row.ID == opts.AroundID || row.RawID == opts.AroundID {
			aroundIndex = i
			break
		}
	}
	if aroundIndex < 0 {
		return nil, fmt.Errorf("message %q was not found in conversation %q with path %q", opts.AroundID, opts.ConversationID, normalizedPathMode(opts.PathMode))
	}
	start := aroundIndex - opts.Before
	if start < 0 {
		start = 0
	}
	end := aroundIndex + opts.After + 1
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end], nil
}

func (a *Archive) conversationMessages(ctx context.Context, conversationID, pathMode string) ([]MessageRow, error) {
	if strings.TrimSpace(conversationID) == "" {
		return nil, fmt.Errorf("conversation id is required")
	}
	pathMode = normalizedPathMode(pathMode)
	where := "where conversation_id = ?"
	args := []any{conversationID}
	if pathMode == "current" {
		var currentCount int
		if err := a.DB().QueryRowContext(ctx, `select count(*) from messages where conversation_id = ? and is_current_path = 1`, conversationID).Scan(&currentCount); err != nil {
			return nil, err
		}
		if currentCount > 0 {
			where += " and is_current_path = 1"
		}
	}
	rows, err := a.DB().QueryContext(ctx, `select id, provider, conversation_id, raw_id, coalesce(parent_id, ''),
		role, coalesce(created_at, ''), coalesce(updated_at, ''), ordinal, is_current_path, text
		from messages `+where+`
		order by ordinal, coalesce(created_at, ''), id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MessageRow
	for rows.Next() {
		var row MessageRow
		var current int
		if err := rows.Scan(&row.ID, &row.Provider, &row.ConversationID, &row.RawID, &row.ParentID,
			&row.Role, &row.CreatedAt, &row.UpdatedAt, &row.Ordinal, &current, &row.Text); err != nil {
			return nil, err
		}
		row.IsCurrentPath = current != 0
		out = append(out, row)
	}
	return out, rows.Err()
}

func (a *Archive) Search(ctx context.Context, query, provider string, limit int) ([]SearchHit, error) {
	if limit <= 0 {
		limit = 25
	}
	return a.SearchMessages(ctx, SearchOptions{Query: query, Provider: provider, Limit: limit})
}

func (a *Archive) SearchMessages(ctx context.Context, opts SearchOptions) ([]SearchHit, error) {
	opts = normalizeSearchOptions(opts)
	whereSQL, args, err := searchWhere(opts)
	if err != nil {
		return nil, err
	}
	orderBy := "score asc, match_at desc, m.id"
	if opts.Sort == "recent" {
		orderBy = "match_at desc, score asc, m.id"
	}
	limitSQL := ""
	if opts.Limit > 0 {
		limitSQL = " limit ?"
		args = append(args, opts.Limit)
	}
	rows, err := a.DB().QueryContext(ctx, `select m.id, m.conversation_id, m.provider, m.role, messages_fts.role,
		coalesce(c.title, ''), coalesce(m.created_at, ''), coalesce(m.updated_at, ''),
		coalesce(rank, 0.0) as score,
		coalesce(m.updated_at, m.created_at, c.updated_at, c.created_at, c.last_seen_at, '') as match_at,
		snippet(messages_fts, 4, '[', ']', '...', 16)
		from messages_fts
		join messages m on m.id = messages_fts.message_id
		join conversations c on c.id = m.conversation_id
		where `+whereSQL+`
		order by `+orderBy+limitSQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		var hit SearchHit
		var matchAt string
		if err := rows.Scan(&hit.MessageID, &hit.ConversationID, &hit.Provider, &hit.Role, &hit.SourceRole,
			&hit.Title, &hit.CreatedAt, &hit.UpdatedAt, &hit.Score, &matchAt, &hit.Text); err != nil {
			return nil, err
		}
		hit.Scope = scopeForRole(hit.SourceRole)
		out = append(out, hit)
	}
	return out, rows.Err()
}

func (a *Archive) SearchConversations(ctx context.Context, opts SearchOptions) ([]ConversationSearchHit, error) {
	opts = normalizeSearchOptions(opts)
	whereSQL, args, err := searchWhere(opts)
	if err != nil {
		return nil, err
	}
	orderBy := "aggregated.best_score asc, aggregated.newest_match_at desc, ranked.conversation_id"
	if opts.Sort == "recent" {
		orderBy = "coalesce(ranked.updated_at, ranked.created_at, aggregated.newest_match_at, '') desc, aggregated.best_score asc, ranked.conversation_id"
	}
	limitSQL := ""
	if opts.Limit > 0 {
		limitSQL = " limit ?"
		args = append(args, opts.Limit)
	}
	rows, err := a.DB().QueryContext(ctx, `with hits as (
			select m.id as message_id, m.conversation_id, m.provider, m.role as message_role,
				messages_fts.role as source_role, c.raw_id, coalesce(c.title, '') as title,
				coalesce(c.created_at, '') as created_at, coalesce(c.updated_at, '') as updated_at,
				c.message_count,
				coalesce(rank, 0.0) as score,
				coalesce(m.updated_at, m.created_at, c.updated_at, c.created_at, c.last_seen_at, '') as match_at,
				snippet(messages_fts, 4, '[', ']', '...', 16) as snippet
			from messages_fts
			join messages m on m.id = messages_fts.message_id
			join conversations c on c.id = m.conversation_id
			where `+whereSQL+`
		),
		ranked as (
			select hits.*,
				row_number() over (
					partition by conversation_id
					order by score asc, match_at desc, message_id
				) as rn
			from hits
		),
		aggregated as (
			select conversation_id, count(*) as match_count, min(score) as best_score,
				max(match_at) as newest_match_at, group_concat(distinct source_role) as matched_roles
			from hits
			group by conversation_id
		)
		select ranked.conversation_id, ranked.provider, ranked.raw_id, ranked.title,
			ranked.created_at, ranked.updated_at, ranked.message_count,
			aggregated.match_count, aggregated.best_score, aggregated.newest_match_at,
			coalesce(aggregated.matched_roles, ''), ranked.message_id, ranked.message_role,
			ranked.source_role, ranked.snippet
		from ranked
		join aggregated on aggregated.conversation_id = ranked.conversation_id
		where ranked.rn = 1
		order by `+orderBy+limitSQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConversationSearchHit
	for rows.Next() {
		var hit ConversationSearchHit
		var roles string
		if err := rows.Scan(&hit.ID, &hit.Provider, &hit.RawID, &hit.Title, &hit.CreatedAt, &hit.UpdatedAt,
			&hit.MessageCount, &hit.MatchCount, &hit.Score, &hit.NewestMatchAt, &roles, &hit.BestMessageID,
			&hit.BestRole, &hit.BestSourceRole, &hit.Snippet); err != nil {
			return nil, err
		}
		hit.BestScope = scopeForRole(hit.BestSourceRole)
		hit.MatchedRoles = splitRoleList(roles)
		hit.MatchedScopes = scopesForRoles(hit.MatchedRoles)
		out = append(out, hit)
	}
	return out, rows.Err()
}

func (a *Archive) Query(ctx context.Context, query string, args ...any) (store.QueryResult, error) {
	return a.store.Query(ctx, query, args...)
}

func conversationWhere(filter ConversationFilter, lastID string, args *[]any) (string, error) {
	var where []string
	if filter.Provider != "" && filter.Provider != "all" {
		where = append(where, "provider = ?")
		*args = append(*args, filter.Provider)
	}
	if filter.ConversationID != "" {
		where = append(where, "id = ?")
		*args = append(*args, filter.ConversationID)
	}
	if filter.Since != "" {
		where = append(where, "coalesce(updated_at, created_at, last_seen_at, '') >= ?")
		*args = append(*args, filter.Since)
	}
	if filter.Until != "" {
		where = append(where, "coalesce(updated_at, created_at, last_seen_at, '') <= ?")
		*args = append(*args, filter.Until)
	}
	if lastID != "" {
		where = append(where, "id > ?")
		*args = append(*args, lastID)
	}
	if len(where) == 0 {
		return "", nil
	}
	return "where " + strings.Join(where, " and "), nil
}

func normalizeSearchOptions(opts SearchOptions) SearchOptions {
	if opts.Provider == "" {
		opts.Provider = "all"
	}
	if opts.Role == "" {
		opts.Role = "all"
	}
	if opts.Scope == "" {
		opts.Scope = "visible"
	}
	opts.PathMode = normalizedPathMode(opts.PathMode)
	if opts.Sort == "" {
		opts.Sort = "relevance"
	}
	return opts
}

func normalizedPathMode(pathMode string) string {
	if pathMode == "" {
		return "current"
	}
	return pathMode
}

func searchWhere(opts SearchOptions) (string, []any, error) {
	wordTerms, literalTerms := textnorm.SearchTerms(opts.Query)
	if len(wordTerms) == 0 && len(literalTerms) == 0 {
		return "", nil, fmt.Errorf("search query is empty")
	}
	var where []string
	var args []any
	if len(wordTerms) > 0 {
		where = append(where, "messages_fts match ?")
		args = append(args, textnorm.FTS5Query(wordTerms))
	}
	for _, term := range literalTerms {
		where = append(where, `messages_fts.body like ? escape '\'`)
		args = append(args, "%"+textnorm.EscapeLike(term)+"%")
	}
	if opts.Provider != "" && opts.Provider != "all" {
		where = append(where, "m.provider = ?")
		args = append(args, opts.Provider)
	}
	switch opts.Scope {
	case "", "visible":
		where = append(where, "messages_fts.role in ('user', 'assistant', 'unknown', 'attachment')")
	case "transcript":
		where = append(where, "messages_fts.role in ('user', 'assistant', 'unknown')")
	case "attachments":
		where = append(where, "messages_fts.role = 'attachment'")
	case "internal":
		where = append(where, "messages_fts.role not in ('user', 'assistant', 'unknown', 'attachment')")
	case "all":
	default:
		return "", nil, fmt.Errorf("unsupported search scope %q", opts.Scope)
	}
	if opts.Role != "" && opts.Role != "all" {
		where = append(where, "messages_fts.role = ?")
		args = append(args, opts.Role)
	}
	switch opts.PathMode {
	case "", "current":
		where = append(where, `(m.is_current_path = 1 or not exists (
			select 1 from messages current_path
			where current_path.conversation_id = m.conversation_id and current_path.is_current_path = 1
		))`)
	case "all":
	default:
		return "", nil, fmt.Errorf("unsupported message path %q", opts.PathMode)
	}
	if opts.Since != "" {
		where = append(where, "coalesce(m.updated_at, m.created_at, c.updated_at, c.created_at, c.last_seen_at, '') >= ?")
		args = append(args, opts.Since)
	}
	if opts.Until != "" {
		where = append(where, "coalesce(m.updated_at, m.created_at, c.updated_at, c.created_at, c.last_seen_at, '') <= ?")
		args = append(args, opts.Until)
	}
	if len(where) == 0 {
		return "", nil, fmt.Errorf("search query is empty")
	}
	return strings.Join(where, " and "), args, nil
}

func scopeForRole(role string) string {
	switch role {
	case "attachment":
		return "attachments"
	case "user", "assistant", "unknown":
		return "transcript"
	default:
		return "internal"
	}
}

func splitRoleList(roles string) []string {
	if roles == "" {
		return nil
	}
	seen := map[string]bool{}
	for _, role := range strings.Split(roles, ",") {
		role = strings.TrimSpace(role)
		if role != "" {
			seen[role] = true
		}
	}
	out := make([]string, 0, len(seen))
	for role := range seen {
		out = append(out, role)
	}
	sort.Strings(out)
	return out
}

func scopesForRoles(roles []string) []string {
	seen := map[string]bool{}
	for _, role := range roles {
		seen[scopeForRole(role)] = true
	}
	out := make([]string, 0, len(seen))
	for scope := range seen {
		out = append(out, scope)
	}
	sort.Strings(out)
	return out
}
