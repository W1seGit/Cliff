package httpserver

import (
	"bytes"
	"compress/gzip"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Text based files compress to a third of their size or less. Images and fonts
// are already compressed, so they are served as they are.
var compressibleExtensions = map[string]bool{
	".html": true, ".js": true, ".css": true, ".svg": true, ".json": true,
	".txt": true, ".map": true, ".xml": true, ".webmanifest": true,
}

const maxCompressedFileBytes = 4 << 20

type gzipKey struct {
	path    string
	size    int64
	modTime int64
}

// staticGzipCache keeps the compressed copy of each static file so a file is
// compressed once, not on every request. The web folder is small and only
// changes on update, which changes the size or modification time and so the key.
type staticGzipCache struct {
	mu      sync.Mutex
	entries map[gzipKey][]byte
}

var staticGzip = &staticGzipCache{entries: map[gzipKey][]byte{}}

func (c *staticGzipCache) get(key gzipKey, path string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if data, ok := c.entries[key]; ok {
		return data, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	// A new build replaces old entries for the same file.
	for existing := range c.entries {
		if existing.path == key.path {
			delete(c.entries, existing)
		}
	}
	c.entries[key] = buf.Bytes()
	return buf.Bytes(), nil
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(fields[0]), "gzip") {
			for _, param := range fields[1:] {
				if strings.TrimSpace(param) == "q=0" {
					return false
				}
			}
			return true
		}
	}
	return false
}

// serveStaticFile serves a file from the web folder, gzip compressed when the
// browser accepts it and the file is text.
func serveStaticFile(w http.ResponseWriter, r *http.Request, fullPath string) {
	ext := strings.ToLower(filepath.Ext(fullPath))
	if !compressibleExtensions[ext] || !acceptsGzip(r) || r.Header.Get("Range") != "" {
		http.ServeFile(w, r, fullPath)
		return
	}
	info, err := os.Stat(fullPath)
	if err != nil || info.IsDir() || info.Size() > maxCompressedFileBytes {
		http.ServeFile(w, r, fullPath)
		return
	}
	data, err := staticGzip.get(gzipKey{fullPath, info.Size(), info.ModTime().UnixNano()}, fullPath)
	if err != nil {
		http.ServeFile(w, r, fullPath)
		return
	}
	if contentType := mime.TypeByExtension(ext); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Add("Vary", "Accept-Encoding")
	http.ServeContent(w, r, filepath.Base(fullPath), info.ModTime().Truncate(time.Second), bytes.NewReader(data))
}
