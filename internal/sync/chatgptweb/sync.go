package chatgptweb

import (
	"fmt"

	"github.com/openclaw/aicrawl/internal/archive"
	"github.com/openclaw/aicrawl/internal/ingest/chatgptexport"
)

const (
	Provider   = "chatgpt"
	SourceKind = "chatgpt_web"
)

func StreamFile(path string, emit archive.ConversationEmitter) (archive.ParsedSource, error) {
	parsed, err := chatgptexport.StreamWebFile(path, emit)
	if err != nil {
		return archive.ParsedSource{}, err
	}
	if parsed.Provider != Provider || parsed.SourceKind != SourceKind {
		return archive.ParsedSource{}, fmt.Errorf("unexpected ChatGPT web source identity")
	}
	return parsed, nil
}
