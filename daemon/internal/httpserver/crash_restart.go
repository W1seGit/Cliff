package httpserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/process"
	"github.com/W1seGit/Cliff/daemon/internal/store"
)

const (
	crashRestartBaseDelay = 5 * time.Second
	crashRestartMaxDelay  = time.Minute
)

// crashRestarter remembers recent automatic restarts per server so a server
// that keeps crashing is given up on instead of being restarted forever.
type crashRestarter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	// delay and sleep exist so tests do not have to wait.
	delay func(attempt int) time.Duration
}

func newCrashRestarter() *crashRestarter {
	return &crashRestarter{attempts: map[string][]time.Time{}, delay: crashRestartDelay}
}

// crashRestartDelay backs off 5s, 10s, 20s ... up to a minute.
func crashRestartDelay(attempt int) time.Duration {
	delay := crashRestartBaseDelay
	for i := 1; i < attempt && delay < crashRestartMaxDelay; i++ {
		delay *= 2
	}
	if delay > crashRestartMaxDelay {
		delay = crashRestartMaxDelay
	}
	return delay
}

// reserve records a restart attempt for the server. It returns the attempt
// number within the window and false when the limit has been reached.
func (c *crashRestarter) reserve(server store.Server, now time.Time) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	window := time.Duration(server.RestartWindowMinutes) * time.Minute
	recent := c.attempts[server.ID][:0]
	for _, at := range c.attempts[server.ID] {
		if now.Sub(at) < window {
			recent = append(recent, at)
		}
	}
	if len(recent) >= server.RestartMaxAttempts {
		c.attempts[server.ID] = recent
		return len(recent), false
	}
	recent = append(recent, now)
	c.attempts[server.ID] = recent
	return len(recent), true
}

// handleLifecycle receives every process event: it sends notifications and
// restarts crashed servers whose policy asks for it.
func (h apiHandler) handleLifecycle(ctx context.Context, event process.LifecycleEvent) {
	if note, ok := notificationForLifecycle(event); ok {
		h.notifier.notify(note)
	}
	if event.Type == process.EventCrashed {
		h.restartAfterCrash(ctx, event)
	}
}

func (h apiHandler) restartAfterCrash(ctx context.Context, event process.LifecycleEvent) {
	if h.restarter == nil || h.store == nil || ctx.Err() != nil {
		return
	}
	// Read the server again: the policy may have changed since it started.
	server, ok, err := h.store.GetServer(ctx, event.Server.ID)
	if err != nil || !ok || server.RestartPolicy != store.RestartPolicyOnCrash {
		return
	}
	attempt, allowed := h.restarter.reserve(server, time.Now())
	if !allowed {
		slog.Error("not restarting a crashed server: too many restarts", "server", server.ID, "name", server.Name,
			"max", server.RestartMaxAttempts, "windowMinutes", server.RestartWindowMinutes)
		h.notifier.notify(notification{
			Event:      notifyRestartGaveUp,
			Level:      "error",
			Title:      server.Name + " keeps crashing",
			Message:    fmt.Sprintf("Cliff restarted it %d times in %d minutes and has stopped trying. Check the console, then start it again yourself.", server.RestartMaxAttempts, server.RestartWindowMinutes),
			ServerID:   server.ID,
			ServerName: server.Name,
		})
		return
	}

	delay := h.restarter.delay(attempt)
	slog.Warn("restarting a crashed server", "server", server.ID, "name", server.Name, "attempt", attempt, "of", server.RestartMaxAttempts, "in", delay.String())
	h.notifier.notify(notification{
		Event:      notifyServerRestart,
		Level:      "warning",
		Title:      server.Name + " is restarting",
		Message:    fmt.Sprintf("Attempt %d of %d. Restarting in %s.", attempt, server.RestartMaxAttempts, delay.Round(time.Second)),
		ServerID:   server.ID,
		ServerName: server.Name,
	})

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	if h.process.IsRunning(server.ID) {
		return // someone started it in the meantime
	}
	if err := h.startServerAfterCrash(ctx, server); err != nil {
		slog.Error("could not restart a crashed server", "server", server.ID, "name", server.Name, "error", err)
		h.notifier.notify(notification{
			Event:      notifyRestartGaveUp,
			Level:      "error",
			Title:      "Could not restart " + server.Name,
			Message:    err.Error(),
			ServerID:   server.ID,
			ServerName: server.Name,
		})
	}
}

func (h apiHandler) startServerAfterCrash(ctx context.Context, server store.Server) error {
	if !readEULAAccepted(filepath.Join(server.Path, "eula.txt")) {
		return fmt.Errorf("the Minecraft EULA is not accepted")
	}
	launch, err := h.resolveServerLaunchTargetCtx(ctx, server)
	if err != nil {
		return err
	}
	launch, err = h.resolveJavaForLaunchCtx(ctx, launch)
	if err != nil {
		return err
	}
	_, err = h.process.Start(launch)
	return err
}

// jvmPresets lists the JVM flag presets with the flags worked out for a heap size.
func (h apiHandler) jvmPresets(w http.ResponseWriter, r *http.Request) {
	maxMemory, _ := strconv.Atoi(r.URL.Query().Get("maxMemoryMb"))
	writeJSON(w, http.StatusOK, map[string]any{"presets": process.JvmPresets(maxMemory)})
}
