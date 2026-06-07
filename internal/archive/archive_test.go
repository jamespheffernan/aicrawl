package archive_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openclaw/aicrawl/internal/archive"
	"github.com/openclaw/aicrawl/internal/schema"
	_ "modernc.org/sqlite"
)

func TestMigrationUserVersionAndNewerFailFast(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/archive.db"
	ar, err := archive.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	var version int
	if err := ar.DB().QueryRowContext(ctx, "pragma user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != schema.Version {
		t.Fatalf("user_version = %d, want %d", version, schema.Version)
	}
	if _, err := ar.DB().ExecContext(ctx, "pragma user_version = 99"); err != nil {
		t.Fatalf("set newer user_version: %v", err)
	}
	if err := ar.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}
	if _, err := archive.Open(ctx, dbPath); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("open newer schema error = %v, want newer schema error", err)
	}
}

func TestMigrationV1ToV2AddsSyncCursorColumns(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "archive.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.ExecContext(ctx, `create table sync_state (
		source_kind text primary key,
		last_import_id text,
		last_import_at text,
		conversation_count integer not null default 0,
		message_count integer not null default 0,
		updated_at text not null
	);
	insert into sync_state(source_kind, last_import_id, last_import_at, conversation_count, message_count, updated_at)
	values('chatgpt_web', 'import:old', '2026-06-07T10:00:00.000000000Z', 2, 3, '2026-06-07T10:00:00.000000000Z');
	pragma user_version = 1;`); err != nil {
		t.Fatalf("seed v1 db: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v1 db: %v", err)
	}

	ar, err := archive.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open migrated archive: %v", err)
	}
	defer ar.Close()
	version, err := ar.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if version != schema.Version {
		t.Fatalf("schema version = %d, want %d", version, schema.Version)
	}
	state, ok, err := ar.SyncState(ctx, "chatgpt_web")
	if err != nil {
		t.Fatalf("sync state: %v", err)
	}
	if !ok || state.LastCheckedAt != state.LastImportAt || state.ConversationCount != 2 || state.MessageCount != 3 {
		t.Fatalf("migrated sync state = %+v", state)
	}
}

func TestSyncStateRecordsSourceHashAndProviderCursor(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "archive.db")
	ar, err := archive.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer ar.Close()
	sourcePath := filepath.Join(t.TempDir(), "source.fixture.json")
	if err := writeTestSource(sourcePath); err != nil {
		t.Fatalf("write source: %v", err)
	}
	parsed := archive.ParsedSource{
		Provider:   "chatgpt",
		SourceKind: "chatgpt_web",
		Conversations: []archive.Conversation{
			{
				ID:         "chatgpt:cursor",
				Provider:   "chatgpt",
				RawID:      "cursor",
				Title:      "Cursor",
				RawPayload: []byte(`{"id":"cursor"}`),
				Messages: []archive.Message{
					{
						ID:             "chatgpt:cursor:msg",
						Provider:       "chatgpt",
						ConversationID: "chatgpt:cursor",
						RawID:          "msg",
						Role:           "assistant",
						Ordinal:        0,
						IsCurrentPath:  true,
						IsPathKnown:    true,
						Text:           "cursor sync state fixture",
						RawPayload:     []byte(`{"id":"msg"}`),
					},
				},
			},
		},
	}
	if _, err := ar.ImportParsed(ctx, sourcePath, parsed); err != nil {
		t.Fatalf("import parsed: %v", err)
	}
	state, ok, err := ar.SyncState(ctx, "chatgpt_web")
	if err != nil {
		t.Fatalf("sync state: %v", err)
	}
	if !ok || state.CursorKind != "source_hash" || state.CursorValue == "" || state.LastCheckedAt == "" {
		t.Fatalf("source-hash sync state = %+v", state)
	}
	cursor := archive.SyncCursor{
		Kind:           "provider_updated_at",
		Value:          "2026-06-07T11:00:00.000000000Z",
		At:             "2026-06-07T11:00:00.000000000Z",
		CandidateCount: 4,
	}
	if err := ar.UpdateSyncCursor(ctx, "chatgpt_web", cursor); err != nil {
		t.Fatalf("update sync cursor: %v", err)
	}
	state, ok, err = ar.SyncState(ctx, "chatgpt_web")
	if err != nil {
		t.Fatalf("sync state after cursor: %v", err)
	}
	if !ok || state.CursorKind != cursor.Kind || state.CursorAt != cursor.At || state.CandidateCount != 4 || state.LastImportID == "" {
		t.Fatalf("provider cursor sync state = %+v", state)
	}
}

func TestImportIdempotentAndReservedSearch(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/archive.db"
	ar, err := archive.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer ar.Close()
	sourcePath := t.TempDir() + "/source.fixture.json"
	parsed := archive.ParsedSource{
		Provider:   "claude",
		SourceKind: "claude_export",
		Conversations: []archive.Conversation{
			{
				ID:         "claude:conv",
				Provider:   "claude",
				RawID:      "conv",
				Title:      "Reserved Terms",
				RawPayload: []byte(`{"uuid":"conv"}`),
				Messages: []archive.Message{
					{
						ID:             "claude:conv:msg",
						Provider:       "claude",
						ConversationID: "claude:conv",
						RawID:          "msg",
						Role:           "user",
						Ordinal:        0,
						IsCurrentPath:  true,
						IsPathKnown:    true,
						Text:           "known fixture phrase AND OR NOT NEAR *",
						RawPayload:     []byte(`{"uuid":"msg"}`),
					},
				},
			},
		},
	}
	if err := writeTestSource(sourcePath); err != nil {
		t.Fatalf("write source: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := ar.ImportParsed(ctx, sourcePath, parsed); err != nil {
			t.Fatalf("import %d: %v", i, err)
		}
	}
	counts, err := ar.Counts(ctx)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts.Conversations != 1 || counts.Messages != 1 {
		t.Fatalf("counts = %+v, want 1 conversation and 1 message", counts)
	}
	for _, query := range []string{"AND", "OR", "NOT", "NEAR", "*"} {
		hits, err := ar.Search(ctx, query, "all", 10)
		if err != nil {
			t.Fatalf("search %q returned error: %v", query, err)
		}
		if len(hits) != 1 {
			t.Fatalf("search %q returned %d hits, want 1", query, len(hits))
		}
	}
}

func TestImportStreamSkipsParserForCompletedImport(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/archive.db"
	ar, err := archive.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer ar.Close()

	sourcePath := filepath.Join(t.TempDir(), "source.fixture.json")
	if err := writeTestSource(sourcePath); err != nil {
		t.Fatalf("write source: %v", err)
	}
	conversation := archive.Conversation{
		ID:         "chatgpt:stream-skip",
		Provider:   "chatgpt",
		RawID:      "stream-skip",
		Title:      "Stream Skip",
		RawPayload: []byte(`{"id":"stream-skip"}`),
		Messages: []archive.Message{
			{
				ID:             "chatgpt:stream-skip:msg",
				Provider:       "chatgpt",
				ConversationID: "chatgpt:stream-skip",
				RawID:          "msg",
				Role:           "user",
				Ordinal:        0,
				IsCurrentPath:  true,
				IsPathKnown:    true,
				Text:           "stream import fixture",
				RawPayload:     []byte(`{"id":"msg"}`),
			},
		},
	}
	parseCalls := 0
	first, err := ar.ImportStream(ctx, sourcePath, "chatgpt", "chatgpt_export", func(emit archive.ConversationEmitter) error {
		parseCalls++
		return emit(conversation, []string{"synthetic warning"})
	})
	if err != nil {
		t.Fatalf("first import stream: %v", err)
	}
	if first.AlreadyImported {
		t.Fatalf("first import reported already imported")
	}
	if parseCalls != 1 {
		t.Fatalf("parse calls after first import = %d, want 1", parseCalls)
	}

	second, err := ar.ImportStream(ctx, sourcePath, "chatgpt", "chatgpt_export", func(emit archive.ConversationEmitter) error {
		parseCalls++
		return fmt.Errorf("parser should not run for a completed import")
	})
	if err != nil {
		t.Fatalf("second import stream: %v", err)
	}
	if !second.AlreadyImported {
		t.Fatalf("second import did not report already imported")
	}
	if parseCalls != 1 {
		t.Fatalf("parse calls after second import = %d, want 1", parseCalls)
	}
	if second.Conversations != first.Conversations || second.Messages != first.Messages || second.Attachments != first.Attachments {
		t.Fatalf("second import stats = %+v, want counts from first import %+v", second, first)
	}
	if len(second.Warnings) != 1 || second.Warnings[0] != "synthetic warning" {
		t.Fatalf("second import warnings = %#v, want stored warning", second.Warnings)
	}
}

func TestImportStreamBoundsStoredWarnings(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/archive.db"
	ar, err := archive.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer ar.Close()

	sourcePath := filepath.Join(t.TempDir(), "source.fixture.json")
	if err := writeTestSource(sourcePath); err != nil {
		t.Fatalf("write source: %v", err)
	}
	conversation := archive.Conversation{
		ID:         "chatgpt:warning-bound",
		Provider:   "chatgpt",
		RawID:      "warning-bound",
		Title:      "Warning Bound",
		RawPayload: []byte(`{"id":"warning-bound"}`),
		Messages: []archive.Message{{
			ID:             "chatgpt:warning-bound:msg",
			Provider:       "chatgpt",
			ConversationID: "chatgpt:warning-bound",
			RawID:          "msg",
			Role:           "user",
			Ordinal:        0,
			Text:           "warning bound fixture",
			RawPayload:     []byte(`{"id":"msg"}`),
		}},
	}
	warnings := make([]string, 105)
	for i := range warnings {
		warnings[i] = fmt.Sprintf("synthetic warning %03d", i)
	}
	stats, err := ar.ImportStream(ctx, sourcePath, "chatgpt", "chatgpt_export", func(emit archive.ConversationEmitter) error {
		return emit(conversation, warnings)
	})
	if err != nil {
		t.Fatalf("import stream: %v", err)
	}
	if len(stats.Warnings) != 101 {
		t.Fatalf("warning count = %d, want bounded warnings plus truncation summary", len(stats.Warnings))
	}
	if stats.Warnings[len(stats.Warnings)-1] != "truncated 5 additional warnings" {
		t.Fatalf("last warning = %q, want truncation summary", stats.Warnings[len(stats.Warnings)-1])
	}
}

func TestAttachmentTextIsSearchableAcrossOverlappingImports(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/archive.db"
	ar, err := archive.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer ar.Close()

	sourceDir := t.TempDir()
	fullSource := filepath.Join(sourceDir, "full.fixture.json")
	narrowSource := filepath.Join(sourceDir, "narrow.fixture.json")
	if err := os.WriteFile(fullSource, []byte(`{"synthetic":"full"}`), 0o600); err != nil {
		t.Fatalf("write full source: %v", err)
	}
	if err := os.WriteFile(narrowSource, []byte(`{"synthetic":"narrow"}`), 0o600); err != nil {
		t.Fatalf("write narrow source: %v", err)
	}

	message := archive.Message{
		ID:             "claude:attachment-search:msg",
		Provider:       "claude",
		ConversationID: "claude:attachment-search",
		RawID:          "msg",
		Role:           "user",
		Ordinal:        0,
		IsCurrentPath:  true,
		IsPathKnown:    true,
		Text:           "message text without the attachment-only phrase",
		RawPayload:     []byte(`{"uuid":"msg"}`),
	}
	full := archive.ParsedSource{
		Provider:   "claude",
		SourceKind: "claude_export",
		Conversations: []archive.Conversation{
			{
				ID:         "claude:attachment-search",
				Provider:   "claude",
				RawID:      "attachment-search",
				Title:      "Attachment Search",
				RawPayload: []byte(`{"uuid":"attachment-search","fixture":"full"}`),
				Messages:   []archive.Message{message},
				Attachments: []archive.Attachment{
					{
						ID:             "claude:attachment-search:msg:attachments:0",
						Provider:       "claude",
						ConversationID: "claude:attachment-search",
						MessageID:      "claude:attachment-search:msg",
						Kind:           "attachments",
						Filename:       "notes.fixture.txt",
						MimeType:       "text/plain",
						Text:           "durable attachment phrase",
						RawPayload:     []byte(`{"file_name":"notes.fixture.txt","text":"durable attachment phrase"}`),
					},
				},
			},
		},
	}
	narrow := archive.ParsedSource{
		Provider:   "claude",
		SourceKind: "claude_export",
		Conversations: []archive.Conversation{
			{
				ID:         "claude:attachment-search",
				Provider:   "claude",
				RawID:      "attachment-search",
				Title:      "Attachment Search",
				RawPayload: []byte(`{"uuid":"attachment-search","fixture":"narrow"}`),
				Messages:   []archive.Message{message},
			},
		},
	}

	if _, err := ar.ImportParsed(ctx, fullSource, full); err != nil {
		t.Fatalf("import full source: %v", err)
	}
	assertSearchHitCount(t, ar, ctx, "durable attachment phrase", 1)

	if _, err := ar.ImportParsed(ctx, narrowSource, narrow); err != nil {
		t.Fatalf("import narrow source: %v", err)
	}
	assertSearchHitCount(t, ar, ctx, "durable attachment phrase", 1)
}

func TestSearchOptionsGroupAndContextWindows(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/archive.db"
	ar, err := archive.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer ar.Close()

	sourcePath := t.TempDir() + "/source.fixture.json"
	if err := writeTestSource(sourcePath); err != nil {
		t.Fatalf("write source: %v", err)
	}
	parsed := archive.ParsedSource{
		Provider:   "claude",
		SourceKind: "claude_export",
		Conversations: []archive.Conversation{
			{
				ID:        "claude:finance-old",
				Provider:  "claude",
				RawID:     "finance-old",
				Title:     "Old Finance Planning",
				CreatedAt: "2026-01-01T00:00:00Z",
				UpdatedAt: "2026-01-02T00:00:00Z",
				RawPayload: []byte(`{
					"uuid":"finance-old"
				}`),
				Messages: []archive.Message{
					{
						ID:             "claude:finance-old:user",
						Provider:       "claude",
						ConversationID: "claude:finance-old",
						RawID:          "user",
						Role:           "user",
						CreatedAt:      "2026-01-01T00:00:00Z",
						Ordinal:        0,
						IsCurrentPath:  true,
						IsPathKnown:    true,
						Text:           "finance planning topic",
						RawPayload:     []byte(`{"uuid":"user"}`),
					},
					{
						ID:             "claude:finance-old:system",
						Provider:       "claude",
						ConversationID: "claude:finance-old",
						RawID:          "system",
						Role:           "system",
						CreatedAt:      "2026-01-01T00:01:00Z",
						Ordinal:        1,
						IsCurrentPath:  true,
						IsPathKnown:    true,
						Text:           "finance hidden tool listing",
						RawPayload:     []byte(`{"uuid":"system"}`),
					},
					{
						ID:             "claude:finance-old:anchor",
						Provider:       "claude",
						ConversationID: "claude:finance-old",
						RawID:          "anchor",
						Role:           "user",
						CreatedAt:      "2026-01-01T00:02:00Z",
						Ordinal:        2,
						IsCurrentPath:  true,
						IsPathKnown:    true,
						Text:           "attachment anchor",
						RawPayload:     []byte(`{"uuid":"anchor"}`),
					},
					{
						ID:             "claude:finance-old:branch",
						Provider:       "claude",
						ConversationID: "claude:finance-old",
						RawID:          "branch",
						Role:           "assistant",
						CreatedAt:      "2026-01-01T00:03:00Z",
						Ordinal:        3,
						IsCurrentPath:  false,
						IsPathKnown:    true,
						Text:           "discarded finance branch",
						RawPayload:     []byte(`{"uuid":"branch"}`),
					},
				},
				Attachments: []archive.Attachment{
					{
						ID:             "claude:finance-old:attachment",
						Provider:       "claude",
						ConversationID: "claude:finance-old",
						MessageID:      "claude:finance-old:anchor",
						Kind:           "attachments",
						Filename:       "finance.txt",
						MimeType:       "text/plain",
						Text:           "finance attached memo",
						RawPayload:     []byte(`{"file_name":"finance.txt"}`),
					},
				},
			},
			{
				ID:        "claude:finance-recent",
				Provider:  "claude",
				RawID:     "finance-recent",
				Title:     "Recent Finance Planning",
				CreatedAt: "2026-02-01T00:00:00Z",
				UpdatedAt: "2026-02-02T00:00:00Z",
				RawPayload: []byte(`{
					"uuid":"finance-recent"
				}`),
				Messages: []archive.Message{
					{
						ID:             "claude:finance-recent:assistant",
						Provider:       "claude",
						ConversationID: "claude:finance-recent",
						RawID:          "assistant",
						Role:           "assistant",
						CreatedAt:      "2026-02-01T00:00:00Z",
						Ordinal:        0,
						IsCurrentPath:  true,
						IsPathKnown:    true,
						Text:           "finance model answer",
						RawPayload:     []byte(`{"uuid":"assistant"}`),
					},
				},
			},
		},
	}
	if _, err := ar.ImportParsed(ctx, sourcePath, parsed); err != nil {
		t.Fatalf("import parsed source: %v", err)
	}

	visibleHits, err := ar.SearchMessages(ctx, archive.SearchOptions{Query: "finance", Scope: "visible", PathMode: "current", Sort: "relevance"})
	if err != nil {
		t.Fatalf("search visible: %v", err)
	}
	if len(visibleHits) != 3 {
		t.Fatalf("visible hits = %d, want user, assistant, and attachment hits", len(visibleHits))
	}
	if !hasSearchHit(visibleHits, "claude:finance-old:anchor", "attachment", "attachments") {
		t.Fatalf("visible hits did not include labeled attachment hit: %+v", visibleHits)
	}

	transcriptHits, err := ar.SearchMessages(ctx, archive.SearchOptions{Query: "finance", Scope: "transcript", PathMode: "current"})
	if err != nil {
		t.Fatalf("search transcript: %v", err)
	}
	if len(transcriptHits) != 2 {
		t.Fatalf("transcript hits = %d, want only user and assistant transcript hits", len(transcriptHits))
	}
	internalHits, err := ar.SearchMessages(ctx, archive.SearchOptions{Query: "finance", Scope: "internal", PathMode: "current"})
	if err != nil {
		t.Fatalf("search internal: %v", err)
	}
	if len(internalHits) != 1 || internalHits[0].SourceRole != "system" {
		t.Fatalf("internal hits = %+v, want system hit", internalHits)
	}
	branchCurrentHits, err := ar.SearchMessages(ctx, archive.SearchOptions{Query: "discarded", Scope: "visible", PathMode: "current"})
	if err != nil {
		t.Fatalf("search current branch: %v", err)
	}
	if len(branchCurrentHits) != 0 {
		t.Fatalf("current path branch hits = %d, want 0", len(branchCurrentHits))
	}
	branchAllHits, err := ar.SearchMessages(ctx, archive.SearchOptions{Query: "discarded", Scope: "visible", PathMode: "all"})
	if err != nil {
		t.Fatalf("search all branches: %v", err)
	}
	if len(branchAllHits) != 1 {
		t.Fatalf("all path branch hits = %d, want 1", len(branchAllHits))
	}

	conversationHits, err := ar.SearchConversations(ctx, archive.SearchOptions{Query: "finance", Scope: "visible", PathMode: "current", Sort: "recent"})
	if err != nil {
		t.Fatalf("search conversations: %v", err)
	}
	if len(conversationHits) != 2 {
		t.Fatalf("conversation hits = %d, want 2", len(conversationHits))
	}
	if conversationHits[0].ID != "claude:finance-recent" {
		t.Fatalf("first recent conversation = %q, want recent conversation", conversationHits[0].ID)
	}

	window, err := ar.MessagesWithOptions(ctx, archive.MessageOptions{
		ConversationID: "claude:finance-old",
		PathMode:       "current",
		AroundID:       "claude:finance-old:anchor",
		Before:         1,
		After:          0,
	})
	if err != nil {
		t.Fatalf("messages around: %v", err)
	}
	if len(window) != 2 || window[0].ID != "claude:finance-old:system" || window[1].ID != "claude:finance-old:anchor" {
		t.Fatalf("context window = %+v, want previous message and anchor", window)
	}
}

func TestOverlappingImportDoesNotShrinkStoredMessageCount(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/archive.db"
	ar, err := archive.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer ar.Close()

	sourceDir := t.TempDir()
	fullSource := filepath.Join(sourceDir, "full.fixture.json")
	narrowSource := filepath.Join(sourceDir, "narrow.fixture.json")
	if err := os.WriteFile(fullSource, []byte(`{"synthetic":"full"}`), 0o600); err != nil {
		t.Fatalf("write full source: %v", err)
	}
	if err := os.WriteFile(narrowSource, []byte(`{"synthetic":"narrow"}`), 0o600); err != nil {
		t.Fatalf("write narrow source: %v", err)
	}

	firstMessage := archive.Message{
		ID:             "claude:overlap:msg-1",
		Provider:       "claude",
		ConversationID: "claude:overlap",
		RawID:          "msg-1",
		Role:           "user",
		Ordinal:        0,
		IsCurrentPath:  true,
		IsPathKnown:    true,
		Text:           "first overlapping message",
		RawPayload:     []byte(`{"uuid":"msg-1"}`),
	}
	secondMessage := archive.Message{
		ID:             "claude:overlap:msg-2",
		Provider:       "claude",
		ConversationID: "claude:overlap",
		RawID:          "msg-2",
		Role:           "assistant",
		Ordinal:        1,
		IsCurrentPath:  true,
		IsPathKnown:    true,
		Text:           "second overlapping message",
		RawPayload:     []byte(`{"uuid":"msg-2"}`),
	}
	full := archive.ParsedSource{
		Provider:   "claude",
		SourceKind: "claude_export",
		Conversations: []archive.Conversation{
			{
				ID:         "claude:overlap",
				Provider:   "claude",
				RawID:      "overlap",
				Title:      "Overlapping Import",
				RawPayload: []byte(`{"uuid":"overlap","fixture":"full"}`),
				Messages:   []archive.Message{firstMessage, secondMessage},
			},
		},
	}
	narrow := archive.ParsedSource{
		Provider:   "claude",
		SourceKind: "claude_export",
		Conversations: []archive.Conversation{
			{
				ID:         "claude:overlap",
				Provider:   "claude",
				RawID:      "overlap",
				Title:      "Overlapping Import",
				RawPayload: []byte(`{"uuid":"overlap","fixture":"narrow"}`),
				Messages:   []archive.Message{firstMessage},
			},
		},
	}

	if _, err := ar.ImportParsed(ctx, fullSource, full); err != nil {
		t.Fatalf("import full source: %v", err)
	}
	if _, err := ar.ImportParsed(ctx, narrowSource, narrow); err != nil {
		t.Fatalf("import narrow source: %v", err)
	}
	conversations, err := ar.Conversations(ctx, "all", 10)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(conversations) != 1 {
		t.Fatalf("conversations = %d, want 1", len(conversations))
	}
	if conversations[0].MessageCount != 2 {
		t.Fatalf("message_count = %d, want stored message count 2", conversations[0].MessageCount)
	}
	messages, err := ar.Messages(ctx, "claude:overlap", "all")
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(messages))
	}
}

func assertSearchHitCount(t *testing.T, ar *archive.Archive, ctx context.Context, query string, want int) {
	t.Helper()
	hits, err := ar.Search(ctx, query, "all", 10)
	if err != nil {
		t.Fatalf("search %q returned error: %v", query, err)
	}
	if len(hits) != want {
		t.Fatalf("search %q returned %d hits, want %d", query, len(hits), want)
	}
}

func hasSearchHit(hits []archive.SearchHit, messageID, sourceRole, scope string) bool {
	for _, hit := range hits {
		if hit.MessageID == messageID && hit.SourceRole == sourceRole && hit.Scope == scope {
			return true
		}
	}
	return false
}

func TestValidateReadOnlySQL(t *testing.T) {
	for _, query := range []string{
		"select count(*) from messages",
		"select 'delete from messages' as text",
		"select ';' as semicolon",
		"with x as (select 1) select * from x",
		"with x as (select 'update messages') select * from x",
		"pragma user_version",
		"pragma table_info(messages)",
		"pragma integrity_check(messages)",
		"pragma foreign_key_check",
	} {
		if _, err := archive.ValidateReadOnlySQL(query); err != nil {
			t.Fatalf("ValidateReadOnlySQL(%q) returned error: %v", query, err)
		}
	}
	for _, query := range []string{
		"delete from messages",
		"with deleted as (delete from messages returning *) select * from deleted",
		"with x as (select 1) delete/**/from messages",
		"with x as (select 1) values (1)",
		"select 1; select 2",
		"pragma writable_schema = 1",
		"pragma user_version = 99",
		"pragma user_version(99)",
		"pragma table_info = messages",
		"pragma table_info(messages) extra",
	} {
		if _, err := archive.ValidateReadOnlySQL(query); err == nil {
			t.Fatalf("ValidateReadOnlySQL(%q) returned nil error", query)
		}
	}
}

func TestExportMarkdownDoesNotOverwriteFilenameCollisions(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/archive.db"
	ar, err := archive.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer ar.Close()

	sourcePath := t.TempDir() + "/source.fixture.json"
	if err := writeTestSource(sourcePath); err != nil {
		t.Fatalf("write source: %v", err)
	}
	const sharedSuffix = "abcdefghijklmnopqrstuvwx"
	firstID := "claude:collision-one-" + sharedSuffix
	secondID := "claude:collision-two-" + sharedSuffix
	parsed := archive.ParsedSource{
		Provider:   "claude",
		SourceKind: "claude_export",
		Conversations: []archive.Conversation{
			{
				ID:         firstID,
				Provider:   "claude",
				RawID:      "collision-one-" + sharedSuffix,
				Title:      "Collision Title",
				RawPayload: []byte(`{"uuid":"collision-one"}`),
				Messages: []archive.Message{
					{
						ID:             firstID + ":msg",
						Provider:       "claude",
						ConversationID: firstID,
						RawID:          "msg",
						Role:           "user",
						Ordinal:        0,
						IsCurrentPath:  true,
						IsPathKnown:    true,
						Text:           "first collision export text",
						RawPayload:     []byte(`{"uuid":"first-msg"}`),
					},
				},
			},
			{
				ID:         secondID,
				Provider:   "claude",
				RawID:      "collision-two-" + sharedSuffix,
				Title:      "Collision Title",
				RawPayload: []byte(`{"uuid":"collision-two"}`),
				Messages: []archive.Message{
					{
						ID:             secondID + ":msg",
						Provider:       "claude",
						ConversationID: secondID,
						RawID:          "msg",
						Role:           "user",
						Ordinal:        0,
						IsCurrentPath:  true,
						IsPathKnown:    true,
						Text:           "second collision export text",
						RawPayload:     []byte(`{"uuid":"second-msg"}`),
					},
				},
			},
		},
	}
	if _, err := ar.ImportParsed(ctx, sourcePath, parsed); err != nil {
		t.Fatalf("import parsed source: %v", err)
	}

	outDir := filepath.Join(t.TempDir(), "export")
	count, err := ar.ExportMarkdown(ctx, outDir, "all")
	if err != nil {
		t.Fatalf("export markdown: %v", err)
	}
	if count != 2 {
		t.Fatalf("export count = %d, want 2", count)
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("read export dir: %v", err)
	}
	var files []string
	var body strings.Builder
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		files = append(files, entry.Name())
		data, err := os.ReadFile(filepath.Join(outDir, entry.Name()))
		if err != nil {
			t.Fatalf("read markdown file %s: %v", entry.Name(), err)
		}
		body.Write(data)
		body.WriteByte('\n')
	}
	if len(files) != 2 {
		t.Fatalf("markdown files = %d, want 2; files = %#v", len(files), files)
	}
	if !strings.Contains(body.String(), "first collision export text") || !strings.Contains(body.String(), "second collision export text") {
		t.Fatalf("exported markdown did not preserve both conversations; data = %q", body.String())
	}
}

func TestExportMarkdownDoesNotChmodExistingDirAndTightensFiles(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/archive.db"
	ar, err := archive.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer ar.Close()

	sourcePath := t.TempDir() + "/source.fixture.json"
	if err := writeTestSource(sourcePath); err != nil {
		t.Fatalf("write source: %v", err)
	}
	parsed := archive.ParsedSource{
		Provider:   "claude",
		SourceKind: "claude_export",
		Conversations: []archive.Conversation{
			{
				ID:         "claude:export-conv",
				Provider:   "claude",
				RawID:      "export-conv",
				Title:      "Export Permissions",
				RawPayload: []byte(`{"uuid":"export-conv"}`),
				Messages: []archive.Message{
					{
						ID:             "claude:export-conv:msg",
						Provider:       "claude",
						ConversationID: "claude:export-conv",
						RawID:          "msg",
						Role:           "user",
						Ordinal:        0,
						IsCurrentPath:  true,
						IsPathKnown:    true,
						Text:           "exported private text",
						RawPayload:     []byte(`{"uuid":"msg"}`),
					},
				},
			},
		},
	}
	if _, err := ar.ImportParsed(ctx, sourcePath, parsed); err != nil {
		t.Fatalf("import parsed source: %v", err)
	}

	outDir := filepath.Join(t.TempDir(), "shared-export")
	if err := os.Mkdir(outDir, 0o755); err != nil {
		t.Fatalf("create export dir: %v", err)
	}
	if err := os.Chmod(outDir, 0o755); err != nil {
		t.Fatalf("chmod export dir: %v", err)
	}
	count, err := ar.ExportMarkdown(ctx, outDir, "all")
	if err != nil {
		t.Fatalf("export markdown: %v", err)
	}
	if count != 1 {
		t.Fatalf("export count = %d, want 1", count)
	}
	assertMode(t, outDir, 0o755)

	exportedPath := singleMarkdownFile(t, outDir)
	assertMode(t, exportedPath, 0o600)
	if err := os.Chmod(exportedPath, 0o644); err != nil {
		t.Fatalf("chmod exported file: %v", err)
	}
	if _, err := ar.ExportMarkdown(ctx, outDir, "all"); err != nil {
		t.Fatalf("re-export markdown: %v", err)
	}
	assertMode(t, outDir, 0o755)
	assertMode(t, exportedPath, 0o600)

	newOutDir := filepath.Join(t.TempDir(), "new-export")
	if _, err := ar.ExportMarkdown(ctx, newOutDir, "all"); err != nil {
		t.Fatalf("export markdown to new dir: %v", err)
	}
	assertMode(t, newOutDir, 0o700)
}

func TestExportMarkdownWithConversationAndQueryFilters(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/archive.db"
	ar, err := archive.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer ar.Close()

	sourcePath := t.TempDir() + "/source.fixture.json"
	if err := writeTestSource(sourcePath); err != nil {
		t.Fatalf("write source: %v", err)
	}
	parsed := archive.ParsedSource{
		Provider:   "claude",
		SourceKind: "claude_export",
		Conversations: []archive.Conversation{
			{
				ID:        "claude:export-one",
				Provider:  "claude",
				RawID:     "export-one",
				Title:     "Export One",
				UpdatedAt: "2026-03-02T00:00:00Z",
				RawPayload: []byte(`{
					"uuid":"export-one"
				}`),
				Messages: []archive.Message{
					{
						ID:             "claude:export-one:msg",
						Provider:       "claude",
						ConversationID: "claude:export-one",
						RawID:          "msg",
						Role:           "user",
						CreatedAt:      "2026-03-01T00:00:00Z",
						Ordinal:        0,
						IsCurrentPath:  true,
						IsPathKnown:    true,
						Text:           "narrow export phrase",
						RawPayload:     []byte(`{"uuid":"msg"}`),
					},
				},
			},
			{
				ID:        "claude:export-two",
				Provider:  "claude",
				RawID:     "export-two",
				Title:     "Export Two",
				UpdatedAt: "2026-04-02T00:00:00Z",
				RawPayload: []byte(`{
					"uuid":"export-two"
				}`),
				Messages: []archive.Message{
					{
						ID:             "claude:export-two:msg",
						Provider:       "claude",
						ConversationID: "claude:export-two",
						RawID:          "msg",
						Role:           "assistant",
						CreatedAt:      "2026-04-01T00:00:00Z",
						Ordinal:        0,
						IsCurrentPath:  true,
						IsPathKnown:    true,
						Text:           "unrelated export text",
						RawPayload:     []byte(`{"uuid":"msg"}`),
					},
				},
			},
		},
	}
	if _, err := ar.ImportParsed(ctx, sourcePath, parsed); err != nil {
		t.Fatalf("import parsed source: %v", err)
	}

	conversationDir := filepath.Join(t.TempDir(), "conversation")
	count, err := ar.ExportMarkdownWithOptions(ctx, archive.MarkdownExportOptions{
		OutDir:         conversationDir,
		Provider:       "all",
		ConversationID: "claude:export-two",
		PathMode:       "all",
	})
	if err != nil {
		t.Fatalf("export conversation: %v", err)
	}
	if count != 1 {
		t.Fatalf("conversation export count = %d, want 1", count)
	}
	data, err := os.ReadFile(singleMarkdownFile(t, conversationDir))
	if err != nil {
		t.Fatalf("read conversation export: %v", err)
	}
	if !strings.Contains(string(data), "unrelated export text") || strings.Contains(string(data), "narrow export phrase") {
		t.Fatalf("conversation export data = %q, want only selected conversation", string(data))
	}

	queryDir := filepath.Join(t.TempDir(), "query")
	count, err = ar.ExportMarkdownWithOptions(ctx, archive.MarkdownExportOptions{
		OutDir:   queryDir,
		Provider: "all",
		Query:    "narrow export phrase",
		PathMode: "current",
	})
	if err != nil {
		t.Fatalf("export query: %v", err)
	}
	if count != 1 {
		t.Fatalf("query export count = %d, want 1", count)
	}
	data, err = os.ReadFile(singleMarkdownFile(t, queryDir))
	if err != nil {
		t.Fatalf("read query export: %v", err)
	}
	if !strings.Contains(string(data), "narrow export phrase") || strings.Contains(string(data), "unrelated export text") {
		t.Fatalf("query export data = %q, want only matching conversation", string(data))
	}
}

func TestExportMarkdownExportsPastIterationBatch(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/archive.db"
	ar, err := archive.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer ar.Close()

	const total = 600
	conversations := make([]archive.Conversation, 0, total)
	for i := 0; i < total; i++ {
		rawID := fmt.Sprintf("batch-%03d", i)
		conversationID := "claude:" + rawID
		messageID := conversationID + ":msg"
		conversations = append(conversations, archive.Conversation{
			ID:         conversationID,
			Provider:   "claude",
			RawID:      rawID,
			Title:      "Batch Export " + rawID,
			RawPayload: []byte(fmt.Sprintf(`{"uuid":%q}`, rawID)),
			Messages: []archive.Message{
				{
					ID:             messageID,
					Provider:       "claude",
					ConversationID: conversationID,
					RawID:          rawID + "-msg",
					Role:           "user",
					Ordinal:        0,
					IsCurrentPath:  true,
					IsPathKnown:    true,
					Text:           "exported private text",
					RawPayload:     []byte(fmt.Sprintf(`{"uuid":%q}`, rawID+"-msg")),
				},
			},
		})
	}
	sourcePath := t.TempDir() + "/source.fixture.json"
	if err := writeTestSource(sourcePath); err != nil {
		t.Fatalf("write source: %v", err)
	}
	parsed := archive.ParsedSource{
		Provider:      "claude",
		SourceKind:    "claude_export",
		Conversations: conversations,
	}
	if _, err := ar.ImportParsed(ctx, sourcePath, parsed); err != nil {
		t.Fatalf("import parsed source: %v", err)
	}

	outDir := filepath.Join(t.TempDir(), "export")
	count, err := ar.ExportMarkdown(ctx, outDir, "all")
	if err != nil {
		t.Fatalf("export markdown: %v", err)
	}
	if count != total {
		t.Fatalf("export count = %d, want %d", count, total)
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("read export dir: %v", err)
	}
	files := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			files++
		}
	}
	if files != total {
		t.Fatalf("markdown files = %d, want %d", files, total)
	}
}

func writeTestSource(path string) error {
	return os.WriteFile(path, []byte(`{"synthetic":true}`), 0o600)
}

func singleMarkdownFile(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read export dir: %v", err)
	}
	var matches []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			matches = append(matches, filepath.Join(dir, entry.Name()))
		}
	}
	if len(matches) != 1 {
		t.Fatalf("markdown files = %d, want 1", len(matches))
	}
	return matches[0]
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}
