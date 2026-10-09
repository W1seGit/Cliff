package httpserver

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestSPAFileServer(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "_next", "static", "chunks", "app.js"), "console.log('ok');\n")
	touch(t, filepath.Join(root, "index.html"), "index")
	handler := spaFileServer(root)

	tests := []struct {
		name         string
		path         string
		wantStatus   int
		wantCache    string
		wantBody     string
		wantBodyNone string // body that must NOT be served
	}{
		{"static file is served directly with an immutable cache", "/_next/static/chunks/app.js", http.StatusOK, "public, max-age=31536000, immutable", "console.log('ok');\n", ""},
		{"unknown page falls back to index with no-cache", "/servers/srv_test/overview", http.StatusOK, "no-cache", "index", ""},
		{"API paths do not fall back to index", "/api/servers", http.StatusNotFound, "", "", "index"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))

			if response.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tc.wantStatus)
			}
			if tc.wantCache != "" {
				if got := response.Header().Get("Cache-Control"); got != tc.wantCache {
					t.Fatalf("Cache-Control = %q, want %q", got, tc.wantCache)
				}
			}
			if tc.wantBody != "" && response.Body.String() != tc.wantBody {
				t.Fatalf("body = %q, want %q", response.Body.String(), tc.wantBody)
			}
			if tc.wantBodyNone != "" && response.Body.String() == tc.wantBodyNone {
				t.Fatalf("body = %q, which must not be served for %s", response.Body.String(), tc.path)
			}
		})
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
		t.Run(requestPath, func(t *testing.T) {
			if got := staticRequestPath(requestPath); got != "" {
				t.Fatalf("staticRequestPath(%q) = %q, want empty", requestPath, got)
			}
		})
	}
}
