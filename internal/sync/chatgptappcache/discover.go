package chatgptappcache

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const SourceKind = "chatgpt_app_cache_ids"

var cacheConversationID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{7,127}$`)

type Candidate struct {
	ID      string
	ModTime time.Time
}

type Report struct {
	SourceKind string
	IDs        []string
	Warnings   []string
}

func Discover(root string, limit int) (Report, error) {
	if strings.TrimSpace(root) == "" {
		return Report{}, fmt.Errorf("ChatGPT app cache path is required")
	}
	info, err := os.Stat(root)
	if err != nil {
		return Report{}, fmt.Errorf("inspect ChatGPT app cache: %w", err)
	}
	if !info.IsDir() {
		return Report{}, fmt.Errorf("ChatGPT app cache path must be a directory")
	}
	candidates := map[string]Candidate{}
	var invalid int
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Ext(entry.Name()) != ".data" || !inConversationCache(path) {
			return nil
		}
		id := strings.TrimSuffix(entry.Name(), ".data")
		if !cacheConversationID.MatchString(id) {
			invalid++
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		current, ok := candidates[id]
		if !ok || info.ModTime().After(current.ModTime) {
			candidates[id] = Candidate{ID: id, ModTime: info.ModTime()}
		}
		return nil
	})
	if err != nil {
		return Report{}, fmt.Errorf("scan ChatGPT app cache: %w", err)
	}
	ordered := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		ordered = append(ordered, candidate)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if !ordered[i].ModTime.Equal(ordered[j].ModTime) {
			return ordered[i].ModTime.After(ordered[j].ModTime)
		}
		return ordered[i].ID < ordered[j].ID
	})
	if limit > 0 && len(ordered) > limit {
		ordered = ordered[:limit]
	}
	ids := make([]string, 0, len(ordered))
	for _, candidate := range ordered {
		ids = append(ids, candidate.ID)
	}
	var warnings []string
	if invalid > 0 {
		warnings = append(warnings, fmt.Sprintf("skipped %d ChatGPT app cache files with invalid conversation IDs", invalid))
	}
	return Report{SourceKind: SourceKind, IDs: ids, Warnings: warnings}, nil
}

func inConversationCache(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.HasPrefix(part, "conversations-v3-") {
			return true
		}
	}
	return false
}
