package httpserver

import (
	"testing"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func TestScheduledSnapshotDue(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	base := store.Server{SnapshotsEnabled: true, ScheduledSnapshotsEnabled: true, SnapshotIntervalMinutes: 60}

	if !scheduledSnapshotDue(base, now) {
		t.Fatal("expected server with no previous scheduled snapshot to be due")
	}

	recent := base
	recent.LastScheduledSnapshotAt = now.Add(-30 * time.Minute).Format(time.RFC3339)
	if scheduledSnapshotDue(recent, now) {
		t.Fatal("expected recent scheduled snapshot to not be due")
	}

	old := base
	old.LastScheduledSnapshotAt = now.Add(-61 * time.Minute).Format(time.RFC3339)
	if !scheduledSnapshotDue(old, now) {
		t.Fatal("expected old scheduled snapshot to be due")
	}

	disabled := base
	disabled.ScheduledSnapshotsEnabled = false
	if scheduledSnapshotDue(disabled, now) {
		t.Fatal("expected disabled scheduler to not be due")
	}

	autoSnapshotsDisabled := base
	autoSnapshotsDisabled.SnapshotsEnabled = false
	if scheduledSnapshotDue(autoSnapshotsDisabled, now) {
		t.Fatal("expected disabled auto snapshots to not be due")
	}

	noInterval := base
	noInterval.SnapshotIntervalMinutes = 0
	if scheduledSnapshotDue(noInterval, now) {
		t.Fatal("expected zero interval to not be due")
	}
}
