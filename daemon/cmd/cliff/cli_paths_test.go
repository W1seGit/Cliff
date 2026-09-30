package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSavedPathsRoundTripAndFallbacks(t *testing.T) {
	root := t.TempDir()

	if got := dataDirFallback(root); got != filepath.Join(root, "data") {
		t.Fatalf("with nothing saved the data folder is <install>/data, got %s", got)
	}
	if got := serverRootFallback(root); got != filepath.Join(root, "servers") {
		t.Fatalf("with nothing saved the servers folder is <install>/servers, got %s", got)
	}

	custom := savedPaths{DataDir: filepath.Join(root, "elsewhere", "data"), ServerRoot: filepath.Join(root, "elsewhere", "servers")}
	if err := writeSavedPaths(root, custom); err != nil {
		t.Fatal(err)
	}
	if got := readSavedPaths(root); got != custom {
		t.Fatalf("saved paths should round-trip, got %#v", got)
	}
	if dataDirFallback(root) != custom.DataDir || serverRootFallback(root) != custom.ServerRoot {
		t.Fatal("saved folders must replace the defaults")
	}

	// Saving only one folder leaves the other at its default.
	if err := writeSavedPaths(root, savedPaths{ServerRoot: custom.ServerRoot}); err != nil {
		t.Fatal(err)
	}
	if dataDirFallback(root) != filepath.Join(root, "data") || serverRootFallback(root) != custom.ServerRoot {
		t.Fatal("an unset folder keeps its default")
	}

	// Clearing removes the file.
	if err := writeSavedPaths(root, savedPaths{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, pathsFileName)); err == nil {
		t.Fatal("empty settings should remove the file")
	}
	if err := writeSavedPaths(root, savedPaths{}); err != nil {
		t.Fatalf("clearing twice is fine: %v", err)
	}
}

func TestReadSavedPathsIgnoresABrokenFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, pathsFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readSavedPaths(root); got != (savedPaths{}) {
		t.Fatalf("a broken file must fall back to the defaults, got %#v", got)
	}
}

func TestHasExistingData(t *testing.T) {
	dir := t.TempDir()
	if hasExistingData(dir) {
		t.Fatal("an empty folder has no Cliff data")
	}
	if err := os.WriteFile(filepath.Join(dir, "dashboard.sqlite"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !hasExistingData(dir) {
		t.Fatal("a folder with dashboard.sqlite holds Cliff data")
	}
}

func TestCountServerFolders(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := countServerFolders(dir); got != 2 {
		t.Fatalf("only folders count as servers, got %d", got)
	}
	if got := countServerFolders(filepath.Join(dir, "missing")); got != 0 {
		t.Fatalf("a missing folder has no servers, got %d", got)
	}
}
