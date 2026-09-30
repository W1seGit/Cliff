package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMemoryLimitMB = 256
	memoryCheckInterval  = 30 * time.Second
	// While memory stays high, repeat the warning at most this often.
	memoryWarnRepeat = 10 * time.Minute
	// Keep the newest crash reports and drop older ones.
	crashReportsKept = 5
)

// memoryLimitBytes reads CLIFF_MEMORY_LIMIT_MB. 0 turns the limit off. A
// GOMEMLIMIT set by the user wins, because it is the standard Go setting.
func memoryLimitBytes() int64 {
	if os.Getenv("GOMEMLIMIT") != "" {
		return debug.SetMemoryLimit(-1)
	}
	limitMB := int64(defaultMemoryLimitMB)
	if raw := strings.TrimSpace(os.Getenv("CLIFF_MEMORY_LIMIT_MB")); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed >= 0 {
			limitMB = parsed
		}
	}
	if limitMB == 0 {
		return -1
	}
	return limitMB << 20
}

// memoryLevel says how close the daemon is to its limit.
type memoryLevel int

const (
	memoryNormal   memoryLevel = iota
	memoryHigh                 // 75% of the limit or more
	memoryCritical             // at or over the limit
)

func classifyMemory(inUse uint64, limit int64) memoryLevel {
	if limit <= 0 {
		return memoryNormal
	}
	switch {
	case int64(inUse) >= limit:
		return memoryCritical
	case int64(inUse)*4 >= limit*3:
		return memoryHigh
	}
	return memoryNormal
}

func megabytes(bytes uint64) string { return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20)) }

// startRuntimeGuard applies the soft memory limit and logs when the daemon's
// own memory gets high. The limit covers the Go daemon, not the Minecraft
// servers it starts, which are separate processes.
func startRuntimeGuard(ctx context.Context) {
	limit := memoryLimitBytes()
	if limit > 0 {
		debug.SetMemoryLimit(limit)
		slog.Info("memory limit set", "limitMB", limit>>20, "note", "soft limit for the daemon only; CLIFF_MEMORY_LIMIT_MB=0 turns it off")
	}
	go func() {
		ticker := time.NewTicker(memoryCheckInterval)
		defer ticker.Stop()
		level := memoryNormal
		var lastWarn time.Time
		var peak uint64
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			// Memory the runtime holds and has not returned to the system.
			inUse := stats.Sys - stats.HeapReleased
			if inUse > peak {
				peak = inUse
			}
			next := classifyMemory(inUse, limit)
			attrs := []any{
				"inUse", megabytes(inUse), "heap", megabytes(stats.HeapAlloc), "peak", megabytes(peak),
				"goroutines", runtime.NumGoroutine(), "gcRuns", stats.NumGC,
			}
			if limit > 0 {
				attrs = append(attrs, "limit", megabytes(uint64(limit)))
			}
			switch {
			case next > memoryNormal && (next > level || time.Since(lastWarn) >= memoryWarnRepeat):
				msg := "daemon memory is high"
				if next == memoryCritical {
					msg = "daemon memory is at its limit; the garbage collector is working hard and Cliff may slow down"
				}
				slog.Warn(msg, attrs...)
				lastWarn = time.Now()
			case next == memoryNormal && level > memoryNormal:
				slog.Info("daemon memory is back to normal", attrs...)
			}
			level = next
		}
	}()
}

// startCrashReports makes Go write the trace of a fatal crash (a panic in any
// goroutine, a fatal runtime error) to logs/crash-<time>.log, next to the
// daemon log. The file is removed on a clean shutdown, so one left behind means
// the last run crashed. The returned function is the clean shutdown step.
func startCrashReports(logDir string) func() {
	reportPreviousCrashes(logDir)
	path := filepath.Join(logDir, "crash-"+time.Now().Format("20060102-150405")+".log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		slog.Warn("crash reports are unavailable", "error", err)
		return func() {}
	}
	if err := debug.SetCrashOutput(file, debug.CrashOptions{}); err != nil {
		slog.Warn("crash reports are unavailable", "error", err)
		_ = file.Close()
		_ = os.Remove(path)
		return func() {}
	}
	return func() {
		_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
		_ = file.Close()
		_ = os.Remove(path)
	}
}

// reportPreviousCrashes logs any crash report left by an earlier run and keeps
// only the newest few.
func reportPreviousCrashes(logDir string) {
	matches, _ := filepath.Glob(filepath.Join(logDir, "crash-*.log"))
	sort.Strings(matches)
	var reports []string
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.Size() == 0 {
			_ = os.Remove(path)
			continue
		}
		reports = append(reports, path)
	}
	for i, path := range reports {
		if i < len(reports)-crashReportsKept {
			_ = os.Remove(path)
			continue
		}
		slog.Warn("the previous run crashed; the report was kept", "report", path)
	}
}
