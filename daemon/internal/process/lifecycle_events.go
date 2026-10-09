package process

import (
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

// Lifecycle event types handed to the handler set with SetLifecycleHandler.
const (
	EventStarted     = "started"
	EventReady       = "ready"
	EventStopped     = "stopped"
	EventCrashed     = "crashed"
	EventPlayerJoin  = "player-join"
	EventPlayerLeave = "player-leave"
)

// LifecycleEvent describes something that happened to a managed server.
// Webhook notifications and the crash-restart supervisor both consume them.
type LifecycleEvent struct {
	Type   string
	Server store.Server
	// ExitCode and Uptime are set for stopped and crashed events.
	ExitCode int
	Uptime   time.Duration
	// WasReady is true when the server finished starting before it exited.
	WasReady bool
	// LastOutput is the tail of the console for crashed events.
	LastOutput string
	// Player is set for player-join and player-leave events.
	Player      string
	PlayerCount int
}

// SetLifecycleHandler registers the function called for every lifecycle
// event. The handler runs on its own goroutine, so it may call back into the
// Manager (to restart a crashed server, for example).
func (m *Manager) SetLifecycleHandler(handler func(LifecycleEvent)) {
	m.mu.Lock()
	m.lifecycleHandler = handler
	m.mu.Unlock()
}

func (m *Manager) emitLifecycle(event LifecycleEvent) {
	m.mu.Lock()
	handler := m.lifecycleHandler
	m.mu.Unlock()
	if handler != nil {
		go handler(event)
	}
}
