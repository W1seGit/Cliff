package httpserver

import (
	"context"
	"log/slog"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

const schedulerInterval = time.Minute

func (h apiHandler) runScheduler(ctx context.Context) {
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			h.runScheduledSnapshots(ctx, time.Now().UTC())
			timer.Reset(schedulerInterval)
		}
	}
}

func (h apiHandler) runScheduledSnapshots(ctx context.Context, now time.Time) {
	servers, err := h.store.ListServers(ctx)
	if err != nil {
		slog.Warn("scheduled snapshot scan failed", "error", err)
		return
	}
	for _, server := range servers {
		if !scheduledSnapshotDue(server, now) {
			continue
		}
		if h.process.IsRunning(server.ID) {
			continue
		}
		if _, err := h.createBackup(ctx, server, "scheduled snapshot"); err != nil {
			slog.Warn("scheduled snapshot failed", "server", server.ID, "error", err)
			continue
		}
		if err := h.store.MarkScheduledSnapshot(ctx, server.ID, now); err != nil {
			slog.Warn("scheduled snapshot timestamp update failed", "server", server.ID, "error", err)
		}
	}
}

func scheduledSnapshotDue(server store.Server, now time.Time) bool {
	if !server.SnapshotsEnabled || !server.ScheduledSnapshotsEnabled || server.SnapshotIntervalMinutes <= 0 {
		return false
	}
	lastRun, err := time.Parse(time.RFC3339, server.LastScheduledSnapshotAt)
	if server.LastScheduledSnapshotAt == "" || err != nil {
		return true
	}
	return !lastRun.Add(time.Duration(server.SnapshotIntervalMinutes) * time.Minute).After(now)
}
