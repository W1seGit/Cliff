package httpserver

import (
	"archive/zip"
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

func TestWorldArchiveTargetValidatesWorldAndBuildsNames(t *testing.T) {
	serverDir := t.TempDir()
	worldDir := filepath.Join(serverDir, "world")
	if err := os.MkdirAll(worldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worldDir, "level.dat"), []byte("level"), 0o644); err != nil {
		t.Fatal(err)
	}

	worldPath, fileName, rootName, err := worldArchiveTarget(store.Server{Name: "Survival Server", Path: serverDir}, "world")
	if err != nil {
		t.Fatal(err)
	}
	if worldPath != worldDir {
		t.Fatalf("expected world path %q, got %q", worldDir, worldPath)
	}
	if fileName != "survival-server-world.zip" {
		t.Fatalf("unexpected archive file name %q", fileName)
	}
	if rootName != "world" {
		t.Fatalf("expected archive root world, got %q", rootName)
	}
}

func TestWorldArchiveTargetRejectsNonWorldFolder(t *testing.T) {
	serverDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(serverDir, "not-world"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := worldArchiveTarget(store.Server{Name: "Server", Path: serverDir}, "not-world"); err == nil {
		t.Fatal("expected non-world folder to be rejected")
	}
}

func TestDatapackDownloadTargetValidatesAndStripsDisabledSuffix(t *testing.T) {
	serverDir := t.TempDir()
	datapackDir := filepath.Join(serverDir, "world", "datapacks")
	if err := os.MkdirAll(datapackDir, 0o755); err != nil {
		t.Fatal(err)
	}
	datapackPath := filepath.Join(datapackDir, "example.zip.disabled")
	if err := os.WriteFile(datapackPath, []byte("zip"), 0o644); err != nil {
		t.Fatal(err)
	}

	target, fileName, err := datapackDownloadTarget(store.Server{Path: serverDir}, "world", "example.zip.disabled")
	if err != nil {
		t.Fatal(err)
	}
	if target != datapackPath {
		t.Fatalf("expected target %q, got %q", datapackPath, target)
	}
	if fileName != "example.zip" {
		t.Fatalf("expected disabled suffix to be stripped, got %q", fileName)
	}
}

func TestZipReaderFromMultipartUsesSeekableUploadWithoutBuffering(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "server.zip")
	file, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("server.properties")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("server-port=25565\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	upload, err := os.Open(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer upload.Close()

	reader, err := zipReaderFromMultipart(upload, "zip failed")
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != 1 || reader.File[0].Name != "server.properties" {
		t.Fatalf("unexpected zip entries: %#v", reader.File)
	}
}

func TestWorldUploadActionStreamsDatapackToDisk(t *testing.T) {
	serverDir := t.TempDir()
	worldDir := filepath.Join(serverDir, "world")
	if err := os.MkdirAll(worldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worldDir, "level.dat"), []byte("level"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(serverDir, "server.properties"), []byte("level-name=world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	mustWriteField(t, writer, "action", "upload-datapack")
	mustWriteField(t, writer, "worldName", "world")
	file, err := writer.CreateFormFile("file", "example.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("datapack zip bytes")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/servers/test/worlds", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()

	apiHandler{}.worldUploadAction(response, request, store.Server{Path: serverDir})

	if response.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body=%s", response.Code, response.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(worldDir, "datapacks", "example.zip"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "datapack zip bytes" {
		t.Fatalf("uploaded datapack content mismatch: %q", string(data))
	}
	var payload worldsPayload
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ActiveWorld != "world" || len(payload.Worlds) != 1 || len(payload.Worlds[0].Datapacks) != 1 {
		t.Fatalf("unexpected worlds payload: %#v", payload)
	}
}

func TestWorldUploadActionRejectsFileBeforeAction(t *testing.T) {
	serverDir := t.TempDir()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "example.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("zip")); err != nil {
		t.Fatal(err)
	}
	mustWriteField(t, writer, "action", "upload-datapack")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/servers/test/worlds", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()

	apiHandler{}.worldUploadAction(response, request, store.Server{Path: serverDir})

	if response.Code != http.StatusBadRequest {
		t.Fatalf("upload status = %d, body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "Upload action must be sent before the file") {
		t.Fatalf("unexpected error body: %s", response.Body.String())
	}
}
