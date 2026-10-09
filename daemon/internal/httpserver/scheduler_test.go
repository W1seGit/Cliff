package httpserver

import (
	"testing"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func TestScheduledSnapshotDue(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }

	tests := []struct {
		name   string
		server store.Server
		want   bool
	}{
		{"no previous snapshot", store.Server{ScheduledSnapshotsEnabled: true, SnapshotIntervalMinutes: 60}, true},
		{"recent snapshot", store.Server{ScheduledSnapshotsEnabled: true, SnapshotIntervalMinutes: 60, LastScheduledSnapshotAt: ago(30 * time.Minute)}, false},
		{"old snapshot", store.Server{ScheduledSnapshotsEnabled: true, SnapshotIntervalMinutes: 60, LastScheduledSnapshotAt: ago(61 * time.Minute)}, true},
		{"scheduler disabled", store.Server{ScheduledSnapshotsEnabled: false, SnapshotIntervalMinutes: 60}, false},
		{"zero interval", store.Server{ScheduledSnapshotsEnabled: true, SnapshotIntervalMinutes: 0}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := scheduledSnapshotDue(tc.server, now); got != tc.want {
				t.Fatalf("scheduledSnapshotDue = %v, want %v", got, tc.want)
			}
		})
	}
}
