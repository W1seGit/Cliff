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
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{"default, for compatibility", "", true},
		{"logs=0", "?logs=0", false},
		{"logs=1", "?logs=1", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", "/api/servers/test/console"+tc.query, nil)
			if got := consoleIncludesLogs(request); got != tc.want {
				t.Fatalf("consoleIncludesLogs(%q) = %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}
