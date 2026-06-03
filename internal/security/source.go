package security

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// MaxZipEntries is intentionally high enough for attachment-heavy official
	// account exports while still bounding malicious archive fan-out.
	MaxZipEntries = 100_000
	// MaxJSONBytes is an abuse guard for a single JSON import stream, not a
	// normal product capacity target. Parser code streams official top-level
	// conversation arrays instead of buffering up to this size.
	MaxJSONBytes int64 = 1 << 40
)

type SourceFile struct {
	Name string
	Data []byte
}

func ReadJSONSource(path string) ([]SourceFile, error) {
	return readJSONSource(path, MaxZipEntries, MaxJSONBytes)
}

func WalkJSONSources(path string, fn func(name string, r io.Reader) error) error {
	return walkJSONSources(path, MaxZipEntries, MaxJSONBytes, fn)
}

func readJSONSource(path string, maxZipEntries int, maxJSONBytes int64) ([]SourceFile, error) {
	var files []SourceFile
	err := walkJSONSources(path, maxZipEntries, maxJSONBytes, func(name string, r io.Reader) error {
		data, err := io.ReadAll(r)
		if err != nil {
			return fmt.Errorf("read JSON source %s: %w", name, err)
		}
		files = append(files, SourceFile{Name: name, Data: data})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func walkJSONSources(path string, maxZipEntries int, maxJSONBytes int64, fn func(name string, r io.Reader) error) error {
	if fn == nil {
		return fmt.Errorf("JSON source callback is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	if strings.EqualFold(filepath.Ext(path), ".zip") {
		return walkZipJSON(path, maxZipEntries, maxJSONBytes, fn)
	}
	isZip, err := hasZipMagic(path)
	if err != nil {
		return err
	}
	if isZip {
		return walkZipJSON(path, maxZipEntries, maxJSONBytes, fn)
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer file.Close()
	if info.Size() > maxJSONBytes {
		return fmt.Errorf("JSON source exceeds maximum supported size of %d bytes", maxJSONBytes)
	}
	return fn(filepath.Base(path), newMaxBytesReader(file, maxJSONBytes))
}

func walkZipJSON(path string, maxZipEntries int, maxJSONBytes int64, fn func(name string, r io.Reader) error) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer reader.Close()
	if len(reader.File) > maxZipEntries {
		return fmt.Errorf("zip has %d entries, maximum is %d", len(reader.File), maxZipEntries)
	}
	var entries []*zip.File
	for _, entry := range reader.File {
		if err := validateZipEntry(entry); err != nil {
			return err
		}
		if entry.FileInfo().IsDir() || !strings.EqualFold(filepath.Ext(entry.Name), ".json") {
			continue
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		iConversation := filepath.Base(entries[i].Name) == "conversations.json"
		jConversation := filepath.Base(entries[j].Name) == "conversations.json"
		if iConversation != jConversation {
			return iConversation
		}
		return entries[i].Name < entries[j].Name
	})
	if len(entries) == 0 {
		return fmt.Errorf("zip contains no JSON files")
	}
	for _, entry := range entries {
		if entry.UncompressedSize64 > uint64(maxJSONBytes) {
			return fmt.Errorf("zip JSON entry exceeds maximum supported size of %d bytes", maxJSONBytes)
		}
		rc, err := entry.Open()
		if err != nil {
			return fmt.Errorf("open zip entry: %w", err)
		}
		err = fn(entry.Name, newMaxBytesReader(rc, maxJSONBytes))
		closeErr := rc.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return fmt.Errorf("close zip entry: %w", closeErr)
		}
	}
	return nil
}

func hasZipMagic(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("open source: %w", err)
	}
	defer file.Close()
	header := make([]byte, 4)
	n, err := io.ReadFull(file, header)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return false, fmt.Errorf("read source header: %w", err)
	}
	return n == len(header) && bytes.Equal(header, []byte("PK\x03\x04")), nil
}

func validateZipEntry(entry *zip.File) error {
	name := entry.Name
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("zip contains an empty entry name")
	}
	normalized := strings.ReplaceAll(name, `\`, "/")
	if isAbsoluteZipPath(name, normalized) {
		return fmt.Errorf("zip contains absolute path entry")
	}
	clean := path.Clean(normalized)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return fmt.Errorf("zip contains path traversal entry")
	}
	if entry.FileInfo().Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("zip contains unsupported symlink entry")
	}
	return nil
}

func isAbsoluteZipPath(name, normalized string) bool {
	if filepath.IsAbs(name) || strings.HasPrefix(normalized, "/") {
		return true
	}
	if len(normalized) >= 3 && normalized[1] == ':' && normalized[2] == '/' {
		return isASCIIAlpha(normalized[0])
	}
	return false
}

func isASCIIAlpha(b byte) bool {
	return ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z')
}

type maxBytesReader struct {
	r         io.Reader
	limit     int64
	remaining int64
}

func newMaxBytesReader(r io.Reader, limit int64) io.Reader {
	return &maxBytesReader{r: r, limit: limit, remaining: limit}
}

func (r *maxBytesReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.remaining <= 0 {
		var one [1]byte
		n, err := r.r.Read(one[:])
		if n > 0 {
			return 0, fmt.Errorf("JSON source exceeds maximum supported size of %d bytes", r.limit)
		}
		return 0, err
	}
	if int64(len(p)) > r.remaining {
		p = p[:int(r.remaining)]
	}
	n, err := r.r.Read(p)
	r.remaining -= int64(n)
	return n, err
}
