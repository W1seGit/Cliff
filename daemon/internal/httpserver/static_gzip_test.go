package httpserver

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticFilesAreGzippedWhenAccepted(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("body { color: red; }\n", 500)
	touch(t, filepath.Join(dir, "app.css"), body)
	touch(t, filepath.Join(dir, "logo.png"), "png-bytes")
	handler := spaFileServer(dir)

	req := httptest.NewRequest(http.MethodGet, "/app.css", nil)
	req.Header.Set("Accept-Encoding", "gzip, br")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("css should be gzipped, headers: %v", rec.Header())
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("content type lost: %q", rec.Header().Get("Content-Type"))
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(zr)
	if string(got) != body {
		t.Fatal("decompressed body differs from the file")
	}

	// Second request is served from the cache and is still correct.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") != "gzip" || rec.Body.Len() >= len(body) {
		t.Fatal("cached response should still be compressed")
	}

	plain := httptest.NewRecorder()
	handler.ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "/app.css", nil))
	if plain.Header().Get("Content-Encoding") != "" || plain.Body.String() != body {
		t.Fatal("without Accept-Encoding the file is served as it is")
	}

	img := httptest.NewRecorder()
	imgReq := httptest.NewRequest(http.MethodGet, "/logo.png", nil)
	imgReq.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(img, imgReq)
	if img.Header().Get("Content-Encoding") != "" {
		t.Fatal("images are not compressed again")
	}

	refused := httptest.NewRequest(http.MethodGet, "/app.css", nil)
	refused.Header.Set("Accept-Encoding", "gzip;q=0")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, refused)
	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatal("gzip;q=0 means the client refuses gzip")
	}
}
