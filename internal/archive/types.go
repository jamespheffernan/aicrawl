package archive

type Conversation struct {
	ID            string
	Provider      string
	AccountID     string
	RawID         string
	Title         string
	CreatedAt     string
	UpdatedAt     string
	CurrentNodeID string
	RawPayload    []byte
	Messages      []Message
	Edges         []MessageEdge
	Attachments   []Attachment
}

type Message struct {
	ID             string
	Provider       string
	ConversationID string
	RawID          string
	ParentID       string
	Role           string
	Sender         string
	CreatedAt      string
	UpdatedAt      string
	Ordinal        int
	IsCurrentPath  bool
	IsPathKnown    bool
	Text           string
	RawPayload     []byte
}

type MessageEdge struct {
	Provider        string
	ConversationID  string
	ParentMessageID string
	ChildMessageID  string
	RawParentID     string
	RawChildID      string
	Kind            string
}

type Attachment struct {
	ID             string
	Provider       string
	ConversationID string
	MessageID      string
	Kind           string
	Filename       string
	MimeType       string
	Text           string
	RawPayload     []byte
}

type ParsedSource struct {
	Provider      string
	SourceKind    string
	Conversations []Conversation
	Warnings      []string
}

type ImportStats struct {
	ImportID        string   `json:"import_id"`
	Provider        string   `json:"provider"`
	SourceKind      string   `json:"source_kind"`
	SourceHash      string   `json:"source_hash"`
	Conversations   int      `json:"conversations"`
	Messages        int      `json:"messages"`
	Attachments     int      `json:"attachments"`
	Warnings        []string `json:"warnings,omitempty"`
	AlreadyImported bool     `json:"already_imported"`
	CompletedAt     string   `json:"completed_at"`
	PrivacyReminder string   `json:"privacy_reminder"`
}

type Counts struct {
	Providers     int64 `json:"providers"`
	Imports       int64 `json:"imports"`
	Conversations int64 `json:"conversations"`
	Messages      int64 `json:"messages"`
	Edges         int64 `json:"edges"`
	Attachments   int64 `json:"attachments"`
}

type SyncState struct {
	SourceKind        string `json:"source_kind"`
	LastImportID      string `json:"last_import_id,omitempty"`
	LastImportAt      string `json:"last_import_at,omitempty"`
	LastCheckedAt     string `json:"last_checked_at,omitempty"`
	CursorKind        string `json:"cursor_kind,omitempty"`
	CursorValue       string `json:"cursor_value,omitempty"`
	CursorAt          string `json:"cursor_at,omitempty"`
	CandidateCount    int64  `json:"candidate_count,omitempty"`
	ConversationCount int64  `json:"conversation_count"`
	MessageCount      int64  `json:"message_count"`
	UpdatedAt         string `json:"updated_at"`
}

type SyncCursor struct {
	Kind           string
	Value          string
	At             string
	CandidateCount int64
}

type ConversationSyncStatus struct {
	SourceKind     string `json:"source_kind"`
	Provider       string `json:"provider"`
	RawID          string `json:"raw_id"`
	ConversationID string `json:"conversation_id"`
	Status         string `json:"status"`
	HTTPStatus     int    `json:"http_status,omitempty"`
}

type ConversationRow struct {
	ID           string `json:"id"`
	Provider     string `json:"provider"`
	RawID        string `json:"raw_id"`
	Title        string `json:"title,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
	MessageCount int64  `json:"message_count"`
}

type MessageRow struct {
	ID             string `json:"id"`
	Provider       string `json:"provider"`
	ConversationID string `json:"conversation_id"`
	RawID          string `json:"raw_id"`
	ParentID       string `json:"parent_id,omitempty"`
	Role           string `json:"role"`
	CreatedAt      string `json:"created_at,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
	Ordinal        int    `json:"ordinal"`
	IsCurrentPath  bool   `json:"is_current_path"`
	Text           string `json:"text"`
}

type MessageOptions struct {
	ConversationID string
	PathMode       string
	AroundID       string
	Before         int
	After          int
}

type SearchHit struct {
	MessageID      string  `json:"message_id"`
	ConversationID string  `json:"conversation_id"`
	Provider       string  `json:"provider"`
	Role           string  `json:"role"`
	SourceRole     string  `json:"source_role,omitempty"`
	Scope          string  `json:"scope,omitempty"`
	Title          string  `json:"title,omitempty"`
	CreatedAt      string  `json:"created_at,omitempty"`
	UpdatedAt      string  `json:"updated_at,omitempty"`
	Score          float64 `json:"score,omitempty"`
	Text           string  `json:"text"`
}

type ConversationSearchHit struct {
	ID             string   `json:"id"`
	Provider       string   `json:"provider"`
	RawID          string   `json:"raw_id"`
	Title          string   `json:"title,omitempty"`
	CreatedAt      string   `json:"created_at,omitempty"`
	UpdatedAt      string   `json:"updated_at,omitempty"`
	MessageCount   int64    `json:"message_count"`
	MatchCount     int64    `json:"match_count"`
	MatchedRoles   []string `json:"matched_roles,omitempty"`
	MatchedScopes  []string `json:"matched_scopes,omitempty"`
	BestMessageID  string   `json:"best_message_id"`
	BestRole       string   `json:"best_role"`
	BestSourceRole string   `json:"best_source_role,omitempty"`
	BestScope      string   `json:"best_scope,omitempty"`
	NewestMatchAt  string   `json:"newest_match_at,omitempty"`
	Score          float64  `json:"score,omitempty"`
	Snippet        string   `json:"snippet"`
}

type SearchOptions struct {
	Query    string
	Provider string
	Limit    int
	Role     string
	Scope    string
	PathMode string
	Sort     string
	Since    string
	Until    string
}

type ConversationFilter struct {
	Provider       string
	ConversationID string
	Since          string
	Until          string
}

type MarkdownExportOptions struct {
	OutDir         string
	Provider       string
	ConversationID string
	Query          string
	Role           string
	Scope          string
	PathMode       string
	Sort           string
	Since          string
	Until          string
}
