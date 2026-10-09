package process

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestManagerShutdownStopsRunningServer(t *testing.T) {
	t.Parallel()
	manager, server := startFakeServer(t)

	manager.Shutdown(2 * time.Second)
	waitForLifecycle(t, manager, server.ID, LifecycleStopped)

	logs := strings.Join(manager.Logs(server.ID), "\n")
	if !strings.Contains(logs, "Stop requested") {
		t.Fatalf("expected shutdown to request a clean stop, logs:\n%s", logs)
	}
	if !strings.Contains(logs, "stopped") {
		t.Fatalf("expected child process to receive stop command, logs:\n%s", logs)
	}
}

func TestManagerRestartWaitsForStopBeforeStart(t *testing.T) {
	t.Parallel()
	manager, server := startFakeServer(t)
	firstPID := manager.StatusFor(server.ID).PID

	status, err := manager.Restart(server, false, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if status.Lifecycle != LifecycleRunning {
		t.Fatalf("expected restarted process to be running after startup wait, got %s", status.Lifecycle)
	}
	waitForLifecycle(t, manager, server.ID, LifecycleRunning)
	restartedPID := manager.StatusFor(server.ID).PID
	if restartedPID == 0 || restartedPID == firstPID {
		t.Fatalf("expected restart to launch a new process, first pid=%d restarted pid=%d", firstPID, restartedPID)
	}
	manager.Shutdown(2 * time.Second)
	waitForLifecycle(t, manager, server.ID, LifecycleStopped)

	logs := strings.Join(manager.Logs(server.ID), "\n")
	if !strings.Contains(logs, "Starting Shutdown Test") {
		t.Fatalf("expected restarted process logs, logs:\n%s", logs)
	}
	if !strings.Contains(logs, "Stop requested") {
		t.Fatalf("expected final shutdown to request a clean stop, logs:\n%s", logs)
	}
}

func TestManagerStopAndWaitReturnsStoppedLifecycle(t *testing.T) {
	t.Parallel()
	manager, server := startFakeServer(t)

	status, err := manager.StopAndWait(server.ID, false, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if status.Lifecycle != LifecycleStopped {
		t.Fatalf("expected stopped lifecycle after stop wait, got %s", status.Lifecycle)
	}
	if manager.IsRunning(server.ID) {
		t.Fatal("server should not remain running after successful stop wait")
	}
}

func TestManagerStartFailsWhenServerExitsDuringStartup(t *testing.T) {
	tests := []struct {
		name       string
		writeFake  func(*testing.T, string) string
		wantInLogs []string
	}{
		{"exits immediately", writeFailingServerScript, []string{"boot failed"}},
		{"drains output before reporting stopped", writeChattyFailingServerScript, []string{"stdout-final-line", "stderr-final-line", "Server exited"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			manager := NewManager(t.TempDir())
			server := fakeServer(dir, tc.writeFake(t, dir))

			status, err := manager.Start(server)
			if err == nil {
				t.Fatal("expected startup failure, got nil error")
			}
			if status.Lifecycle != LifecycleStopped {
				t.Fatalf("lifecycle after failed startup = %s, want %s", status.Lifecycle, LifecycleStopped)
			}
			if manager.IsRunning(server.ID) {
				t.Fatal("failed startup should not leave server marked running")
			}
			logs := strings.Join(manager.Logs(server.ID), "\n")
			for _, want := range tc.wantInLogs {
				if !strings.Contains(logs, want) {
					t.Fatalf("expected retained log %q, logs:\n%s", want, logs)
				}
			}
		})
	}
}

func TestManagerStatusEventsIncludeFleetStatus(t *testing.T) {
	t.Parallel()
	manager, first := startFakeServer(t)
	events, unsubscribe := manager.Subscribe()
	defer unsubscribe()

	secondDir := t.TempDir()
	second := fakeServerWithID("srv_second", secondDir, writeFakeServerScript(t, secondDir))
	if _, err := manager.Start(second); err != nil {
		t.Fatal(err)
	}
	waitForLifecycle(t, manager, second.ID, LifecycleRunning)

	event := waitForStatusEvent(t, events, second.ID)
	if event.Status.Servers[first.ID].RunningServerID != first.ID {
		t.Fatalf("status event did not include first server runtime: %#v", event.Status.Servers)
	}
	if event.Status.Servers[second.ID].RunningServerID != second.ID {
		t.Fatalf("status event did not include second server runtime: %#v", event.Status.Servers)
	}
	if event.Status.Servers[second.ID].Usage != nil {
		t.Fatalf("status events should not collect usage by default: %#v", event.Status.Servers[second.ID].Usage)
	}
}

func TestManagerStatusLightSkipsUsageCollection(t *testing.T) {
	t.Parallel()
	manager, server := startFakeServer(t)

	light := manager.StatusLight()
	if light.Usage != nil {
		t.Fatalf("light fleet status should not include usage: %#v", light.Usage)
	}
	if light.Servers[server.ID].Usage != nil {
		t.Fatalf("light server status should not include usage: %#v", light.Servers[server.ID].Usage)
	}

	full := manager.Status()
	if full.Servers[server.ID].Usage == nil {
		t.Fatal("full server status should include usage")
	}
}

func TestManagerRetainsBoundedLogs(t *testing.T) {
	manager := NewManager(t.TempDir())
	proc := &managedProcess{serverID: "srv_logs"}
	manager.running[proc.serverID] = proc

	for i := 0; i < maxRetainedLogLines+25; i++ {
		manager.pushLog(proc, "line-"+strconv.Itoa(i))
	}

	logs := manager.Logs(proc.serverID)
	if len(logs) != maxRetainedLogLines {
		t.Fatalf("expected %d retained logs, got %d", maxRetainedLogLines, len(logs))
	}
	if logs[0] != "line-25" {
		t.Fatalf("expected oldest retained line to be line-25, got %q", logs[0])
	}
	if logs[len(logs)-1] != "line-1024" {
		t.Fatalf("expected newest retained line to be line-1024, got %q", logs[len(logs)-1])
	}

	if history := manager.history[proc.serverID]; len(history) != 0 {
		t.Fatalf("running log updates should not copy retained history on every line, got %d history lines", len(history))
	}

	manager.mu.Lock()
	manager.rememberLocked(proc.serverID, proc.logs)
	manager.mu.Unlock()

	history := manager.history[proc.serverID]
	if len(history) != maxRetainedLogLines {
		t.Fatalf("expected %d retained history lines, got %d", maxRetainedLogLines, len(history))
	}
	if history[0] != logs[0] || history[len(history)-1] != logs[len(logs)-1] {
		t.Fatalf("history does not match retained logs: first=%q/%q last=%q/%q", history[0], logs[0], history[len(history)-1], logs[len(logs)-1])
	}
}

func TestManagerTruncatesOversizedLogLines(t *testing.T) {
	manager := NewManager(t.TempDir())
	proc := &managedProcess{serverID: "srv_long_logs"}
	manager.running[proc.serverID] = proc

	longLine := strings.Repeat("x", maxRetainedLogLineBytes+500)
	manager.pushLog(proc, longLine)

	logs := manager.Logs(proc.serverID)
	if len(logs) != 1 {
		t.Fatalf("expected one retained log, got %#v", logs)
	}
	if len(logs[0]) != maxRetainedLogLineBytes {
		t.Fatalf("expected retained line to be capped at %d bytes, got %d", maxRetainedLogLineBytes, len(logs[0]))
	}
	if !strings.HasSuffix(logs[0], truncatedLogSuffix) {
		t.Fatalf("expected truncation suffix, got %q", logs[0])
	}
}

func TestManagerPublishesTruncatedLogLines(t *testing.T) {
	manager := NewManager(t.TempDir())
	proc := &managedProcess{serverID: "srv_long_events"}
	manager.running[proc.serverID] = proc
	events, unsubscribe := manager.SubscribeFor(proc.serverID, true)
	defer unsubscribe()

	manager.pushLog(proc, strings.Repeat("x", maxRetainedLogLineBytes+500))

	select {
	case event := <-events:
		if len(event.Line) != maxRetainedLogLineBytes {
			t.Fatalf("expected event line to be capped at %d bytes, got %d", maxRetainedLogLineBytes, len(event.Line))
		}
		if !strings.HasSuffix(event.Line, truncatedLogSuffix) {
			t.Fatalf("expected truncation suffix, got %q", event.Line)
		}
	default:
		t.Fatal("expected log event")
	}
}

func TestManagerForgetClearsRetainedLogs(t *testing.T) {
	manager := NewManager(t.TempDir())
	manager.history["srv_removed"] = []string{"old line"}

	manager.Forget("srv_removed")

	if logs := manager.Logs("srv_removed"); len(logs) != 0 {
		t.Fatalf("expected removed server logs to be forgotten, got %#v", logs)
	}
}

func TestManagerPublishDoesNotBlockSlowSubscriber(t *testing.T) {
	manager := NewManager(t.TempDir())
	events, unsubscribe := manager.Subscribe()
	defer unsubscribe()

	for i := 0; i < subscriberQueueSize; i++ {
		manager.publish(Event{Type: "log", ServerID: "srv_slow", Line: fmt.Sprintf("line-%d", i)})
	}

	done := make(chan struct{})
	go func() {
		manager.publish(Event{Type: "log", ServerID: "srv_slow", Line: "dropped-if-full"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("publish blocked behind a slow console subscriber")
	}

	if len(events) != subscriberQueueSize {
		t.Fatalf("expected full subscriber queue to remain bounded at %d, got %d", subscriberQueueSize, len(events))
	}
}

func TestManagerSubscribeForFiltersLogsAndServerEvents(t *testing.T) {
	manager := NewManager(t.TempDir())
	events, unsubscribe := manager.SubscribeFor("srv_selected", false)
	defer unsubscribe()

	manager.publish(Event{Type: "log", ServerID: "srv_selected", Line: "hidden"})
	manager.publish(Event{Type: "status", ServerID: "srv_other", Status: Status{RunningServerID: "srv_other", Lifecycle: LifecycleRunning}})
	manager.publish(Event{Type: "status", ServerID: "srv_selected", Status: Status{RunningServerID: "srv_selected", Lifecycle: LifecycleRunning}})

	select {
	case event := <-events:
		if event.Type != "status" || event.ServerID != "srv_selected" {
			t.Fatalf("expected selected status event, got %#v", event)
		}
	default:
		t.Fatal("expected selected status event")
	}

	select {
	case event := <-events:
		t.Fatalf("unexpected filtered event: %#v", event)
	default:
	}
}

// A server that has not printed its ready line must stay "starting"; Start
// returning must not be taken as "the server is up".
func TestManagerStaysStartingUntilServerIsReady(t *testing.T) {
	dir := t.TempDir()
	launchTarget := writeSilentServerScript(t, dir)

	manager := NewManager(t.TempDir())
	t.Cleanup(func() { manager.Shutdown(2 * time.Second) })
	status, err := manager.Start(fakeServer(dir, launchTarget))
	if err != nil {
		t.Fatal(err)
	}
	if status.Lifecycle != LifecycleStarting {
		t.Fatalf("a server that has not reported ready must be starting, got %s", status.Lifecycle)
	}
	// A negative assertion ("nothing happens") cannot be polled for: give the
	// startup path a moment to wrongly flip the lifecycle, then check it did not.
	time.Sleep(300 * time.Millisecond)
	if got := manager.StatusFor("srv_test").Lifecycle; got != LifecycleStarting {
		t.Fatalf("lifecycle drifted to %s without a ready message", got)
	}
}

func TestManagerReadyWatchdogEventuallyReportsRunning(t *testing.T) {
	previous := readyTimeout
	readyTimeout = 300 * time.Millisecond
	t.Cleanup(func() { readyTimeout = previous })

	dir := t.TempDir()
	launchTarget := writeSilentServerScript(t, dir)
	manager := NewManager(t.TempDir())
	t.Cleanup(func() { manager.Shutdown(2 * time.Second) })
	if _, err := manager.Start(fakeServer(dir, launchTarget)); err != nil {
		t.Fatal(err)
	}
	waitForLifecycle(t, manager, "srv_test", LifecycleRunning)
	logs := strings.Join(manager.Logs("srv_test"), "\n")
	if !strings.Contains(logs, "no ready message") {
		t.Fatalf("the watchdog should explain why it marked the server running, logs:\n%s", logs)
	}
}
