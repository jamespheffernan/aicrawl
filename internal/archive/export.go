package archive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var unsafeFilenameChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

const (
	// Keep generated Markdown filenames under common 255-byte filesystem limits
	// while leaving room for provider, title, separators, ID, and extension.
	maxMarkdownTitleFilenamePart = 80
	maxMarkdownIDFilenamePart    = 120
)

func (a *Archive) ExportMarkdown(ctx context.Context, outDir, provider string) (int, error) {
	return a.ExportMarkdownWithOptions(ctx, MarkdownExportOptions{OutDir: outDir, Provider: provider, PathMode: "all"})
}

func (a *Archive) ExportMarkdownWithOptions(ctx context.Context, opts MarkdownExportOptions) (int, error) {
	if strings.TrimSpace(opts.OutDir) == "" {
		return 0, fmt.Errorf("output directory is required")
	}
	if opts.Provider == "" {
		opts.Provider = "all"
	}
	if opts.PathMode == "" {
		opts.PathMode = "all"
	}
	if opts.PathMode != "current" && opts.PathMode != "all" {
		return 0, fmt.Errorf("message path must be current or all")
	}
	if err := ensureExportDir(opts.OutDir); err != nil {
		return 0, err
	}
	if strings.TrimSpace(opts.Query) != "" {
		return a.exportMarkdownSearchResults(ctx, opts)
	}
	count := 0
	err := a.EachConversationFiltered(ctx, ConversationFilter{
		Provider:       opts.Provider,
		ConversationID: opts.ConversationID,
		Since:          opts.Since,
		Until:          opts.Until,
	}, func(conversation ConversationRow) error {
		messages, err := a.Messages(ctx, conversation.ID, opts.PathMode)
		if err != nil {
			return err
		}
		path := filepath.Join(opts.OutDir, markdownFilename(conversation))
		if err := writePrivateFile(path, []byte(renderConversationMarkdown(conversation, messages))); err != nil {
			return fmt.Errorf("write markdown %s: %w", path, err)
		}
		count++
		return nil
	})
	if err != nil {
		return count, err
	}
	return count, nil
}

func (a *Archive) exportMarkdownSearchResults(ctx context.Context, opts MarkdownExportOptions) (int, error) {
	scope := opts.Scope
	if scope == "" {
		scope = "visible"
	}
	role := opts.Role
	if role == "" {
		role = "all"
	}
	sortMode := opts.Sort
	if sortMode == "" {
		sortMode = "recent"
	}
	hits, err := a.SearchConversations(ctx, SearchOptions{
		Query:    opts.Query,
		Provider: opts.Provider,
		Role:     role,
		Scope:    scope,
		PathMode: opts.PathMode,
		Sort:     sortMode,
		Since:    opts.Since,
		Until:    opts.Until,
	})
	if err != nil {
		return 0, err
	}
	count := 0
	for _, hit := range hits {
		if opts.ConversationID != "" && hit.ID != opts.ConversationID {
			continue
		}
		conversation := ConversationRow{
			ID:           hit.ID,
			Provider:     hit.Provider,
			RawID:        hit.RawID,
			Title:        hit.Title,
			CreatedAt:    hit.CreatedAt,
			UpdatedAt:    hit.UpdatedAt,
			MessageCount: hit.MessageCount,
		}
		messages, err := a.Messages(ctx, conversation.ID, opts.PathMode)
		if err != nil {
			return count, err
		}
		path := filepath.Join(opts.OutDir, markdownFilename(conversation))
		if err := writePrivateFile(path, []byte(renderConversationMarkdown(conversation, messages))); err != nil {
			return count, fmt.Errorf("write markdown %s: %w", path, err)
		}
		count++
	}
	return count, nil
}

func ensureExportDir(outDir string) error {
	info, err := os.Stat(outDir)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("markdown export path exists and is not a directory")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat markdown export dir: %w", err)
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return fmt.Errorf("create markdown export dir: %w", err)
	}
	if err := os.Chmod(outDir, 0o700); err != nil {
		return fmt.Errorf("chmod markdown export dir: %w", err)
	}
	return nil
}

func writePrivateFile(path string, data []byte) (err error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	_, err = file.Write(data)
	return err
}

func renderConversationMarkdown(conversation ConversationRow, messages []MessageRow) string {
	var b strings.Builder
	title := conversation.Title
	if title == "" {
		title = conversation.ID
	}
	b.WriteString("# ")
	b.WriteString(escapeMarkdownHeading(title))
	b.WriteString("\n\n")
	b.WriteString("- provider: `")
	b.WriteString(conversation.Provider)
	b.WriteString("`\n")
	b.WriteString("- conversation_id: `")
	b.WriteString(conversation.ID)
	b.WriteString("`\n")
	if conversation.CreatedAt != "" {
		b.WriteString("- created_at: `")
		b.WriteString(conversation.CreatedAt)
		b.WriteString("`\n")
	}
	if conversation.UpdatedAt != "" {
		b.WriteString("- updated_at: `")
		b.WriteString(conversation.UpdatedAt)
		b.WriteString("`\n")
	}
	b.WriteString("\n")
	for _, message := range messages {
		b.WriteString("## ")
		b.WriteString(message.Role)
		if message.CreatedAt != "" {
			b.WriteString(" ")
			b.WriteString(message.CreatedAt)
		}
		b.WriteString("\n\n")
		b.WriteString("<!-- message_id: ")
		b.WriteString(message.ID)
		if message.ParentID != "" {
			b.WriteString(" parent_id: ")
			b.WriteString(message.ParentID)
		}
		b.WriteString(" -->\n\n")
		if strings.TrimSpace(message.Text) == "" {
			b.WriteString("_No text payload._\n\n")
			continue
		}
		b.WriteString(message.Text)
		b.WriteString("\n\n")
	}
	return b.String()
}

func markdownFilename(conversation ConversationRow) string {
	title := strings.ToLower(conversation.Title)
	if title == "" {
		title = conversation.RawID
	}
	title = safeFilenamePart(title, "conversation")
	if len(title) > maxMarkdownTitleFilenamePart {
		title = strings.Trim(title[:maxMarkdownTitleFilenamePart], "-_.")
	}

	idPart := safeFilenamePart(conversation.ID, "conversation")
	if len(idPart) > maxMarkdownIDFilenamePart {
		idPart = hashFilenamePart(conversation.ID)
	}
	return conversation.Provider + "-" + title + "-" + idPart + ".md"
}

func safeFilenamePart(value, fallback string) string {
	value = unsafeFilenameChars.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-_.")
	if value == "" {
		return fallback
	}
	return value
}

func hashFilenamePart(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func escapeMarkdownHeading(s string) string {
	return strings.ReplaceAll(s, "#", "\\#")
}
