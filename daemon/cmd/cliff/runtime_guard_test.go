package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClassifyMemory(t *testing.T) {
	const limit = 256 << 20
	tests := []struct {
		inUse uint64
		want  memoryLevel
	}{
		{50 << 20, memoryNormal},
		{191 << 20, memoryNormal},
		{192 << 20, memoryHigh},
		{255 << 20, memoryHigh},
		{256 << 20, memoryCritical},
	}
	for _, tc := range tests {
		if got := classifyMemory(tc.inUse, limit); got != tc.want {
			t.Errorf("classifyMemory(%d MB of %d MB) = %v, want %v", tc.inUse>>20, limit>>20, got, tc.want)
		}
	}
	if got := classifyMemory(1<<40, -1); got != memoryNormal {
		t.Errorf("with no limit, classifyMemory = %v, want %v (never high)", got, memoryNormal)
	}
}

func TestMemoryLimitBytes(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  int64
	}{
		{"unset uses the default", "", defaultMemoryLimitMB << 20},
		{"custom megabytes", "512", 512 << 20},
		{"zero turns the limit off", "0", -1},
		{"junk falls back to the default", "junk", defaultMemoryLimitMB << 20},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GOMEMLIMIT", "")
			t.Setenv("CLIFF_MEMORY_LIMIT_MB", tc.value)
			if got := memoryLimitBytes(); got != tc.want {
				t.Fatalf("CLIFF_MEMORY_LIMIT_MB=%q: memoryLimitBytes() = %d, want %d", tc.value, got, tc.want)
			}
		})
	}
}

func TestReportPreviousCrashesKeepsNewestAndDropsEmpty(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"crash-1.log", "crash-2.log", "crash-3.log", "crash-4.log", "crash-5.log", "crash-6.log", "crash-7.log"} {
		touch(t, filepath.Join(dir, n), "goroutine 1")
	}
	touch(t, filepath.Join(dir, "crash-0empty.log"), "")
	reportPreviousCrashes(dir)
	left, _ := filepath.Glob(filepath.Join(dir, "crash-*.log"))
	if len(left) != crashReportsKept {
		t.Fatalf("keep %d reports, have %d: %v", crashReportsKept, len(left), left)
	}
	if _, err := os.Stat(filepath.Join(dir, "crash-7.log")); err != nil {
		t.Fatal("the newest report must stay")
	}
}
