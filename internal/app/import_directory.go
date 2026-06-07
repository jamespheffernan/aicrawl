package app

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/openclaw/aicrawl/internal/archive"
)

type importDirectoryStats struct {
	Provider               string   `json:"provider"`
	SourceKind             string   `json:"source_kind"`
	Sources                int      `json:"sources"`
	ImportedSources        int      `json:"imported_sources"`
	AlreadyImportedSources int      `json:"already_imported_sources"`
	SkippedSources         int      `json:"skipped_sources,omitempty"`
	Conversations          int      `json:"conversations"`
	Messages               int      `json:"messages"`
	Attachments            int      `json:"attachments"`
	Warnings               []string `json:"warnings,omitempty"`
	PrivacyReminder        string   `json:"privacy_reminder"`
}

func importSourceIsDir(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

func inspectImportDirectory(root, provider string) (importDryRunReport, error) {
	if !supportsDirectoryImport(provider) {
		return importDryRunReport{}, fmt.Errorf("directory import requires provider openclaw, codex, gemini, claude-code, cursor, or hermes")
	}
	header, err := importSourceIdentity(provider)
	if err != nil {
		return importDryRunReport{}, err
	}
	files, err := discoverImportSourceFiles(root, provider)
	if err != nil {
		return importDryRunReport{}, err
	}
	report := importDryRunReport{
		DryRun:          true,
		Provider:        header.Provider,
		SourceKind:      header.SourceKind,
		Sources:         len(files),
		PrivacyReminder: "Source files contain private conversation data. Dry-run does not write the archive.",
	}
	for i, path := range files {
		fileReport, err := inspectImportFile(path, provider)
		if err != nil {
			if shouldSkipImportSourceError(err) {
				report.SkippedSources++
				report.Warnings = append(report.Warnings, skippedImportSourceWarning("inspect", i, len(files), err))
				continue
			}
			return importDryRunReport{}, fmt.Errorf("inspect source %d of %d: %w", i+1, len(files), err)
		}
		report.Conversations += fileReport.Conversations
		report.Messages += fileReport.Messages
		report.Attachments += fileReport.Attachments
		report.Warnings = append(report.Warnings, fileReport.Warnings...)
	}
	report.Warnings = limitReportWarnings(report.Warnings)
	return report, nil
}

func importDirectory(ctx context.Context, ar *archive.Archive, root, provider string) (importDirectoryStats, error) {
	if !supportsDirectoryImport(provider) {
		return importDirectoryStats{}, fmt.Errorf("directory import requires provider openclaw, codex, gemini, claude-code, cursor, or hermes")
	}
	header, err := importSourceIdentity(provider)
	if err != nil {
		return importDirectoryStats{}, err
	}
	files, err := discoverImportSourceFiles(root, provider)
	if err != nil {
		return importDirectoryStats{}, err
	}
	stats := importDirectoryStats{
		Provider:        header.Provider,
		SourceKind:      header.SourceKind,
		Sources:         len(files),
		PrivacyReminder: "Source files still contain private conversation data. Store or delete them intentionally.",
	}
	for i, path := range files {
		fileStats, err := importStream(ctx, ar, path, provider)
		if err != nil {
			if shouldSkipImportSourceError(err) {
				stats.SkippedSources++
				stats.Warnings = append(stats.Warnings, skippedImportSourceWarning("import", i, len(files), err))
				continue
			}
			return importDirectoryStats{}, fmt.Errorf("import source %d of %d: %w", i+1, len(files), err)
		}
		stats.Conversations += fileStats.Conversations
		stats.Messages += fileStats.Messages
		stats.Attachments += fileStats.Attachments
		stats.Warnings = append(stats.Warnings, fileStats.Warnings...)
		if fileStats.AlreadyImported {
			stats.AlreadyImportedSources++
		} else {
			stats.ImportedSources++
		}
	}
	stats.Warnings = limitReportWarnings(stats.Warnings)
	return stats, nil
}

func shouldSkipImportSourceError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "contains no importable messages")
}

func skippedImportSourceWarning(action string, index, total int, err error) string {
	return fmt.Sprintf("skipped %s source %d of %d: %s", action, index+1, total, err)
}

func discoverImportSourceFiles(root, provider string) ([]string, error) {
	if !supportsDirectoryImport(provider) {
		return nil, fmt.Errorf("directory import requires provider openclaw, codex, gemini, claude-code, cursor, or hermes")
	}
	if provider == "hermes" {
		stateDB := filepath.Join(root, "state.db")
		if info, err := os.Stat(stateDB); err == nil && !info.IsDir() {
			return []string{stateDB}, nil
		}
	}
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if matchesImportSourceFile(path, provider) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("no %s source files found in directory", provider)
	}
	return files, nil
}

func supportsDirectoryImport(provider string) bool {
	switch provider {
	case "openclaw", "codex", "gemini", "claude-code", "cursor", "hermes":
		return true
	default:
		return false
	}
}

func matchesImportSourceFile(path, provider string) bool {
	name := strings.ToLower(filepath.Base(path))
	ext := strings.ToLower(filepath.Ext(path))
	switch provider {
	case "openclaw", "codex", "claude-code":
		return ext == ".jsonl"
	case "gemini":
		return ext == ".json"
	case "cursor":
		return name == "store.db"
	case "hermes":
		return name == "state.db" || ext == ".jsonl" || (ext == ".json" && strings.HasPrefix(name, "session_"))
	default:
		return false
	}
}
