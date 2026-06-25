package httpserver

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func TestReadPlayerSessionsScansOnlyRecentBoundedLogs(t *testing.T) {
	serverDir := t.TempDir()
	logsDir := filepath.Join(serverDir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	baseTime := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	for index := 0; index < maxPlayerSessionLogFiles+3; index++ {
		name := fmt.Sprintf("2026-06-%02d-1.log", index+1)
		playerName := fmt.Sprintf("Player%02d", index)
		path := filepath.Join(logsDir, name)
		if err := os.WriteFile(path, []byte(fmt.Sprintf("[12:00:00] [Server thread/INFO]: %s[/192.168.0.%d:50000] logged in with entity id 1\n", playerName, index+1)), 0o644); err != nil {
			t.Fatal(err)
		}
		modTime := baseTime.Add(time.Duration(index) * time.Hour)
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}

	sessions := readPlayerSessions(store.Server{Path: serverDir})
	if len(sessions) != maxPlayerSessionLogFiles {
		t.Fatalf("expected only %d recent session logs to be scanned, got %d sessions: %#v", maxPlayerSessionLogFiles, len(sessions), sessions)
	}
	for _, session := range sessions {
		if session.Name == "Player00" || session.Name == "Player01" || session.Name == "Player02" {
			t.Fatalf("old log session %s should not be retained when log scan is bounded: %#v", session.Name, sessions)
		}
	}
}
