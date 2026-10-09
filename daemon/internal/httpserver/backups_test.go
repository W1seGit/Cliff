package httpserver

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/config"
	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func TestDirectorySizeCacheReusesFreshEntry(t *testing.T) {
	cache := &directorySizeCache{}
	computes := 0
	compute := func(string) int64 {
		computes++
		return 42
	}

	first := cache.get("server-a", time.Hour, compute)
	second := cache.get("server-a", time.Hour, compute)

	if first != 42 || second != 42 {
		t.Fatalf("expected cached size 42, got %d and %d", first, second)
	}
	if computes != 1 {
		t.Fatalf("expected one directory size computation, got %d", computes)
	}
}

func TestDirectorySizeCachePrunesExpiredEntriesAndStaysBounded(t *testing.T) {
	cache := &directorySizeCache{}
	cache.entries = map[string]directorySizeCacheEntry{}
	for index := 0; index < 10; index++ {
		cache.entries["expired-"+strconv.Itoa(index)] = directorySizeCacheEntry{size: int64(index), expiresAt: time.Now().Add(-time.Minute)}
	}
	for index := 0; index < maxDirectorySizeCacheEntries+50; index++ {
		cache.get("server-"+strconv.Itoa(index), time.Hour, func(string) int64 { return 1 })
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.entries) > maxDirectorySizeCacheEntries {
		t.Fatalf("expected cache to stay at or below %d entries, got %d", maxDirectorySizeCacheEntries, len(cache.entries))
	}
	for key := range cache.entries {
		if len(key) >= len("expired-") && key[:len("expired-")] == "expired-" {
			t.Fatalf("expired cache entry was retained: %s", key)
		}
	}
}

func TestDirectorySizeDoesNotFollowSymlinkedDirectories(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	touch(t, filepath.Join(outside, "large.bin"), strings.Repeat("x", 1024*1024))
	touch(t, filepath.Join(root, "local.txt"), "local")
	linkPath := filepath.Join(root, "outside-link")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlink creation is not available: %v", err)
	}

	size := directorySize(root)
	if size >= 1024*1024 {
		t.Fatalf("directorySize followed symlinked directory outside root, got %d bytes", size)
	}
}

func TestSmartBackupPathCategory(t *testing.T) {
	cases := map[string]string{
		"server.properties":           "config",
		"config/fabric-api.json":      "config",
		"mods/sodium.jar":             "content",
		"plugins/LuckPerms.jar":       "content",
		"world/region/r.0.0.mca":      "world",
		"world/playerdata/leo.dat":    "world",
		"world/datapacks/example.zip": "world",
		"README.md":                   "other",
	}
	for path, expected := range cases {
		if actual := backupPathCategory(path); actual != expected {
			t.Fatalf("expected %s to be %s, got %s", path, expected, actual)
		}
	}
}

func TestSafeJoinServerPathRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	unsafe := []string{
		"../server.properties",
		"world/../../server.properties",
		filepath.Join("world", "..", "..", "server.properties"),
	}
	for _, path := range unsafe {
		if _, err := safeJoinServerPath(root, path); err == nil {
			t.Fatalf("expected unsafe path to be rejected: %s", path)
		}
	}
	safePath, err := safeJoinServerPath(root, "world/region/r.0.0.mca")
	if err != nil {
		t.Fatalf("expected safe path to be accepted: %v", err)
	}
	if !strings.HasPrefix(safePath, root) {
		t.Fatalf("safe path escaped root: %s", safePath)
	}
}

func TestBuildSimpleLineDiff(t *testing.T) {
	lines := buildSimpleLineDiff(
		[]string{"server-port=25565", "motd=old", "online-mode=true"},
		[]string{"server-port=25565", "motd=new", "online-mode=true"},
	)
	kinds := []string{}
	for _, line := range lines {
		kinds = append(kinds, line.Type+":"+line.Text)
	}
	expected := []string{
		"context:server-port=25565",
		"removed:motd=old",
		"added:motd=new",
		"context:online-mode=true",
	}
	if strings.Join(kinds, "|") != strings.Join(expected, "|") {
		t.Fatalf("unexpected diff: %#v", kinds)
	}
}

func TestSmartBackupCreateDiffDownloadRestoreAndGC(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "data")
	mustMkdir(t, dataDir)
	serverDir := filepath.Join(root, "servers", "demo")
	touch(t, filepath.Join(serverDir, "server.properties"), "motd=old\nserver-port=25565\n")
	touch(t, filepath.Join(serverDir, "world", "region", "r.0.0.mca"), "region-a")
	writeZip(t, filepath.Join(serverDir, "mods", "example.jar"), map[string]string{
		"fabric.mod.json": `{"id":"example","name":"Example Mod","version":"1.0.0"}`,
	})

	db := openTestStoreAt(t, dataDir, root)
	server, err := db.CreateServer(ctx, store.Server{
		ID:               "srv_test",
		Name:             "Demo",
		Path:             serverDir,
		Type:             "fabric",
		MinecraftVersion: "1.21.1",
		LoaderVersion:    "0.16.0",
		JavaPath:         "java",
		MinMemoryMB:      512,
		MaxMemoryMB:      1024,
		Port:             25565,
		LaunchJar:        "server.jar",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := apiHandler{
		config: config.Config{ServerRoot: root, DataDir: dataDir},
		store:  db,
	}

	firstID, err := handler.createBackup(ctx, server, "base")
	if err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(serverDir, "server.properties"), "motd=new\nserver-port=25565\n")
	writeZip(t, filepath.Join(serverDir, "mods", "example.jar"), map[string]string{
		"fabric.mod.json": `{"id":"example","name":"Example Mod","version":"2.0.0"}`,
	})
	secondID, err := handler.createBackup(ctx, server, "changed")
	if err != nil {
		t.Fatal(err)
	}

	backups, err := db.ListBackups(ctx, server.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 2 {
		t.Fatalf("expected two backups, got %d", len(backups))
	}
	for index := range backups {
		handler.hydrateSmartBackup(&backups[index])
	}
	var latest store.Backup
	for _, backup := range backups {
		if backup.ID == secondID {
			latest = backup
			break
		}
	}
	if latest.ID == "" {
		t.Fatalf("expected backup %s in list", secondID)
	}
	if latest.Stats.ConfigChanges == 0 || latest.Stats.ContentChanges == 0 {
		t.Fatalf("expected config and content changes, got %#v", latest.Stats)
	}
	foundMetadata := false
	for _, change := range latest.Changes {
		if change.Path == "mods/example.jar" && change.DisplayName == "Example Mod" && change.OldVersion == "1.0.0" && change.NewVersion == "2.0.0" {
			foundMetadata = true
		}
	}
	if !foundMetadata {
		t.Fatalf("expected mod metadata in changes: %#v", latest.Changes)
	}

	diffRequest := httptest.NewRequest(http.MethodGet, "/api/servers/srv_test/backups/diff?backupId="+secondID+"&path=server.properties", nil)
	diffRequest.SetPathValue("id", server.ID)
	diffResponse := httptest.NewRecorder()
	handler.backupDiff(diffResponse, diffRequest)
	if diffResponse.Code != http.StatusOK {
		t.Fatalf("diff failed: %d %s", diffResponse.Code, diffResponse.Body.String())
	}
	var diffPayload struct {
		Lines []backupDiffLine `json:"lines"`
	}
	if err := json.Unmarshal(diffResponse.Body.Bytes(), &diffPayload); err != nil {
		t.Fatal(err)
	}
	if len(diffPayload.Lines) == 0 {
		t.Fatal("expected diff lines")
	}

	downloadResponse := httptest.NewRecorder()
	backup, ok, err := db.Backup(ctx, server.ID, secondID)
	if err != nil || !ok {
		t.Fatalf("backup lookup failed: %v", err)
	}
	handler.writeSmartBackupZip(downloadResponse, "demo.zip", backup)
	if downloadResponse.Code != http.StatusOK {
		t.Fatalf("download status = %d", downloadResponse.Code)
	}
	zipReader, err := zip.NewReader(bytes.NewReader(downloadResponse.Body.Bytes()), int64(downloadResponse.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	foundProperties := false
	for _, file := range zipReader.File {
		if file.Name == "server.properties" {
			foundProperties = true
			break
		}
	}
	if !foundProperties {
		t.Fatal("download zip did not include server.properties")
	}

	touch(t, filepath.Join(serverDir, "server.properties"), "motd=broken\n")
	restoreRequest := httptest.NewRequest(http.MethodPost, "/api/servers/srv_test/backups", nil)
	if err := handler.restoreBackup(restoreRequest, server, secondID); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(filepath.Join(serverDir, "server.properties"))
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != "motd=new\nserver-port=25565\n" {
		t.Fatalf("unexpected restored content: %q", restored)
	}
	if err := handler.deleteBackup(restoreRequest, server, firstID); err != nil {
		t.Fatal(err)
	}
	if err := handler.collectSmartBackupGarbage(ctx, server.ID); err != nil {
		t.Fatal(err)
	}
}
