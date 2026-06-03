package security_test

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/openclaw/aicrawl/internal/security"
)

func TestReadJSONSourceRejectsZipTraversal(t *testing.T) {
	for _, entryName := range []string{"../conversations.json", `..\conversations.json`} {
		t.Run(entryName, func(t *testing.T) {
			zipPath := filepath.Join(t.TempDir(), "bad.fixture.zip")
			writeZipEntry(t, zipPath, entryName)
			if _, err := security.ReadJSONSource(zipPath); err == nil {
				t.Fatalf("ReadJSONSource accepted traversal zip entry")
			}
		})
	}
}

func TestReadJSONSourceRejectsAbsoluteZipPaths(t *testing.T) {
	for _, entryName := range []string{"/conversations.json", `C:\conversations.json`, "C:/conversations.json"} {
		t.Run(entryName, func(t *testing.T) {
			zipPath := filepath.Join(t.TempDir(), "absolute.fixture.zip")
			writeZipEntry(t, zipPath, entryName)
			if _, err := security.ReadJSONSource(zipPath); err == nil {
				t.Fatalf("ReadJSONSource accepted absolute zip entry")
			}
		})
	}
}

func TestReadJSONSourceOrdersConversationFilesFirstDeterministically(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "ordered.fixture.zip")
	writeZipEntries(t, zipPath, zipFixtureEntry{Name: "other.json", Data: `{"other":true}`}, zipFixtureEntry{Name: "nested/conversations.json", Data: `{"nested":true}`}, zipFixtureEntry{Name: "conversations.json", Data: `{"root":true}`})

	files, err := security.ReadJSONSource(zipPath)
	if err != nil {
		t.Fatalf("ReadJSONSource returned error: %v", err)
	}
	wantNames := []string{"conversations.json", "nested/conversations.json", "other.json"}
	if len(files) != len(wantNames) {
		t.Fatalf("file count = %d, want %d", len(files), len(wantNames))
	}
	for i, want := range wantNames {
		if files[i].Name != want {
			t.Fatalf("file %d = %q, want %q", i, files[i].Name, want)
		}
	}
}

func TestReadJSONSourceAllowsAttachmentHeavyZipFanOut(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "many-entries.fixture.zip")
	entries := []zipFixtureEntry{{Name: "conversations.json", Data: `[]`}}
	for i := 0; i < 300; i++ {
		entries = append(entries, zipFixtureEntry{Name: filepath.Join("attachments", fmt.Sprintf("file-%03d.txt", i)), Data: "ignored"})
	}
	writeZipEntries(t, zipPath, entries...)

	files, err := security.ReadJSONSource(zipPath)
	if err != nil {
		t.Fatalf("ReadJSONSource returned error: %v", err)
	}
	if len(files) != 1 || files[0].Name != "conversations.json" {
		t.Fatalf("files = %+v, want conversations.json only", files)
	}
}

type zipFixtureEntry struct {
	Name string
	Data string
}

func writeZipEntry(t *testing.T, zipPath, entryName string) {
	t.Helper()
	writeZipEntries(t, zipPath, zipFixtureEntry{Name: entryName, Data: `[]`})
}

func writeZipEntries(t *testing.T, zipPath string, entries ...zipFixtureEntry) {
	t.Helper()
	file, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(file)
	for _, entry := range entries {
		w, err := zw.Create(entry.Name)
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := w.Write([]byte(entry.Data)); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close zip file: %v", err)
	}
}
