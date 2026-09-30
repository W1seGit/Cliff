package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/updater"
)

// restartMarkerName records which servers were running when Cliff restarted
// itself, so the restarted daemon can start them again.
const restartMarkerName = "restart-resume.json"

type restartMarker struct {
	ServerIDs []string `json:"serverIds"`
	At        string   `json:"at"`
}

// confirmServersStop answers 409 when a server is running and the caller has
// not confirmed that stopping Cliff will stop it. It returns the running
// servers' ids and whether the request may go on.
func (h apiHandler) confirmServersStop(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	var input struct {
		ConfirmStopServers bool `json:"confirmStopServers"`
	}
	_ = json.NewDecoder(r.Body).Decode(&input)
	running := h.runningServers(r.Context())
	if len(running) > 0 && !input.ConfirmStopServers {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "A Minecraft server is running, and it stops when Cliff does.",
			"code":    "servers_running",
			"servers": running,
		})
		return nil, false
	}
	ids := make([]string, 0, len(running))
	for _, server := range running {
		ids = append(ids, server.ID)
	}
	return ids, true
}

// daemonStop stops Cliff at the user's request: servers are stopped and their
// worlds saved, then the daemon exits. It has to be started again from the
// host machine with `cliff start`.
func (h apiHandler) daemonStop(w http.ResponseWriter, r *http.Request) {
	if h.shutdown == nil {
		writeError(w, http.StatusServiceUnavailable, "Stopping from the dashboard is not available here")
		return
	}
	ids, ok := h.confirmServersStop(w, r)
	if !ok {
		return
	}
	slog.Info("stopping Cliff at the user's request", "runningServers", len(ids))
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "message": "Cliff is stopping. Start it again with 'cliff start' on the host machine."})
	go func() {
		// Let the response reach the browser first.
		time.Sleep(300 * time.Millisecond)
		h.shutdown()
	}()
}

// daemonRestart restarts Cliff itself, then starts again the servers that
// were running. The dashboard reloads once it answers again.
func (h apiHandler) daemonRestart(w http.ResponseWriter, r *http.Request) {
	ids, ok := h.confirmServersStop(w, r)
	if !ok {
		return
	}
	slog.Info("restarting Cliff at the user's request", "runningServers", len(ids))
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "message": "Cliff is restarting."})
	go func() {
		time.Sleep(300 * time.Millisecond)
		if h.process != nil {
			h.process.Shutdown(25 * time.Second)
		}
		if len(ids) > 0 {
			marker := restartMarker{ServerIDs: ids, At: time.Now().UTC().Format(time.RFC3339)}
			if data, err := json.Marshal(marker); err == nil {
				_ = os.WriteFile(filepath.Join(h.config.DataDir, restartMarkerName), data, 0o644)
			}
		}
		binary, err := os.Executable()
		if err != nil {
			slog.Error("could not restart Cliff", "error", err)
			return
		}
		if err := updater.Restart(binary, os.Args[1:]); err != nil {
			slog.Error("could not restart Cliff", "error", err)
		}
	}()
}

// resumeAfterRestart starts the servers that were running before a restart
// requested from the dashboard. The marker is removed first so a crash in here
// can never start servers a second time.
func (h apiHandler) resumeAfterRestart(ctx context.Context) {
	path := filepath.Join(h.config.DataDir, restartMarkerName)
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	_ = os.Remove(path)
	var marker restartMarker
	if json.Unmarshal(data, &marker) != nil || len(marker.ServerIDs) == 0 {
		return
	}
	select {
	case <-time.After(2 * time.Second):
	case <-ctx.Done():
		return
	}
	for _, id := range marker.ServerIDs {
		result := h.resumeServer(ctx, id)
		slog.Info("server after restart", "server", id, "name", result.Name, "status", result.Status, "reason", result.Message)
	}
}
