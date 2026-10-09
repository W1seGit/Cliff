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

func TestUploadFileStreamsMultipartToDisk(t *testing.T) {
	root := t.TempDir()
	request := multipartPost(t, "/api/servers/test/files",
		formPart{name: "action", value: "upload"},
		formPart{name: "path", value: ""},
		formPart{name: "file", value: "server-port=25565\n", filename: "server.properties"})
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
		t.Fatalf("uploaded content = %q, want %q", string(data), "server-port=25565\n")
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["fileName"] != "server.properties" || payload["path"] != "server.properties" {
		t.Fatalf("unexpected upload payload: %#v", payload)
	}
}

func TestUploadFileRejectsFileSentBeforeItsFields(t *testing.T) {
	file := formPart{name: "file", value: "server-port=25565\n", filename: "server.properties"}
	action := formPart{name: "action", value: "upload"}
	path := formPart{name: "path", value: ""}
	tests := []struct {
		name    string
		parts   []formPart
		wantErr string
	}{
		{"file before action", []formPart{file, action}, "Upload action must be sent before the file"},
		{"file before path", []formPart{action, file, path}, "Upload path must be sent before the file"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			apiHandler{}.uploadFile(response, multipartPost(t, "/api/servers/test/files", tc.parts...), store.Server{Path: t.TempDir()})

			if response.Code != http.StatusBadRequest {
				t.Fatalf("upload status = %d, want %d (body=%s)", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), tc.wantErr) {
				t.Fatalf("error body = %s, want it to contain %q", response.Body.String(), tc.wantErr)
			}
		})
	}
}
