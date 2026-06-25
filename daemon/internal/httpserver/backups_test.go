package httpserver

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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
	if err := os.WriteFile(filepath.Join(outside, "large.bin"), []byte(strings.Repeat("x", 1024*1024)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "local.txt"), []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(root, "outside-link")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlink creation is not available: %v", err)
	}

	size := directorySize(root)
	if size >= 1024*1024 {
		t.Fatalf("directorySize followed symlinked directory outside root, got %d bytes", size)
	}
}
