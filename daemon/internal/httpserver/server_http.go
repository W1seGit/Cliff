package httpserver

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	urlpath "path"
	"path/filepath"
	"strings"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func spaFileServer(root string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		requestPath := staticRequestPath(r.URL.Path)
		if requestPath != "" {
			fullPath := filepath.Join(root, requestPath)
			if info, err := os.Stat(fullPath); err == nil && !info.IsDir() {
				setStaticCacheHeaders(w, r.URL.Path, false)
				serveStaticFile(w, r, fullPath)
				return
			}
		}

		indexPath := filepath.Join(root, "index.html")
		if _, err := os.Stat(indexPath); err == nil {
			setStaticCacheHeaders(w, r.URL.Path, true)
			serveStaticFile(w, r, indexPath)
			return
		}

		writeError(w, http.StatusNotFound, "static dashboard has not been built into the daemon web directory")
	})
}

func staticRequestPath(requestPath string) string {
	decodedPath, err := url.PathUnescape(requestPath)
	if err != nil {
		return ""
	}
	normalizedPath := strings.ReplaceAll(decodedPath, "\\", "/")
	segments := strings.Split(normalizedPath, "/")
	for _, segment := range segments {
		if segment == ".." || strings.Contains(segment, ":") {
			return ""
		}
	}
	cleaned := strings.TrimPrefix(urlpath.Clean("/"+normalizedPath), "/")
	if cleaned == "." || cleaned == "" {
		return "index.html"
	}
	localPath := filepath.FromSlash(cleaned)
	if !filepath.IsLocal(localPath) {
		return ""
	}
	return localPath
}

func setStaticCacheHeaders(w http.ResponseWriter, requestPath string, spaFallback bool) {
	if spaFallback || requestPath == "/" || strings.HasSuffix(requestPath, ".html") {
		w.Header().Set("Cache-Control", "no-cache")
		return
	}
	if strings.HasPrefix(requestPath, "/_next/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	// errBody keeps the start of an error response so the log says why it failed.
	errBody []byte
}

func (rec *statusRecorder) WriteHeader(code int) {
	rec.status = code
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *statusRecorder) Write(data []byte) (int, error) {
	if rec.status >= 400 && len(rec.errBody) < 300 {
		room := 300 - len(rec.errBody)
		if len(data) < room {
			room = len(data)
		}
		rec.errBody = append(rec.errBody, data[:room]...)
	}
	return rec.ResponseWriter.Write(data)
}

func (rec *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := rec.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("ResponseWriter does not support Hijack")
}

func (rec *statusRecorder) Flush() {
	if f, ok := rec.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rec *statusRecorder) Unwrap() http.ResponseWriter {
	return rec.ResponseWriter
}

func withErrorLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		logRequest(r, rec, time.Since(start))
	})
}

// logRequest writes the requests worth reading in the daemon log: failures
// with the reason, and every successful change made through the API. Plain
// reads are only logged at debug level so polling does not drown the log.
func logRequest(r *http.Request, rec *statusRecorder, duration time.Duration) {
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		return
	}
	attrs := []any{"method", r.Method, "path", r.URL.Path, "status", rec.status, "duration", duration.Round(time.Millisecond).String()}
	reason := strings.TrimSpace(string(rec.errBody))
	if reason != "" {
		attrs = append(attrs, "response", reason)
	}
	switch {
	case rec.status >= 500:
		slog.Error("request failed", attrs...)
	case rec.status >= 400:
		// A signed-out poll or a missing file is routine; anything else is worth seeing.
		if rec.status == http.StatusUnauthorized || (rec.status == http.StatusNotFound && r.Method == http.MethodGet) {
			slog.Debug("request refused", attrs...)
			return
		}
		slog.Warn("request refused", attrs...)
	case r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions:
		slog.Info("request", attrs...)
	default:
		slog.Debug("request", attrs...)
	}
}

// requireEULA stops a start when eula.txt does not say eula=true. The file is
// the source of truth, so this holds whether or not anyone is watching the
// console. The dashboard answers the "eula_required" code with the accept dialog.
func requireEULA(w http.ResponseWriter, server store.Server) bool {
	if readEULAAccepted(filepath.Join(server.Path, "eula.txt")) {
		return true
	}
	slog.Warn("start refused: the Minecraft EULA has not been accepted", "server", server.ID, "name", server.Name)
	writeJSON(w, http.StatusConflict, map[string]string{
		"error": "Accept the Minecraft EULA before starting this server.",
		"code":  "eula_required",
	})
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	if status >= 500 {
		slog.Error("HTTP server error", "status", status, "message", message)
	}
	writeJSON(w, status, map[string]string{"error": message})
}

const maxJSONRequestBytes int64 = 1 * 1024 * 1024

func readJSON(r *http.Request, value any) error {
	defer r.Body.Close()
	return decodeBoundedRequestJSON(r.Body, value)
}

func decodeBoundedRequestJSON(reader io.Reader, value any) error {
	limited := &io.LimitedReader{R: reader, N: maxJSONRequestBytes + 1}
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(value); err != nil {
		if limited.N <= 0 {
			return errors.New("JSON request body is too large")
		}
		return err
	}
	if limited.N <= 0 {
		return errors.New("JSON request body is too large")
	}
	return nil
}

func numberValue(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	default:
		return 0, false
	}
}
