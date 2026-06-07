package claudeweb

import (
	"fmt"

	"github.com/openclaw/aicrawl/internal/archive"
	"github.com/openclaw/aicrawl/internal/ingest/claudeexport"
)

const (
	Provider   = "claude"
	SourceKind = "claude_web"
)

func StreamFile(path string, emit archive.ConversationEmitter) (archive.ParsedSource, error) {
	parsed, err := claudeexport.StreamWebFile(path, emit)
	if err != nil {
		return archive.ParsedSource{}, err
	}
	if parsed.Provider != Provider || parsed.SourceKind != SourceKind {
		return archive.ParsedSource{}, fmt.Errorf("unexpected Claude web source identity")
	}
	return parsed, nil
}
