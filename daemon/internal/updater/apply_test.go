package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The test binary doubles as the "new version" of Cliff: when asked for its
// version it prints whatever the test says, which is all the updater's
// start-up check needs.
func TestMain(m *testing.M) {
	if output := os.Getenv("CLIFF_FAKE_VERSION_OUTPUT"); output != "" && len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(output)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type releaseServer struct {
	url      string
	archive  []byte
	checksum string
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "cliff.exe"
	}
	return "cliff"
}

// startReleaseServer publishes a fake release "9.9.9" and points the updater at it.
func startReleaseServer(t *testing.T, checksumOverride string) *releaseServer {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range map[string][]byte{
		"cliff/" + binaryName():       exe,
		"cliff/web/index.html":        []byte("new web"),
		"cliff/package-manifest.json": []byte("{}"),
	} {
		entry, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, ExternalAttrs: 0o755 << 16})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buffer.Bytes())
	release := &releaseServer{archive: buffer.Bytes(), checksum: hex.EncodeToString(sum[:])}
	if checksumOverride != "" {
		release.checksum = checksumOverride
	}
	archiveName := "cliff-9.9.9-" + runtime.GOOS + "-" + runtime.GOARCH + ".zip"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "cliff-release.json"):
			_ = json.NewEncoder(w).Encode(ReleaseManifest{
				SchemaVersion: 1, Name: "cliff", Version: "9.9.9", Commit: "abc123", BuiltAt: "2026-01-01T00:00:00Z",
				Platforms: []PlatformAsset{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Archive: archiveName, SizeBytes: int64(len(release.archive)), SHA256: release.checksum}},
			})
		case strings.HasSuffix(r.URL.Path, archiveName):
			_, _ = io.Copy(w, bytes.NewReader(release.archive))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	release.url = server.URL
	t.Setenv("CLIFF_UPDATE_BASE_URL", server.URL)
	t.Setenv("CLIFF_FAKE_VERSION_OUTPUT", "cliff 9.9.9 commit abc123 built now")
	return release
}

type installation struct {
	binary string
	web    string
	data   string
}

func newInstallation(t *testing.T) installation {
	t.Helper()
	root := t.TempDir()
	in := installation{binary: filepath.Join(root, binaryName()), web: filepath.Join(root, "web"), data: filepath.Join(root, "data")}
	writeFile(t, in.binary, "old binary")
	writeFile(t, filepath.Join(in.web, "index.html"), "old web")
	writeFile(t, filepath.Join(in.data, "keep.txt"), "user data")
	return in
}

func (in installation) unchanged(t *testing.T) {
	t.Helper()
	if readFile(t, in.binary) != "old binary" || readFile(t, filepath.Join(in.web, "index.html")) != "old web" {
		t.Fatal("a failed update must leave the installed version exactly as it was")
	}
	if _, err := os.Stat(in.binary + previousSuffix); err == nil {
		t.Fatal("no previous copy should exist when nothing was installed")
	}
	if readFile(t, filepath.Join(in.data, "keep.txt")) != "user data" {
		t.Fatal("user data must never be touched")
	}
}

func TestApplyGoodUpdateReportsEveryStepAndKeepsThePreviousVersion(t *testing.T) {
	startReleaseServer(t, "")
	in := newInstallation(t)
	manager := NewManager(in.binary, in.web, in.data)

	stages := []string{}
	manager.SetStageHook(func(stage string, _ string) { stages = append(stages, stage) })
	hookCalled := false

	result, err := manager.Apply(context.Background(), ApplyHooks{BeforeSwap: func(context.Context) error {
		hookCalled = true
		// At this point nothing has been replaced yet.
		if readFile(t, in.binary) != "old binary" {
			t.Fatal("the swap must not happen before the hook")
		}
		manager.SetStage(StageBackup, "backing up")
		manager.SetStage(StageStopping, "stopping")
		return nil
	}})
	if err != nil || !result.Success || result.NewVersion != "9.9.9" {
		t.Fatalf("update failed: %#v (err=%v)", result, err)
	}
	if !hookCalled {
		t.Fatal("BeforeSwap must run")
	}

	want := []string{StageDownloading, StageVerifying, StageChecking, StageBackup, StageStopping, StageInstalling, StageRestarting}
	if strings.Join(stages, ",") != strings.Join(want, ",") {
		t.Fatalf("steps were %v, want %v", stages, want)
	}
	if manager.Progress().Stage != StageRestarting {
		t.Fatalf("the last step should be restarting, got %s", manager.Progress().Stage)
	}

	if readFile(t, filepath.Join(in.web, "index.html")) != "new web" {
		t.Fatal("the new dashboard files must be in place")
	}
	if readFile(t, in.binary+previousSuffix) != "old binary" {
		t.Fatal("the old program must be kept for rollback")
	}
	if readFile(t, filepath.Join(in.web+previousSuffix, "index.html")) != "old web" {
		t.Fatal("the old dashboard files must be kept for rollback")
	}
	if readFile(t, filepath.Join(in.data, "keep.txt")) != "user data" {
		t.Fatal("user data must never be touched")
	}
	if !manager.SafetyInfo().CanRollback {
		t.Fatal("rollback must be available after an update")
	}
}

func TestApplyRejectsABadChecksumBeforeChangingAnything(t *testing.T) {
	startReleaseServer(t, strings.Repeat("0", 64))
	in := newInstallation(t)
	manager := NewManager(in.binary, in.web, in.data)
	hookCalled := false

	_, err := manager.Apply(context.Background(), ApplyHooks{BeforeSwap: func(context.Context) error { hookCalled = true; return nil }})
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected a checksum failure, got %v", err)
	}
	if hookCalled {
		t.Fatal("servers must not be stopped for a download that failed its checksum")
	}
	if manager.Progress().Stage != StageFailed || manager.Progress().Active {
		t.Fatalf("progress should show the failure, got %#v", manager.Progress())
	}
	in.unchanged(t)
}

func TestApplyRejectsABuildThatFailsItsStartupCheck(t *testing.T) {
	startReleaseServer(t, "")
	// The new build claims a different version than the release says.
	t.Setenv("CLIFF_FAKE_VERSION_OUTPUT", "cliff 1.0.0 commit x built y")
	in := newInstallation(t)
	manager := NewManager(in.binary, in.web, in.data)
	hookCalled := false

	_, err := manager.Apply(context.Background(), ApplyHooks{BeforeSwap: func(context.Context) error { hookCalled = true; return nil }})
	if err == nil || !strings.Contains(err.Error(), "start-up check") {
		t.Fatalf("expected the start-up check to fail, got %v", err)
	}
	if hookCalled {
		t.Fatal("servers must not be stopped for a build that cannot start")
	}
	in.unchanged(t)
}

func TestApplyStopsWhenThePreparationStepFails(t *testing.T) {
	startReleaseServer(t, "")
	in := newInstallation(t)
	manager := NewManager(in.binary, in.web, in.data)

	_, err := manager.Apply(context.Background(), ApplyHooks{BeforeSwap: func(context.Context) error { return errors.New("database backup failed") }})
	if err == nil || !strings.Contains(err.Error(), "database backup failed") {
		t.Fatalf("expected the preparation error, got %v", err)
	}
	in.unchanged(t)
}

func TestSafetyInfoAndClear(t *testing.T) {
	in := newInstallation(t)
	writeFile(t, in.binary+previousSuffix, strings.Repeat("x", 1000))
	writeFile(t, filepath.Join(in.web+previousSuffix, "index.html"), strings.Repeat("y", 500))
	backups := PreUpdateBackupDir(in.data)
	writeFile(t, filepath.Join(backups, "a.sqlite"), strings.Repeat("d", 200))
	writeFile(t, filepath.Join(backups, "b.sqlite"), strings.Repeat("d", 300))

	manager := NewManager(in.binary, in.web, in.data)
	info := manager.SafetyInfo()
	if !info.CanRollback || info.PreviousVersionBytes != 1500 || info.BackupCount != 2 || info.BackupBytes != 500 || info.TotalBytes != 2000 {
		t.Fatalf("unexpected safety info: %#v", info)
	}

	freed, err := manager.ClearSafetyCopies()
	if err != nil || freed != 2000 {
		t.Fatalf("expected 2000 bytes freed, got %d (err=%v)", freed, err)
	}
	after := manager.SafetyInfo()
	if after.CanRollback || after.TotalBytes != 0 || after.BackupCount != 0 {
		t.Fatalf("everything should be gone, got %#v", after)
	}
	if readFile(t, in.binary) != "old binary" || readFile(t, filepath.Join(in.data, "keep.txt")) != "user data" {
		t.Fatal("cleanup must not touch the installed version or user data")
	}
}

func TestClearSafetyCopiesRefusesDuringAnUpdate(t *testing.T) {
	in := newInstallation(t)
	writeFile(t, in.binary+previousSuffix, "prev")
	manager := NewManager(in.binary, in.web, in.data)
	manager.mu.Lock()
	manager.applying = true
	manager.mu.Unlock()
	if _, err := manager.ClearSafetyCopies(); err == nil {
		t.Fatal("cleanup must not run while an update is applying")
	}
	if !fileExists(in.binary + previousSuffix) {
		t.Fatal("nothing should have been deleted")
	}
}

func TestUpdateBaseOverrideOnlyAcceptsLoopback(t *testing.T) {
	for value, want := range map[string]string{
		"http://127.0.0.1:9000":  "http://127.0.0.1:9000/",
		"http://localhost:9000/": "http://localhost:9000/",
		"https://127.0.0.1:9000": "",
		"http://evil.example":    "",
		"http://10.0.0.5:9000":   "",
		"":                       "",
	} {
		t.Setenv("CLIFF_UPDATE_BASE_URL", value)
		if got := updateBaseOverride(); got != want {
			t.Fatalf("override %q = %q, want %q", value, got, want)
		}
	}
}

func TestUpdateResultRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if ReadUpdateResult(dir) != nil {
		t.Fatal("no result should exist yet")
	}
	if err := WriteUpdateResult(dir, UpdateResult{Status: "rolled-back", From: "1.0.0", To: "1.1.0", Message: "went back"}); err != nil {
		t.Fatal(err)
	}
	result := ReadUpdateResult(dir)
	if result == nil || result.Status != "rolled-back" || result.To != "1.1.0" || result.At == "" {
		t.Fatalf("unexpected result: %#v", result)
	}
	ClearUpdateResult(dir)
	if ReadUpdateResult(dir) != nil {
		t.Fatal("dismissing must clear the result")
	}
}
