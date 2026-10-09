package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func TestProtectionReasonCoversWhatAServerNeeds(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "server.properties"), "level-name=Survival\n")
	items := serverProtections(store.Server{Path: dir, LaunchJar: "fabric-server-launch.jar"})

	protected := []string{
		"run.bat", "RUN.SH", "eula.txt", "server.properties", "user_jvm_args.txt",
		"server.jar", "paper-1.21.jar", "fabric-server-launch.jar",
		"libraries", "libraries/net/minecraft/server.jar", "versions/1.21/server.jar",
		"mods", "plugins", "Survival", "Survival_nether", "Survival_the_end",
	}
	for _, path := range protected {
		if protectionReason(items, path) == "" {
			t.Errorf("%q should be protected", path)
		}
	}
	free := []string{"", "notes.txt", "config/settings.toml", "mods/lithium.jar", "plugins/EssentialsX.jar", "logs/latest.log", "backup/world", "world", "world/level.dat", "Survival/level.dat", "world.zip", "docs/run.bat"}
	for _, path := range free {
		if reason := protectionReason(items, path); reason != "" {
			t.Errorf("%q should not be protected, got %q", path, reason)
		}
	}
}

func TestRenameManagedPath(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "notes.txt"), "x")
	touch(t, filepath.Join(root, "taken.txt"), "y")
	mustMkdir(t, filepath.Join(root, "docs"))

	renamed, err := renameManagedPath(root, filepath.Join(root, "notes.txt"), "readme.txt")
	if err != nil || renamed != "readme.txt" {
		t.Fatalf("rename failed: %q %v", renamed, err)
	}
	if _, err := os.Stat(filepath.Join(root, "readme.txt")); err != nil {
		t.Fatal("the file should be under its new name")
	}
	if _, err := renameManagedPath(root, filepath.Join(root, "readme.txt"), "taken.txt"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("an existing name must be refused, got %v", err)
	}
	for _, bad := range []string{"", "..", "a/b", `a\b`, "what?", "trailing.", "x "} {
		if _, err := renameManagedPath(root, filepath.Join(root, "readme.txt"), bad); err == nil {
			t.Errorf("name %q should be refused", bad)
		}
	}
	if _, err := renameManagedPath(root, root, "x"); err == nil {
		t.Fatal("the server folder itself cannot be renamed")
	}
	renamed, err = renameManagedPath(root, filepath.Join(root, "docs"), "documents")
	if err != nil || renamed != "documents" {
		t.Fatalf("folders can be renamed too: %q %v", renamed, err)
	}
}

func TestMoveManagedPaths(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "a.txt"), "a")
	touch(t, filepath.Join(root, "b.txt"), "b")
	touch(t, filepath.Join(root, "docs", "b.txt"), "existing")
	touch(t, filepath.Join(root, "tree", "inner", "f.txt"), "f")
	mustMkdir(t, filepath.Join(root, "archive"))
	mustMkdir(t, filepath.Join(root, "empty"))

	moved, err := moveManagedPaths(root, []string{"a.txt"}, "archive")
	if err != nil || len(moved) != 1 || moved[0] != "archive/a.txt" {
		t.Fatalf("move failed: %v %v", moved, err)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); err == nil {
		t.Fatal("the original must be gone")
	}

	// A conflict anywhere moves nothing.
	if _, err := moveManagedPaths(root, []string{"b.txt", "tree"}, "docs"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("a name clash must be refused, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "tree", "inner", "f.txt")); err != nil {
		t.Fatal("nothing should have moved when one item clashed")
	}

	if _, err := moveManagedPaths(root, []string{"tree"}, "tree/inner"); err == nil || !strings.Contains(err.Error(), "into itself") {
		t.Fatalf("a folder cannot move into itself, got %v", err)
	}
	if _, err := moveManagedPaths(root, []string{"docs/b.txt"}, "docs"); err == nil || !strings.Contains(err.Error(), "already in that folder") {
		t.Fatalf("moving into the same folder is pointless, got %v", err)
	}
	if _, err := moveManagedPaths(root, []string{"b.txt"}, "../outside"); err == nil {
		t.Fatal("the destination must stay inside the server")
	}
	if _, err := moveManagedPaths(root, []string{"b.txt"}, "missing"); err == nil {
		t.Fatal("the destination must exist")
	}
	if _, err := moveManagedPaths(root, []string{"b.txt"}, "empty"); err != nil {
		t.Fatalf("a normal move should work: %v", err)
	}
}

// newFilesFixture creates a store with one vanilla server whose folder holds
// the given files (name -> content).
func newFilesFixture(t *testing.T, files map[string]string) (apiHandler, store.Server, string) {
	t.Helper()
	dir := t.TempDir()
	db := openTestStoreAt(t, dir, filepath.Join(dir, "servers"))
	serverDir := filepath.Join(dir, "servers", "s1")
	for name, content := range files {
		touch(t, filepath.Join(serverDir, name), content)
	}
	server, err := db.CreateServer(context.Background(), store.Server{
		Name: "S1", Path: serverDir, Type: "vanilla", MinecraftVersion: "1.21.1", JavaPath: "java",
		MinMemoryMB: 512, MaxMemoryMB: 1024, Port: 25565, LaunchJar: "server.jar",
	})
	if err != nil {
		t.Fatal(err)
	}
	return apiHandler{store: db}, server, serverDir
}

func TestFileActionsHonourSafeMode(t *testing.T) {
	handler, server, serverDir := newFilesFixture(t, map[string]string{"run.bat": "@echo off", "notes.txt": "hello"})
	mustMkdir(t, filepath.Join(serverDir, "archive"))

	call := func(body string) (int, map[string]any) {
		request := httptest.NewRequest(http.MethodPost, "/api/servers/"+server.ID+"/files", strings.NewReader(body))
		request.SetPathValue("id", server.ID)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.fileAction(recorder, request)
		var payload map[string]any
		_ = json.Unmarshal(recorder.Body.Bytes(), &payload)
		return recorder.Code, payload
	}

	// Safe mode is the default: launcher scripts cannot be moved, renamed or deleted.
	for _, body := range []string{
		`{"action":"move","paths":["run.bat"],"destination":"archive"}`,
		`{"action":"rename","path":"run.bat","newName":"go.bat"}`,
		`{"action":"delete","path":"run.bat"}`,
		`{"action":"delete-selected","paths":["notes.txt","run.bat"]}`,
		`{"action":"move","paths":["run.bat"],"destination":"archive","safeMode":true}`,
	} {
		status, payload := call(body)
		if status != http.StatusForbidden || payload["code"] != "protected" {
			t.Fatalf("%s should be refused by Safe mode, got %d %v", body, status, payload)
		}
	}
	if _, err := os.Stat(filepath.Join(serverDir, "run.bat")); err != nil {
		t.Fatal("run.bat must still be there")
	}
	if _, err := os.Stat(filepath.Join(serverDir, "notes.txt")); err != nil {
		t.Fatal("a refused delete-selected must not delete the other items either")
	}

	// Ordinary files are unaffected.
	if status, payload := call(`{"action":"rename","path":"notes.txt","newName":"readme.txt"}`); status != http.StatusOK {
		t.Fatalf("renaming an ordinary file should work, got %d %v", status, payload)
	}
	if status, payload := call(`{"action":"move","paths":["readme.txt"],"destination":"archive"}`); status != http.StatusOK {
		t.Fatalf("moving an ordinary file should work, got %d %v", status, payload)
	}

	// With Safe mode off the protected file can be moved.
	if status, payload := call(`{"action":"move","paths":["run.bat"],"destination":"archive","safeMode":false}`); status != http.StatusOK {
		t.Fatalf("Safe mode off should allow the move, got %d %v", status, payload)
	}
	if _, err := os.Stat(filepath.Join(serverDir, "archive", "run.bat")); err != nil {
		t.Fatal("run.bat should have been moved")
	}
}

func TestFileListingMarksProtectedEntries(t *testing.T) {
	handler, server, _ := newFilesFixture(t, map[string]string{"run.bat": "x", "notes.txt": "x"})
	request := httptest.NewRequest(http.MethodGet, "/api/servers/"+server.ID+"/files", nil)
	request.SetPathValue("id", server.ID)
	recorder := httptest.NewRecorder()
	handler.files(recorder, request)
	var listing fileListing
	if err := json.Unmarshal(recorder.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	reasons := map[string]string{}
	for _, entry := range listing.Entries {
		reasons[entry.Name] = entry.Protected
	}
	if reasons["run.bat"] == "" || reasons["notes.txt"] != "" {
		t.Fatalf("run.bat should be marked protected and notes.txt not: %v", reasons)
	}
}
