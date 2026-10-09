package main

import (
	"os"
	"path/filepath"
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
	touch(t, filepath.Join(dir, "package-manifest.json"), "{}")
	if !isCliffInstall(dir) {
		t.Fatal("a folder with package-manifest.json is a Cliff install")
	}
}

func TestPathInside(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"the folder itself", root, true},
		{"a child", filepath.Join(root, "data"), true},
		{"a grandchild", filepath.Join(root, "a", "b"), true},
		{"a sibling", filepath.Join(filepath.Dir(root), "x"), false},
		{"the parent", filepath.Dir(root), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pathInside(tc.path, root); got != tc.want {
				t.Fatalf("pathInside(%q, %q) = %v, want %v", tc.path, root, got, tc.want)
			}
		})
	}
}

func TestEntryHoldsKeptPath(t *testing.T) {
	root := t.TempDir()
	keep := []string{filepath.Join(root, "data"), filepath.Join(root, "servers", "world1")}
	tests := []struct {
		name  string
		entry string
		want  bool
	}{
		{"a kept folder itself", "data", true},
		{"a folder that contains a kept path", "servers", true},
		{"an unrelated folder", "web", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := entryHoldsKeptPath(filepath.Join(root, tc.entry), keep); got != tc.want {
				t.Fatalf("entryHoldsKeptPath(%q) = %v, want %v", tc.entry, got, tc.want)
			}
		})
	}
}

func TestRemoveInstallEntriesKeepsData(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"data", "servers", "web"} {
		touch(t, filepath.Join(root, name, "file.txt"), "x")
	}
	touch(t, filepath.Join(root, "cliff"), "bin")
	failures := removeInstallEntries(root, filepath.Join(root, "cliff"), []string{filepath.Join(root, "data"), filepath.Join(root, "servers")})
	if failures != 0 {
		t.Fatalf("removeInstallEntries reported %d failures, want 0", failures)
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

func TestCountServerFolders(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "alpha"))
	mustMkdir(t, filepath.Join(dir, "beta"))
	touch(t, filepath.Join(dir, "stray.txt"), "x")
	if got := countServerFolders(dir); got != 2 {
		t.Fatalf("countServerFolders = %d, want 2 (only folders count as servers)", got)
	}
	if got := countServerFolders(filepath.Join(dir, "missing")); got != 0 {
		t.Fatalf("countServerFolders on a missing folder = %d, want 0", got)
	}
}
