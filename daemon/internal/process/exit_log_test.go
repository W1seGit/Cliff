package process

import (
	"bytes"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestLastLogLinesJoinsTheEnd(t *testing.T) {
	lines := []string{"one", "", "  two  ", "three", "", "four"}
	if got := lastLogLines(lines, 3); got != "two | three | four" {
		t.Fatalf("unexpected tail %q", got)
	}
	if got := lastLogLines(nil, 5); got != "" {
		t.Fatalf("no lines should give an empty string, got %q", got)
	}
	if got := lastLogLines([]string{"a", "b"}, 10); got != "a | b" {
		t.Fatalf("fewer lines than asked should return all, got %q", got)
	}
}

func exitedProcess(t *testing.T, code string) *managedProcess {
	t.Helper()
	cmd := exec.Command("go", "version")
	_ = code
	if err := cmd.Run(); err != nil {
		t.Skip("go toolchain not available to create a finished process")
	}
	return &managedProcess{serverID: "srv_test", cmd: cmd, startedAt: time.Now().Add(-3 * time.Second)}
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buffer bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buffer
}

func TestLogExitDistinguishesRoutineFromTrouble(t *testing.T) {
	manager := NewManager(t.TempDir())

	stopped := exitedProcess(t, "0")
	stopped.stopRequested = true
	stopped.logs = []string{"Stopping the server"}
	out := captureLogs(t)
	manager.logExit(stopped, nil)
	if !strings.Contains(out.String(), "level=INFO") || !strings.Contains(out.String(), "server stopped") {
		t.Fatalf("a requested stop is routine, got %q", out.String())
	}

	failedStart := exitedProcess(t, "1")
	failedStart.logs = []string{"Starting Test", "You need to agree to the EULA in order to run the server."}
	out = captureLogs(t)
	manager.logExit(failedStart, nil)
	text := out.String()
	if !strings.Contains(text, "level=ERROR") || !strings.Contains(text, "before it finished starting") || !strings.Contains(text, "agree to the EULA") {
		t.Fatalf("a failed start should be an error that carries the server's last output, got %q", text)
	}

	crashed := exitedProcess(t, "1")
	crashed.readyAt = time.Now().Add(-time.Second)
	crashed.logs = []string{"Done (3s)!", "java.lang.OutOfMemoryError"}
	out = captureLogs(t)
	manager.logExit(crashed, nil)
	text = out.String()
	if !strings.Contains(text, "level=ERROR") || !strings.Contains(text, "unexpectedly") || !strings.Contains(text, "OutOfMemoryError") {
		t.Fatalf("a crash after startup should be an error with the last output, got %q", text)
	}
}
