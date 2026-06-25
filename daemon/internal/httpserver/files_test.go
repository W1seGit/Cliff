package httpserver

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func TestUploadFileStreamsMultipartToDisk(t *testing.T) {
	root := t.TempDir()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	mustWriteField(t, writer, "action", "upload")
	mustWriteField(t, writer, "path", "")
	file, err := writer.CreateFormFile("file", "server.properties")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("server-port=25565\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/servers/test/files", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()

	apiHandler{}.uploadFile(response, request, store.Server{Path: root})

	if response.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body=%s", response.Code, response.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(root, "server.properties"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "server-port=25565\n" {
		t.Fatalf("uploaded content mismatch: %q", string(data))
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["fileName"] != "server.properties" || payload["path"] != "server.properties" {
		t.Fatalf("unexpected upload payload: %#v", payload)
	}
}

func TestUploadFileRejectsFileBeforeAction(t *testing.T) {
	root := t.TempDir()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "server.properties")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("server-port=25565\n")); err != nil {
		t.Fatal(err)
	}
	mustWriteField(t, writer, "action", "upload")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/servers/test/files", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()

	apiHandler{}.uploadFile(response, request, store.Server{Path: root})

	if response.Code != http.StatusBadRequest {
		t.Fatalf("upload status = %d, body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "Upload action must be sent before the file") {
		t.Fatalf("unexpected error body: %s", response.Body.String())
	}
}

func TestUploadFileRejectsFileBeforePath(t *testing.T) {
	root := t.TempDir()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	mustWriteField(t, writer, "action", "upload")
	file, err := writer.CreateFormFile("file", "server.properties")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("server-port=25565\n")); err != nil {
		t.Fatal(err)
	}
	mustWriteField(t, writer, "path", "")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/servers/test/files", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()

	apiHandler{}.uploadFile(response, request, store.Server{Path: root})

	if response.Code != http.StatusBadRequest {
		t.Fatalf("upload status = %d, body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "Upload path must be sent before the file") {
		t.Fatalf("unexpected error body: %s", response.Body.String())
	}
}

func mustWriteField(t *testing.T, writer *multipart.Writer, name string, value string) {
	t.Helper()
	if err := writer.WriteField(name, value); err != nil {
		t.Fatal(err)
	}
}
