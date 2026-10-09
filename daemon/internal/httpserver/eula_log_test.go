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

func TestRequireEULAReadsTheFile(t *testing.T) {
	dir := t.TempDir()
	server := store.Server{ID: "srv_eula", Name: "Eula World", Path: dir}

	cases := []struct {
		name    string
		content string
		write   bool
		allowed bool
	}{
		{"file is missing", "", false, false},
		{"eula=false", "eula=false\n", true, false},
		{"eula=true", "eula=true\n", true, true},
		{"upper case value", "#By changing the setting below\nEULA=TRUE\n", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, "eula.txt")
			_ = os.Remove(path)
			if tc.write {
				touch(t, path, tc.content)
			}
			recorder := httptest.NewRecorder()
			if got := requireEULA(recorder, server); got != tc.allowed {
				t.Fatalf("requireEULA = %v, want %v", got, tc.allowed)
			}
			if tc.allowed {
				if recorder.Body.Len() != 0 {
					t.Fatalf("nothing should be written when the EULA is accepted, got %q", recorder.Body.String())
				}
				return
			}
			if recorder.Code != http.StatusConflict {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusConflict)
			}
			var body map[string]string
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["code"] != "eula_required" || body["error"] == "" {
				t.Fatalf("the dashboard needs the eula_required code and a message, got %v", body)
			}
		})
	}
}

func logged(t *testing.T, method string, path string, status int, body string) string {
	t.Helper()
	buffer := captureLogs(t)
	handler := withErrorLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, path, nil))
	return buffer.String()
}

func TestRequestLogShowsChangesAndFailuresWithReasons(t *testing.T) {
	if out := logged(t, http.MethodPut, "/api/servers/srv_1/properties", 200, "{}"); !strings.Contains(out, "level=INFO") || !strings.Contains(out, "PUT") {
		t.Fatalf("a change made through the API should be logged at info, got %q", out)
	}
	out := logged(t, http.MethodPost, "/api/servers/srv_1/start", 400, `{"error":"server exited during startup"}`)
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "server exited during startup") {
		t.Fatalf("a refused request should be logged with its reason, got %q", out)
	}
	out = logged(t, http.MethodPost, "/api/servers/srv_1/backups", 500, `{"error":"disk full"}`)
	if !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "disk full") {
		t.Fatalf("a failed request should be logged as an error with its reason, got %q", out)
	}
}

func TestRequestLogStaysQuietForRoutineReads(t *testing.T) {
	infoOnly := func(out string) bool {
		return strings.Contains(out, "level=INFO") || strings.Contains(out, "level=WARN") || strings.Contains(out, "level=ERROR")
	}
	for _, tc := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/api/servers", 200},
		{http.MethodGet, "/api/auth/me", 401},
		{http.MethodGet, "/api/servers/srv_1/files", 404},
		{http.MethodGet, "/_next/static/app.js", 200},
	} {
		if out := logged(t, tc.method, tc.path, tc.status, "{}"); infoOnly(out) {
			t.Fatalf("%s %s %d should not appear at info level, got %q", tc.method, tc.path, tc.status, out)
		}
	}
}

func TestStatusRecorderKeepsOnlyTheStartOfAnErrorBody(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusBadRequest}
	_, _ = rec.Write([]byte(strings.Repeat("x", 1000)))
	if len(rec.errBody) != 300 {
		t.Fatalf("expected 300 bytes kept, got %d", len(rec.errBody))
	}
	ok := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
	_, _ = ok.Write([]byte("fine"))
	if len(ok.errBody) != 0 {
		t.Fatal("successful responses must not be captured")
	}
}
