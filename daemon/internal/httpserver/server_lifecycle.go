package httpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	javamanager "github.com/W1seGit/Cliff/daemon/internal/java"
	"github.com/W1seGit/Cliff/daemon/internal/process"
	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func (h apiHandler) servers(w http.ResponseWriter, r *http.Request) {
	user, ok := h.authenticatedUser(w, r)
	if !ok {
		return
	}
	servers, err := h.store.ListServers(r.Context())
	if err == nil {
		servers, err = h.visibleServers(r, user, servers)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	includeRuntime := r.URL.Query().Get("runtime") == "1" || r.URL.Query().Get("health") == "1"
	includeHealth := r.URL.Query().Get("health") == "1"
	if includeRuntime {
		runtimeStatus := h.process.StatusLight()
		usageFor := strings.TrimSpace(r.URL.Query().Get("usageFor"))
		if usageFor != "" {
			runtimeStatus = mergeServerRuntime(runtimeStatus, h.process.StatusFor(usageFor), usageFor)
		}
		if includeHealth {
			if usageFor == "" {
				runtimeStatus = h.process.Status()
			}
		}
		runtimeStatus, err = h.visibleRuntime(r, user, runtimeStatus)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		payload := map[string]any{
			"servers": servers,
			"runtime": runtimeStatus,
		}
		if includeHealth {
			health := map[string]serverHealth{}
			healthFor := strings.TrimSpace(r.URL.Query().Get("healthFor"))
			for _, server := range servers {
				if healthFor != "" && server.ID != healthFor {
					continue
				}
				health[server.ID] = h.cachedServerHealth(server)
			}
			payload["health"] = health
		}
		writeJSON(w, http.StatusOK, payload)
		return
	}
	writeJSON(w, http.StatusOK, servers)
}

func mergeServerRuntime(fleet process.Status, serverRuntime process.Status, serverID string) process.Status {
	if fleet.Servers == nil {
		fleet.Servers = map[string]process.Status{}
	}
	fleet.Servers[serverID] = serverRuntime
	if fleet.RunningServerID == serverID {
		fleet.Usage = serverRuntime.Usage
	}
	return fleet
}

func (h apiHandler) runtime(w http.ResponseWriter, r *http.Request) {
	user, ok := h.authenticatedUser(w, r)
	if !ok {
		return
	}
	status := h.process.Status()
	if r.URL.Query().Get("light") == "1" {
		status = h.process.StatusLight()
	}
	status, err := h.visibleRuntime(r, user, status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h apiHandler) serverUsage(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("id")
	windowParam := r.URL.Query().Get("window")
	var window time.Duration
	switch windowParam {
	case "5m", "":
		window = 5 * time.Minute
	case "15m":
		window = 15 * time.Minute
	case "1h":
		window = time.Hour
	case "24h":
		window = 24 * time.Hour
	default:
		window = 5 * time.Minute
	}
	usage := h.process.UsageForWindow(serverID, window)
	if usage == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"usage": &process.Usage{
				Samples:       []process.UsageSample{},
				PlayerSamples: []process.PlayerSample{},
			},
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"usage": usage})
}

func (h apiHandler) serverDetail(w http.ResponseWriter, r *http.Request) {
	server, ok, err := h.store.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server": server, "runtime": h.process.StatusForLight(server.ID)})
}

func (h apiHandler) updateServer(w http.ResponseWriter, r *http.Request) {
	current, ok, err := h.store.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	var raw map[string]any
	if err := readJSON(r, &raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid server body")
		return
	}
	next := current
	if value, ok := raw["name"].(string); ok {
		next.Name = strings.TrimSpace(value)
	}
	if value, ok := raw["type"].(string); ok {
		next.Type = value
	}
	if value, ok := raw["minecraftVersion"].(string); ok {
		next.MinecraftVersion = value
	}
	if value, ok := raw["loaderVersion"].(string); ok {
		next.LoaderVersion = value
	}
	if value, ok := raw["javaPath"].(string); ok {
		next.JavaPath = value
	}
	if value, ok := raw["launchJar"].(string); ok {
		next.LaunchJar = value
	}
	if value, ok := raw["extraArgs"].(string); ok {
		next.ExtraArgs = value
	}
	if value, ok := numberValue(raw["minMemoryMb"]); ok {
		next.MinMemoryMB = value
	}
	if value, ok := numberValue(raw["maxMemoryMb"]); ok {
		next.MaxMemoryMB = value
	}
	if value, ok := numberValue(raw["port"]); ok {
		next.Port = value
	}
	if value, ok := raw["scheduledSnapshotsEnabled"].(bool); ok {
		next.ScheduledSnapshotsEnabled = value
	}
	if value, ok := numberValue(raw["snapshotIntervalMinutes"]); ok {
		next.SnapshotIntervalMinutes = value
	}
	if value, ok := raw["jvmPreset"].(string); ok {
		next.JvmPreset = strings.TrimSpace(value)
		if !process.ValidJvmPreset(next.JvmPreset) {
			writeError(w, http.StatusBadRequest, "Unknown JVM preset")
			return
		}
	}
	if value, ok := raw["restartPolicy"].(string); ok {
		next.RestartPolicy = strings.TrimSpace(value)
		if next.RestartPolicy != store.RestartPolicyOff && next.RestartPolicy != store.RestartPolicyOnCrash {
			writeError(w, http.StatusBadRequest, "Unknown restart policy")
			return
		}
	}
	if value, ok := numberValue(raw["restartMaxAttempts"]); ok {
		next.RestartMaxAttempts = value
	}
	if value, ok := numberValue(raw["restartWindowMinutes"]); ok {
		next.RestartWindowMinutes = value
	}
	next.Type = strings.TrimSpace(next.Type)
	next.MinecraftVersion = strings.TrimSpace(next.MinecraftVersion)
	next.LoaderVersion = strings.TrimSpace(next.LoaderVersion)
	if !validServerType(next.Type) {
		writeError(w, http.StatusBadRequest, "Invalid server type")
		return
	}
	metadata, err := h.getMinecraftMetadata(r, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if next.MinecraftVersion == "" {
		next.MinecraftVersion = metadata.Latest.Release
	}
	if !metadataHasMinecraftVersion(metadata, next.MinecraftVersion) {
		writeError(w, http.StatusBadRequest, "Minecraft "+next.MinecraftVersion+" is not available in current release metadata")
		return
	}
	if !serverTypeNeedsLoader(next.Type) {
		next.LoaderVersion = ""
	} else {
		if next.LoaderVersion == "" {
			writeError(w, http.StatusBadRequest, "Loader version is required for this server type")
			return
		}
		loaders, err := h.getLoaderVersions(r, next.Type, next.MinecraftVersion, false)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !loaderListContains(loaders, next.LoaderVersion) {
			writeError(w, http.StatusBadRequest, next.Type+" loader "+next.LoaderVersion+" is not available for Minecraft "+next.MinecraftVersion)
			return
		}
	}
	server, err := h.store.UpdateServer(r.Context(), current.ID, next)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.invalidateServerHealth(current.ID)
	writeJSON(w, http.StatusOK, map[string]store.Server{"server": server})
}

func (h apiHandler) deleteServer(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("id")
	server, ok, err := h.store.GetServer(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	if h.process.IsRunning(serverID) {
		writeError(w, http.StatusBadRequest, "Stop this server before removing it")
		return
	}
	var input struct {
		DeleteFiles bool `json:"deleteFiles"`
	}
	_ = readJSON(r, &input)
	if input.DeleteFiles {
		root := filepath.Clean(h.config.ServerRoot)
		target := filepath.Clean(server.Path)
		if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
			writeError(w, http.StatusBadRequest, "Only folders inside the configured server root can be deleted. Unregister this imported server instead.")
			return
		}
		if err := removeAllWithRetry(target); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := h.store.DeleteServerRecord(r.Context(), serverID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.process.Forget(serverID)
	h.invalidateServerHealth(serverID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h apiHandler) start(w http.ResponseWriter, r *http.Request) {
	server, ok, err := h.store.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	if !requireEULA(w, server) {
		return
	}
	server, err = h.resolveServerLaunchTarget(r, server)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	server, err = h.resolveJavaForLaunch(r, server)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	status, err := h.process.Start(server)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h apiHandler) stop(w http.ResponseWriter, r *http.Request) {
	force, err := requestForce(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid stop body")
		return
	}
	status, err := h.process.StopAndWait(r.PathValue("id"), force, 30*time.Second)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h apiHandler) restart(w http.ResponseWriter, r *http.Request) {
	server, ok, err := h.store.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	force, err := requestForce(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid restart body")
		return
	}
	if !requireEULA(w, server) {
		return
	}
	server, err = h.resolveServerLaunchTarget(r, server)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	server, err = h.resolveJavaForLaunch(r, server)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	status, err := h.process.Restart(server, force, 30*time.Second)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h apiHandler) resolveJavaForLaunch(r *http.Request, server store.Server) (store.Server, error) {
	return h.resolveJavaForLaunchCtx(r.Context(), server)
}

func (h apiHandler) resolveJavaForLaunchCtx(ctx context.Context, server store.Server) (store.Server, error) {
	resolved, err := javamanager.Resolver{DataDir: h.config.DataDir}.Resolve(ctx, server.JavaPath, server.MinecraftVersion)
	if err != nil {
		return server, fmt.Errorf("managed Java setup failed: %w", err)
	}
	server.JavaPath = resolved
	return server, nil
}

func (h apiHandler) resolveServerLaunchTarget(r *http.Request, server store.Server) (store.Server, error) {
	return h.resolveServerLaunchTargetCtx(r.Context(), server)
}

func (h apiHandler) resolveServerLaunchTargetCtx(ctx context.Context, server store.Server) (store.Server, error) {
	launchTarget := process.SuggestLaunchTarget(server.Path, server.LaunchJar)
	if launchTarget == "" {
		return server, nil
	}
	updated, err := h.store.UpdateServer(ctx, server.ID, store.Server{LaunchJar: launchTarget})
	if err != nil {
		return server, fmt.Errorf("could not update server launch target: %w", err)
	}
	return updated, nil
}

func requestForce(r *http.Request) (bool, error) {
	if r.URL.Query().Get("force") == "1" {
		return true, nil
	}
	if r.Body == nil || r.ContentLength == 0 {
		return false, nil
	}
	defer r.Body.Close()
	var input struct {
		Force bool `json:"force"`
	}
	if err := decodeBoundedRequestJSON(r.Body, &input); err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return input.Force, nil
}

func removeAllWithRetry(target string) error {
	var err error
	for attempt := 0; attempt < 10; attempt++ {
		err = os.RemoveAll(target)
		if err == nil {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return err
}
