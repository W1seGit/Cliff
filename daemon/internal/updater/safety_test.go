package updater

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSwapKeepsThePreviousVersionForRollback(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "cliff")
	web := filepath.Join(root, "web")
	writeFile(t, binary, "old binary")
	writeFile(t, filepath.Join(web, "index.html"), "old web")

	staged := t.TempDir()
	newBinary := filepath.Join(staged, "cliff")
	newWeb := filepath.Join(staged, "web")
	writeFile(t, newBinary, "new binary")
	writeFile(t, filepath.Join(newWeb, "index.html"), "new web")

	manager := &Manager{binaryPath: binary, webDir: web}
	if err := manager.swapAssets(newBinary, newWeb); err != nil {
		t.Fatalf("swap failed: %v", err)
	}
	if readFile(t, binary) != "new binary" || readFile(t, filepath.Join(web, "index.html")) != "new web" {
		t.Fatal("the new version must be in place")
	}
	if readFile(t, binary+previousSuffix) != "old binary" {
		t.Fatal("the old binary must be kept as .previous")
	}
	if readFile(t, filepath.Join(web+previousSuffix, "index.html")) != "old web" {
		t.Fatal("the old dashboard files must be kept as .previous")
	}
}

func TestRollbackRestoresThePreviousVersion(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "cliff")
	web := filepath.Join(root, "web")
	writeFile(t, binary, "new binary")
	writeFile(t, filepath.Join(web, "index.html"), "new web")
	writeFile(t, binary+previousSuffix, "old binary")
	writeFile(t, filepath.Join(web+previousSuffix, "index.html"), "old web")
	writeFile(t, filepath.Join(root, "data", "keep.txt"), "user data")

	restored, err := Rollback(binary, web)
	if err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	if len(restored) != 2 {
		t.Fatalf("expected the program and dashboard restored, got %v", restored)
	}
	if readFile(t, binary) != "old binary" || readFile(t, filepath.Join(web, "index.html")) != "old web" {
		t.Fatal("the previous version must be back in place")
	}
	if readFile(t, filepath.Join(root, "data", "keep.txt")) != "user data" {
		t.Fatal("user data must never be touched")
	}
}

func TestRollbackWithoutAPreviousVersionExplainsWhy(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "cliff")
	writeFile(t, binary, "only version")
	if _, err := Rollback(binary, filepath.Join(root, "web")); err == nil {
		t.Fatal("expected an error when there is nothing to go back to")
	}
	if readFile(t, binary) != "only version" {
		t.Fatal("a failed rollback must leave the program alone")
	}
}

func TestPruneBackupsKeepsTheNewest(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for index, name := range []string{"a.sqlite", "b.sqlite", "c.sqlite", "d.sqlite", "e.sqlite"} {
		path := filepath.Join(dir, name)
		writeFile(t, path, name)
		// a is oldest, e is newest
		stamp := now.Add(time.Duration(index-10) * time.Minute)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	PruneBackups(dir, 3)
	for _, name := range []string{"c.sqlite", "d.sqlite", "e.sqlite"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s should have been kept", name)
		}
	}
	for _, name := range []string{"a.sqlite", "b.sqlite"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Fatalf("%s should have been pruned", name)
		}
	}
}

func TestPreUpdateBackupPathIsSafeAndNamedAfterTheVersion(t *testing.T) {
	path := PreUpdateBackupPath(filepath.Join("data"), "v0.2.1")
	if filepath.Dir(path) != PreUpdateBackupDir("data") {
		t.Fatalf("backup should live in the pre-update folder, got %s", path)
	}
	if base := filepath.Base(path); len(base) < len("dashboard-v0.2.1-") || base[:len("dashboard-v0.2.1-")] != "dashboard-v0.2.1-" {
		t.Fatalf("unexpected backup name %s", base)
	}
}

func TestCopyDatabaseIgnoresAMissingSource(t *testing.T) {
	dir := t.TempDir()
	if err := CopyDatabase(filepath.Join(dir, "missing.sqlite"), filepath.Join(dir, "out", "copy.sqlite")); err != nil {
		t.Fatalf("a missing database (a fresh install) is not an error: %v", err)
	}
	writeFile(t, filepath.Join(dir, "real.sqlite"), "data")
	if err := CopyDatabase(filepath.Join(dir, "real.sqlite"), filepath.Join(dir, "out", "copy.sqlite")); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(dir, "out", "copy.sqlite")) != "data" {
		t.Fatal("the copy must match the original")
	}
}
