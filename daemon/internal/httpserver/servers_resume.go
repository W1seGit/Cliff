package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
)

// runningServer is a server that an update would interrupt.
type runningServer struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Lifecycle string `json:"lifecycle"`
}

// runningServers lists the servers that are starting, running or stopping, with their names.
func (h apiHandler) runningServers(ctx context.Context) []runningServer {
	if h.process == nil {
		return nil
	}
	ids := h.process.RunningServerIDs()
	servers := make([]runningServer, 0, len(ids))
	for _, id := range ids {
		entry := runningServer{ID: id, Name: id, Lifecycle: string(h.process.StatusForLight(id).Lifecycle)}
		if h.store != nil {
			if server, ok, err := h.store.GetServer(ctx, id); err == nil && ok {
				entry.Name = server.Name
			}
		}
		servers = append(servers, entry)
	}
	return servers
}

// updatesServers tells the dashboard which servers an update would stop, so it
// can say so before anyone presses Install.
func (h apiHandler) updatesServers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"servers": h.runningServers(r.Context())})
}

// internalRunningServers answers the command line, which has to warn before it
// stops the daemon. Local callers holding the token only.
func (h apiHandler) internalRunningServers(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeInternal(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": h.runningServers(r.Context())})
}

// resumeResult is the outcome of starting one server again after an update.
type resumeResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"` // "started", "skipped" or "failed"
	Message string `json:"message,omitempty"`
}

// internalResumeServers starts the given servers again once an update has
// finished. It uses the same checks and launch steps as pressing Start.
func (h apiHandler) internalResumeServers(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeInternal(w, r) {
		return
	}
	var input struct {
		ServerIDs []string `json:"serverIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	results := make([]resumeResult, 0, len(input.ServerIDs))
	for _, id := range input.ServerIDs {
		results = append(results, h.resumeServer(r.Context(), id))
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (h apiHandler) resumeServer(ctx context.Context, id string) resumeResult {
	result := resumeResult{ID: id, Name: id}
	server, ok, err := h.store.GetServer(ctx, id)
	if err != nil || !ok {
		result.Status, result.Message = "failed", "the server no longer exists"
		slog.Warn("could not start a server again after the update", "server", id, "reason", result.Message)
		return result
	}
	result.Name = server.Name
	if h.process.IsRunning(id) {
		result.Status = "started"
		return result
	}
	if !readEULAAccepted(filepath.Join(server.Path, "eula.txt")) {
		result.Status, result.Message = "skipped", "the Minecraft EULA is not accepted"
		slog.Warn("did not start a server again after the update", "server", id, "name", server.Name, "reason", result.Message)
		return result
	}
	launch, err := h.resolveServerLaunchTargetCtx(ctx, server)
	if err == nil {
		launch, err = h.resolveJavaForLaunchCtx(ctx, launch)
	}
	if err == nil {
		_, err = h.process.Start(launch)
	}
	if err != nil {
		result.Status, result.Message = "failed", err.Error()
		slog.Error("could not start a server again after the update", "server", id, "name", server.Name, "error", err)
		return result
	}
	slog.Info("started a server again after the update", "server", id, "name", server.Name)
	result.Status = "started"
	return result
}
