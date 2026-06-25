package httpserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSPAFileServerServesStaticFileDirectlyWithImmutableCache(t *testing.T) {
	root := t.TempDir()
	chunkDir := filepath.Join(root, "_next", "static", "chunks")
	if err := os.MkdirAll(chunkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chunkDir, "app.js"), []byte("console.log('ok');\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("index"), 0o644); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/_next/static/chunks/app.js", nil)
	response := httptest.NewRecorder()
	spaFileServer(root).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
	if got := response.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("unexpected static cache header %q", got)
	}
	if response.Body.String() != "console.log('ok');\n" {
		t.Fatalf("unexpected body %q", response.Body.String())
	}
}

func TestSPAFileServerFallbackUsesNoCache(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("index"), 0o644); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/servers/srv_test/overview", nil)
	response := httptest.NewRecorder()
	spaFileServer(root).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("unexpected fallback cache header %q", got)
	}
	if response.Body.String() != "index" {
		t.Fatalf("unexpected body %q", response.Body.String())
	}
}

func TestSPAFileServerDoesNotFallbackForAPIPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("index"), 0o644); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	response := httptest.NewRecorder()
	spaFileServer(root).ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for API path, got %d", response.Code)
	}
	if response.Body.String() == "index" {
		t.Fatal("API path should not receive SPA index fallback")
	}
}

func TestStaticRequestPathRejectsEscapingPaths(t *testing.T) {
	for _, requestPath := range []string{
		"/../secret.txt",
		"/%2e%2e/secret.txt",
		"/_next/../../secret.txt",
		"/_next/%2e%2e/%2e%2e/secret.txt",
		"/C:/Windows/System32/drivers/etc/hosts",
	} {
		if got := staticRequestPath(requestPath); got != "" {
			t.Fatalf("staticRequestPath(%q) = %q, want empty", requestPath, got)
		}
	}
}
