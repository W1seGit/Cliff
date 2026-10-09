package httpserver

import (
	"encoding/json"
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
	touch(t, filepath.Join(worldDir, "level.dat"), "level")

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
	mustMkdir(t, filepath.Join(serverDir, "not-world"))

	if _, _, _, err := worldArchiveTarget(store.Server{Name: "Server", Path: serverDir}, "not-world"); err == nil {
		t.Fatal("expected non-world folder to be rejected")
	}
}

func TestDatapackDownloadTargetValidatesAndStripsDisabledSuffix(t *testing.T) {
	serverDir := t.TempDir()
	datapackPath := filepath.Join(serverDir, "world", "datapacks", "example.zip.disabled")
	touch(t, datapackPath, "zip")

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
	writeZip(t, zipPath, map[string]string{"server.properties": "server-port=25565\n"})

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
	touch(t, filepath.Join(worldDir, "level.dat"), "level")
	touch(t, filepath.Join(serverDir, "server.properties"), "level-name=world\n")

	request := multipartPost(t, "/api/servers/test/worlds",
		formPart{name: "action", value: "upload-datapack"},
		formPart{name: "worldName", value: "world"},
		formPart{name: "file", value: "datapack zip bytes", filename: "example.zip"})
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
		t.Fatalf("uploaded datapack content = %q, want %q", string(data), "datapack zip bytes")
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
	request := multipartPost(t, "/api/servers/test/worlds",
		formPart{name: "file", value: "zip", filename: "example.zip"},
		formPart{name: "action", value: "upload-datapack"})
	response := httptest.NewRecorder()

	apiHandler{}.worldUploadAction(response, request, store.Server{Path: t.TempDir()})

	if response.Code != http.StatusBadRequest {
		t.Fatalf("upload status = %d, want %d (body=%s)", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "Upload action must be sent before the file") {
		t.Fatalf("error body = %s, want it to mention the action ordering", response.Body.String())
	}
}
