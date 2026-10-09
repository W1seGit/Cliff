package httpserver

import (
	"archive/zip"
	"bytes"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

// openTestStore opens a fresh SQLite store in temp dirs and closes it when the
// test ends.
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	return openTestStoreAt(t, t.TempDir(), t.TempDir())
}

// openTestStoreAt opens a store whose database lives in dataDir and whose
// default server root is serverRoot.
func openTestStoreAt(t *testing.T, dataDir string, serverRoot string) *store.Store {
	t.Helper()
	db, err := store.Open(filepath.Join(dataDir, "test.sqlite"), serverRoot)
	if err != nil {
		t.Fatalf("opening test store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// touch writes a file, creating parent folders as needed.
func touch(t *testing.T, path string, content string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

// writeZip writes a zip archive holding files (name -> content) at path,
// creating parent folders as needed.
func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for name, content := range files {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// makeZip writes a zip archive into a fresh temp dir and returns its path.
func makeZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "archive")
	writeZip(t, path, entries)
	return path
}

// captureLogs sends slog output to a buffer at debug level for one test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buffer bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buffer
}

// formPart is one part of a multipart form; a non-empty filename makes it a
// file upload with value as its content.
type formPart struct {
	name     string
	value    string
	filename string
}

// multipartPost builds a POST whose multipart parts are sent in the given order.
func multipartPost(t *testing.T, target string, parts ...formPart) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, part := range parts {
		if part.filename == "" {
			if err := writer.WriteField(part.name, part.value); err != nil {
				t.Fatal(err)
			}
			continue
		}
		file, err := writer.CreateFormFile(part.name, part.filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(part.value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, target, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}
