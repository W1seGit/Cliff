package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
)

func TestTailLines(t *testing.T) {
	got := tailLines("a\r\nb\nc\nd\n\n", 2)
	if len(got) != 2 || got[0] != "c" || got[1] != "d" {
		t.Fatalf("unexpected tail: %#v", got)
	}
	if all := tailLines("a\nb\n", 0); len(all) != 2 {
		t.Fatalf("n <= 0 returns everything, got %#v", all)
	}
}

func TestProcessAliveForSelfAndInvalid(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("the current process is alive")
	}
	if processAlive(0) || processAlive(-5) {
		t.Fatal("invalid PIDs are not alive")
	}
}

func TestFindDaemonUsesHealthWhenFilesAreMissing(t *testing.T) {
	isolateFromRealDaemon(t)
	port := healthServer(t, os.Getpid(), "cliff")
	t.Setenv("CLIFF_PORT", strconv.Itoa(port))
	dataDir := t.TempDir()

	info := findDaemon(dataDir)
	if info == nil || info.PID != os.Getpid() || info.Port != port || !info.Responding {
		t.Fatalf("expected the daemon to be found by port, got %#v", info)
	}
	if byFiles := findDaemonByFiles(dataDir); byFiles != nil {
		t.Fatalf("file-only lookup must not use the port probe, got %#v", byFiles)
	}
}

func TestFindDaemonIgnoresOtherServices(t *testing.T) {
	isolateFromRealDaemon(t)
	port := healthServer(t, os.Getpid(), "not-cliff")
	t.Setenv("CLIFF_PORT", strconv.Itoa(port))
	if info := findDaemon(t.TempDir()); info != nil && info.Port == port {
		t.Fatalf("a non-Cliff service must not be treated as the daemon, got %#v", info)
	}
}

func TestFindDaemonClearsStaleFiles(t *testing.T) {
	isolateFromRealDaemon(t)
	dataDir := t.TempDir()
	t.Setenv("CLIFF_PORT", "1")
	// A state file that points at a dead PID and a closed port.
	writeState(dataDir, cliffState{PID: 999999, Port: 1})
	_ = os.WriteFile(pidFilePath(dataDir), []byte("999999\n"), 0o644)
	if info := findDaemon(dataDir); info != nil {
		t.Fatalf("nothing is running, got %#v", info)
	}
	if _, err := os.Stat(stateFilePath(dataDir)); err == nil {
		t.Fatal("stale state file should have been removed")
	}
	if _, err := os.Stat(pidFilePath(dataDir)); err == nil {
		t.Fatal("stale PID file should have been removed")
	}
}

func TestRequestGracefulShutdownSendsToken(t *testing.T) {
	dataDir := t.TempDir()
	if err := writeShutdownToken(dataDir, "abc123"); err != nil {
		t.Fatal(err)
	}
	gotToken := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Cliff-Token")
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)
	port := serverPort(t, server)

	if !requestGracefulShutdown(port, dataDir) {
		t.Fatal("expected the shutdown request to be accepted")
	}
	if gotToken != "abc123" {
		t.Fatalf("token was not sent, got %q", gotToken)
	}
	removeShutdownToken(dataDir)
	if requestGracefulShutdown(port, dataDir) {
		t.Fatal("without a token file there is nothing to send")
	}
}
