package process

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// eventLog collects lifecycle events from a manager.
type eventLog struct {
	mu     sync.Mutex
	events []LifecycleEvent
}

func collectEvents(manager *Manager) *eventLog {
	log := &eventLog{}
	manager.SetLifecycleHandler(func(event LifecycleEvent) {
		log.mu.Lock()
		log.events = append(log.events, event)
		log.mu.Unlock()
	})
	return log
}

func (l *eventLog) find(eventType string) (LifecycleEvent, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, event := range l.events {
		if event.Type == eventType {
			return event, true
		}
	}
	return LifecycleEvent{}, false
}

func (l *eventLog) wait(t *testing.T, eventType string) LifecycleEvent {
	t.Helper()
	var found LifecycleEvent
	waitFor(t, 5*time.Second, func() string { return "a " + eventType + " event" }, func() bool {
		event, ok := l.find(eventType)
		found = event
		return ok
	})
	return found
}

func TestUnrequestedNonZeroExitIsReportedAsACrashWithItsLastOutput(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager(t.TempDir())
	events := collectEvents(manager)
	server := fakeServer(dir, writeChattyFailingServerScript(t, dir))

	_, _ = manager.Start(server) // fails during startup; the exit is still a crash

	crash := events.wait(t, EventCrashed)
	if crash.ExitCode != 2 || crash.Server.ID != server.ID {
		t.Fatalf("unexpected crash event: %+v", crash)
	}
	// The pipes are drained before the exit is reported, so the final lines
	// the server printed are not lost.
	for _, want := range []string{"stdout-final-line", "stderr-final-line"} {
		if !strings.Contains(crash.LastOutput, want) {
			t.Fatalf("crash output is missing %q: %s", want, crash.LastOutput)
		}
	}
	if _, stopped := events.find(EventStopped); stopped {
		t.Fatal("a crash must not also be reported as a normal stop")
	}
}

func TestRequestedStopIsNotACrash(t *testing.T) {
	manager, server := startFakeServer(t)
	events := collectEvents(manager)

	if _, err := manager.StopAndWait(server.ID, false, 5*time.Second); err != nil {
		t.Fatal(err)
	}

	events.wait(t, EventStopped)
	if _, crashed := events.find(EventCrashed); crashed {
		t.Fatal("a stop the user asked for must not be reported as a crash")
	}
}

func TestReadyAndStartedEventsAreReported(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager(t.TempDir())
	t.Cleanup(func() { manager.Shutdown(2 * time.Second) })
	events := collectEvents(manager)
	server := fakeServer(dir, writeFakeServerScript(t, dir))

	if _, err := manager.Start(server); err != nil {
		t.Fatal(err)
	}
	events.wait(t, EventStarted)
	if ready := events.wait(t, EventReady); !ready.WasReady || ready.Server.ID != server.ID {
		t.Fatalf("unexpected ready event: %+v", ready)
	}
}
