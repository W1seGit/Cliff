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

func TestRemoveShellBlocksMarkedBlock(t *testing.T) {
	input := "export A=1\n\n# >>> cliff >>>\nexport PATH=\"$HOME/.local/bin:$PATH\"\n# <<< cliff <<<\nexport B=2\n"
	got, changed := removeShellBlocks(input)
	if !changed {
		t.Fatal("expected the marked block to be removed")
	}
	if strings.Contains(got, "cliff") || !strings.Contains(got, "export A=1") || !strings.Contains(got, "export B=2") {
		t.Fatalf("unexpected result: %q", got)
	}
}

func TestRemoveShellBlocksLegacyLines(t *testing.T) {
	input := "export A=1\n\n# Cliff CLI\nexport PATH=\"$HOME/.local/bin:$PATH\"\n"
	got, changed := removeShellBlocks(input)
	if !changed || strings.Contains(got, "Cliff CLI") || strings.Contains(got, ".local/bin") {
		t.Fatalf("legacy block should be removed, got %q (changed=%v)", got, changed)
	}
	if !strings.Contains(got, "export A=1") {
		t.Fatalf("unrelated lines must stay, got %q", got)
	}
}

func TestRemoveShellBlocksLeavesOtherPathLinesAlone(t *testing.T) {
	input := "export PATH=\"$HOME/.local/bin:$PATH\"\n# my notes\n"
	got, changed := removeShellBlocks(input)
	if changed || got != input {
		t.Fatalf("a user PATH line must not be touched, got %q (changed=%v)", got, changed)
	}
}

func TestIsCliffInstall(t *testing.T) {
	dir := t.TempDir()
	if isCliffInstall(dir) {
		t.Fatal("an empty folder is not a Cliff install")
	}
	if err := os.WriteFile(filepath.Join(dir, "package-manifest.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isCliffInstall(dir) {
		t.Fatal("a folder with package-manifest.json is a Cliff install")
	}
}

func TestPathInside(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	cases := map[string]bool{
		root:                                   true,
		filepath.Join(root, "data"):            true,
		filepath.Join(root, "a", "b"):          true,
		filepath.Join(filepath.Dir(root), "x"): false,
		filepath.Dir(root):                     false,
	}
	for path, want := range cases {
		if got := pathInside(path, root); got != want {
			t.Fatalf("pathInside(%q, %q) = %v, want %v", path, root, got, want)
		}
	}
}

func TestEntryHoldsKeptPath(t *testing.T) {
	root := t.TempDir()
	keep := []string{filepath.Join(root, "data"), filepath.Join(root, "servers", "world1")}
	if !entryHoldsKeptPath(filepath.Join(root, "data"), keep) {
		t.Fatal("data itself is kept")
	}
	if !entryHoldsKeptPath(filepath.Join(root, "servers"), keep) {
		t.Fatal("a folder that contains a kept path is kept")
	}
	if entryHoldsKeptPath(filepath.Join(root, "web"), keep) {
		t.Fatal("web is not kept")
	}
}

func TestRemoveInstallEntriesKeepsData(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"data", "servers", "web"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name, "file.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "cliff"), []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	failures := removeInstallEntries(root, filepath.Join(root, "cliff"), []string{filepath.Join(root, "data"), filepath.Join(root, "servers")})
	if failures != 0 {
		t.Fatalf("unexpected failures: %d", failures)
	}
	for _, name := range []string{"data", "servers"} {
		if _, err := os.Stat(filepath.Join(root, name, "file.txt")); err != nil {
			t.Fatalf("%s must be kept: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "web")); err == nil {
		t.Fatal("web should have been removed")
	}
}

func TestSafeToRemoveExternal(t *testing.T) {
	if safeToRemoveExternal(filepath.VolumeName(os.TempDir()) + string(filepath.Separator)) {
		t.Fatal("a drive root must never be removed")
	}
	if home := homeDir(); home != "" {
		if safeToRemoveExternal(home) || safeToRemoveExternal(filepath.Dir(home)) {
			t.Fatal("the home folder and its parents must never be removed")
		}
		if !safeToRemoveExternal(filepath.Join(home, "cliff-servers")) {
			t.Fatal("a folder inside home is fine")
		}
	}
}

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
	_, portText, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	port, _ := strconv.Atoi(portText)
	return port
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
	defer server.Close()
	_, portText, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	port, _ := strconv.Atoi(portText)

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
