package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClassifyMemory(t *testing.T) {
	const limit = 256 << 20
	cases := []struct {
		inUse uint64
		want  memoryLevel
	}{
		{50 << 20, memoryNormal},
		{191 << 20, memoryNormal},
		{192 << 20, memoryHigh},
		{255 << 20, memoryHigh},
		{256 << 20, memoryCritical},
	}
	for _, c := range cases {
		if got := classifyMemory(c.inUse, limit); got != c.want {
			t.Errorf("%d MB: got %v want %v", c.inUse>>20, got, c.want)
		}
	}
	if classifyMemory(1<<40, -1) != memoryNormal {
		t.Error("no limit means never high")
	}
}

func TestMemoryLimitBytes(t *testing.T) {
	t.Setenv("GOMEMLIMIT", "")
	t.Setenv("CLIFF_MEMORY_LIMIT_MB", "")
	if got := memoryLimitBytes(); got != defaultMemoryLimitMB<<20 {
		t.Fatalf("default limit, got %d", got)
	}
	t.Setenv("CLIFF_MEMORY_LIMIT_MB", "512")
	if got := memoryLimitBytes(); got != 512<<20 {
		t.Fatalf("custom limit, got %d", got)
	}
	t.Setenv("CLIFF_MEMORY_LIMIT_MB", "0")
	if got := memoryLimitBytes(); got != -1 {
		t.Fatalf("0 turns the limit off, got %d", got)
	}
	t.Setenv("CLIFF_MEMORY_LIMIT_MB", "junk")
	if got := memoryLimitBytes(); got != defaultMemoryLimitMB<<20 {
		t.Fatalf("junk falls back to the default, got %d", got)
	}
}

func TestReportPreviousCrashesKeepsNewestAndDropsEmpty(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"crash-1.log", "crash-2.log", "crash-3.log", "crash-4.log", "crash-5.log", "crash-6.log", "crash-7.log"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("goroutine 1"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "crash-0empty.log"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	reportPreviousCrashes(dir)
	left, _ := filepath.Glob(filepath.Join(dir, "crash-*.log"))
	if len(left) != crashReportsKept {
		t.Fatalf("keep %d reports, have %d: %v", crashReportsKept, len(left), left)
	}
	if _, err := os.Stat(filepath.Join(dir, "crash-7.log")); err != nil {
		t.Fatal("the newest report must stay")
	}
}
