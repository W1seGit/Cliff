package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// serverPort returns the TCP port an httptest server listens on.
func serverPort(t *testing.T, server *httptest.Server) int {
	t.Helper()
	_, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatalf("parsing test server URL %q: %v", server.URL, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parsing test server port %q: %v", portText, err)
	}
	return port
}

// touch writes a file, creating parent folders as needed.
func touch(t *testing.T, path string, content string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

// isolateFromRealDaemon stops tests from finding a Cliff that happens to run on this machine.
func isolateFromRealDaemon(t *testing.T) {
	t.Helper()
	previous := defaultProbePorts
	defaultProbePorts = nil
	t.Cleanup(func() { defaultProbePorts = previous })
}

func healthServer(t *testing.T, pid int, daemon string) int {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"daemon": daemon, "self": map[string]any{"pid": pid}})
	}))
	t.Cleanup(server.Close)
	return serverPort(t, server)
}

// fakeDaemon answers the local-only endpoints the update relies on.
func fakeDaemon(t *testing.T, running []serverRef, results string) (port int, dataDir string, seen *[]string) {
	t.Helper()
	dataDir = t.TempDir()
	if err := writeShutdownToken(dataDir, "tok"); err != nil {
		t.Fatal(err)
	}
	calls := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Cliff-Token") != "tok" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/internal/running-servers":
			_ = json.NewEncoder(w).Encode(map[string]any{"servers": running})
		case "/api/internal/resume-servers":
			var body struct {
				ServerIDs []string `json:"serverIds"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			calls = append(calls, "ids="+strings.Join(body.ServerIDs, ","))
			_, _ = w.Write([]byte(results))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return serverPort(t, server), dataDir, &calls
}
