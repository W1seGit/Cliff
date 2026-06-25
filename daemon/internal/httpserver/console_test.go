package httpserver

import (
	"net/http/httptest"
	"testing"
)

func TestConsoleStatusIntervalOnlyUsesFastCadenceForUsage(t *testing.T) {
	if got := consoleStatusInterval(true); got != consoleUsageStatusPeriod {
		t.Fatalf("usage console status interval = %s, want %s", got, consoleUsageStatusPeriod)
	}
	if got := consoleStatusInterval(false); got != consoleLightStatusPeriod {
		t.Fatalf("light console status interval = %s, want %s", got, consoleLightStatusPeriod)
	}
	if consoleLightStatusPeriod <= consoleUsageStatusPeriod {
		t.Fatalf("light console interval should be slower than usage interval: light=%s usage=%s", consoleLightStatusPeriod, consoleUsageStatusPeriod)
	}
}

func TestConsoleWriteDeadlineIsBounded(t *testing.T) {
	if consoleWriteWait <= 0 {
		t.Fatalf("console write deadline must be positive, got %s", consoleWriteWait)
	}
	if consoleWriteWait >= consolePongWait {
		t.Fatalf("console write deadline should be shorter than pong wait: write=%s pong=%s", consoleWriteWait, consolePongWait)
	}
}

func TestConsoleOutgoingQueueIsBounded(t *testing.T) {
	if consoleOutgoingQueueSize <= 0 {
		t.Fatalf("console outgoing queue must be positive, got %d", consoleOutgoingQueueSize)
	}
	if consoleOutgoingQueueSize > 32 {
		t.Fatalf("console outgoing queue should remain small and bounded, got %d", consoleOutgoingQueueSize)
	}
}

func TestConsoleLogStreamingCanBeDisabled(t *testing.T) {
	if !consoleIncludesLogs(httptest.NewRequest("GET", "/api/servers/test/console", nil)) {
		t.Fatal("console logs should be included by default for compatibility")
	}
	if consoleIncludesLogs(httptest.NewRequest("GET", "/api/servers/test/console?logs=0", nil)) {
		t.Fatal("console logs should be disabled when logs=0 is requested")
	}
	if !consoleIncludesLogs(httptest.NewRequest("GET", "/api/servers/test/console?logs=1", nil)) {
		t.Fatal("console logs should be included when logs=1 is requested")
	}
}
