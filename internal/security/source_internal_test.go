package security

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadJSONSourceLimitsBufferedJSONOnly(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "large-non-json.fixture.zip")
	writeInternalZipEntries(t, zipPath,
		internalZipFixtureEntry{Name: "account-data.txt", Data: strings.Repeat("x", 64)},
		internalZipFixtureEntry{Name: "conversations.json", Data: `[]`},
	)

	files, err := readJSONSource(zipPath, 10, 8)
	if err != nil {
		t.Fatalf("readJSONSource returned error: %v", err)
	}
	if len(files) != 1 || files[0].Name != "conversations.json" {
		t.Fatalf("files = %+v, want only conversations.json", files)
	}
}

func TestReadJSONSourceRejectsOversizedJSONEntry(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "oversized-json.fixture.zip")
	writeInternalZipEntries(t, zipPath,
		internalZipFixtureEntry{Name: "conversations.json", Data: `{"too":"large"}`},
	)

	if _, err := readJSONSource(zipPath, 10, 8); err == nil {
		t.Fatalf("readJSONSource accepted JSON entry over limit")
	}
}

func TestReadJSONSourceAllowsExactJSONLimit(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "exact-json.fixture.zip")
	writeInternalZipEntries(t, zipPath,
		internalZipFixtureEntry{Name: "conversations.json", Data: `[]`},
	)

	files, err := readJSONSource(zipPath, 10, 2)
	if err != nil {
		t.Fatalf("readJSONSource returned error: %v", err)
	}
	if len(files) != 1 || string(files[0].Data) != `[]` {
		t.Fatalf("files = %+v, want exact JSON payload", files)
	}
}

type internalZipFixtureEntry struct {
	Name string
	Data string
}

func writeInternalZipEntries(t *testing.T, zipPath string, entries ...internalZipFixtureEntry) {
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
