package httpserver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyDirectoryRefusesToCopyIntoItself(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "server.properties"), "a=b\n")
	inside := filepath.Join(root, "servers", "copy")
	if err := copyDirectory(root, inside); err == nil {
		t.Fatal("copying a folder into a subfolder of itself must be refused")
	}
	if _, err := os.Stat(inside); err == nil {
		t.Fatal("nothing should have been created")
	}
	if err := copyDirectory(root, root); err == nil {
		t.Fatal("copying a folder onto itself must be refused")
	}
}

func TestCopyDirectoryStillCopiesToASibling(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	mustMkdir(t, filepath.Join(source, "world"))
	touch(t, filepath.Join(source, "world", "level.dat"), "x")
	target := filepath.Join(parent, "servers", "copy")
	if err := copyDirectory(source, target); err != nil {
		t.Fatalf("a normal copy must still work: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "world", "level.dat")); err != nil {
		t.Fatal("file should have been copied")
	}
	// a name that merely starts with the source name is not "inside"
	lookalike := filepath.Join(parent, "source-copy")
	if err := copyDirectory(source, lookalike); err != nil {
		t.Fatalf("a sibling that shares a prefix is fine: %v", err)
	}
}
