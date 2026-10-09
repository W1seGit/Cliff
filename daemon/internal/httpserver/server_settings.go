package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/buildinfo"
	"github.com/W1seGit/Cliff/daemon/internal/config"
	javamanager "github.com/W1seGit/Cliff/daemon/internal/java"
	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func (h apiHandler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"daemon":        "cliff",
		"build":         buildinfo.Current(),
		"platform":      config.Platform(),
		"self":          readDaemonSelfMetrics(),
		"startedAt":     h.startedAt.Format(time.RFC3339),
		"uptimeSeconds": int64(time.Since(h.startedAt).Seconds()),
		"localUrl":      h.config.LocalURL(),
		"lanUrls":       h.config.LANURLs(),
	})
}

func readDaemonSelfMetrics() daemonSelfMetrics {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return daemonSelfMetrics{
		PID:               os.Getpid(),
		Goroutines:        runtime.NumGoroutine(),
		HeapAllocBytes:    stats.HeapAlloc,
		HeapSysBytes:      stats.HeapSys,
		HeapIdleBytes:     stats.HeapIdle,
		HeapReleasedBytes: stats.HeapReleased,
		StackInuseBytes:   stats.StackInuse,
		NextGCBytes:       stats.NextGC,
		NumGC:             stats.NumGC,
	}
}

func (h apiHandler) settings(w http.ResponseWriter, r *http.Request) {
	user, ok := h.authenticatedUser(w, r)
	if !ok {
		return
	}
	if user.Role != store.RoleAdmin {
		// Members only need the addresses players join with.
		writeJSON(w, http.StatusOK, settingsResponse{Access: h.accessInfo()})
		return
	}
	settings, err := h.store.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if settings.CurseForgeAPIKey != "" {
		settings.CurseForgeAPIKey = "configured"
	}
	response := settingsResponse{
		ServerRoot:       settings.ServerRoot,
		DataDir:          h.config.DataDir,
		LogFile:          filepath.Join(h.config.DataDir, "logs", "daemon.log"),
		CurseForgeAPIKey: settings.CurseForgeAPIKey,
		Access:           h.accessInfo(),
	}
	if r.URL.Query().Get("storage") != "0" {
		storage, err := h.storageUsage(r.Context(), settings.ServerRoot)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		response.Storage = &storage
	}
	writeJSON(w, http.StatusOK, response)
}

func (h apiHandler) updateSettings(w http.ResponseWriter, r *http.Request) {
	var input store.Settings
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid settings body")
		return
	}
	if err := h.store.UpdateSettings(r.Context(), input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.storageCache != nil {
		h.storageCache.clear()
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h apiHandler) javaRuntimes(w http.ResponseWriter, r *http.Request) {
	required := []int{}
	servers, err := h.store.ListServers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, server := range servers {
		required = append(required, javamanager.RequiredMajor(server.MinecraftVersion))
	}
	resolver := javamanager.Resolver{DataDir: h.config.DataDir}
	runtimes := resolver.List(required...)
	usedByMap := map[int][]string{}
	for _, server := range servers {
		major := javamanager.RequiredMajor(server.MinecraftVersion)
		javaPath := strings.TrimSpace(server.JavaPath)
		if strings.HasPrefix(javaPath, "managed:") {
			if parsed, err := strconv.Atoi(strings.TrimPrefix(javaPath, "managed:")); err == nil && parsed > 0 {
				major = parsed
			}
		} else if javaPath != "" && javaPath != "auto" {
			continue
		}
		usedByMap[major] = append(usedByMap[major], server.Name)
	}
	for i := range runtimes {
		if names, ok := usedByMap[runtimes[i].Major]; ok {
			runtimes[i].UsedBy = names
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runtimes": runtimes})
}

func (h apiHandler) installJavaRuntime(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Major int `json:"major"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid Java runtime body")
		return
	}
	path, err := javamanager.Resolver{DataDir: h.config.DataDir}.Ensure(r.Context(), input.Major)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	runtimes := h.javaRuntimesList(r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": path, "runtimes": runtimes})
}

func (h apiHandler) uninstallJavaRuntime(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Major int `json:"major"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid Java runtime body")
		return
	}
	resolver := javamanager.Resolver{DataDir: h.config.DataDir}
	runtimes := h.javaRuntimesList(r)
	for _, rt := range runtimes {
		if rt.Major == input.Major && rt.Installed && len(rt.UsedBy) > 0 {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("Java %d is used by: %s. Remove or reconfigure those servers before uninstalling.", input.Major, strings.Join(rt.UsedBy, ", ")))
			return
		}
	}
	if err := resolver.Uninstall(input.Major); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated := h.javaRuntimesList(r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "runtimes": updated})
}

func (h apiHandler) javaRuntimesList(r *http.Request) []javamanager.RuntimeInfo {
	required := []int{}
	servers, err := h.store.ListServers(r.Context())
	if err != nil {
		return []javamanager.RuntimeInfo{}
	}
	for _, server := range servers {
		required = append(required, javamanager.RequiredMajor(server.MinecraftVersion))
	}
	resolver := javamanager.Resolver{DataDir: h.config.DataDir}
	runtimes := resolver.List(required...)
	usedByMap := map[int][]string{}
	for _, server := range servers {
		major := javamanager.RequiredMajor(server.MinecraftVersion)
		javaPath := strings.TrimSpace(server.JavaPath)
		if strings.HasPrefix(javaPath, "managed:") {
			if parsed, err := strconv.Atoi(strings.TrimPrefix(javaPath, "managed:")); err == nil && parsed > 0 {
				major = parsed
			}
		} else if javaPath != "" && javaPath != "auto" {
			continue
		}
		usedByMap[major] = append(usedByMap[major], server.Name)
	}
	for i := range runtimes {
		if names, ok := usedByMap[runtimes[i].Major]; ok {
			runtimes[i].UsedBy = names
		}
	}
	return runtimes
}

func (h apiHandler) storageUsage(ctx context.Context, serverRoot string) (storageUsage, error) {
	if h.storageCache != nil {
		if usage, ok := h.storageCache.get(serverRoot); ok {
			return usage, nil
		}
	}

	rootExists := dirExists(serverRoot)
	servers, err := h.store.ListServers(ctx)
	if err != nil {
		return storageUsage{}, err
	}
	var registeredSize int64
	for _, server := range servers {
		registeredSize += h.cachedDirectorySize(server.Path)
	}
	backupCount, err := h.store.CountBackups(ctx)
	if err != nil {
		return storageUsage{}, err
	}
	usage := storageUsage{
		RootExists:                rootExists,
		ServerRootSizeBytes:       h.cachedDirectorySize(serverRoot),
		RegisteredServerSizeBytes: registeredSize,
		SnapshotsSizeBytes:        h.cachedDirectorySize(filepath.Join(serverRoot, ".dashboard-snapshots")),
		BackupCount:               backupCount,
		FreeBytes:                 nil,
		TotalBytes:                nil,
		UpdatedAt:                 time.Now().UTC().Format(time.RFC3339),
	}
	if h.storageCache != nil {
		h.storageCache.set(serverRoot, usage, 10*time.Second)
	}
	return usage, nil
}

func (c *storageUsageCache) get(root string) (storageUsage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.root != root || c.expiresAt.IsZero() || time.Now().After(c.expiresAt) {
		return storageUsage{}, false
	}
	return c.value, true
}

func (c *storageUsageCache) set(root string, value storageUsage, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.root = root
	c.value = value
	c.expiresAt = time.Now().Add(ttl)
}

func (c *storageUsageCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.root = ""
	c.value = storageUsage{}
	c.expiresAt = time.Time{}
}

func (h apiHandler) accessInfo() accessInfo {
	lanURLs := h.config.LANURLs()
	addresses := make([]string, 0, len(lanURLs))
	for _, rawURL := range lanURLs {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			continue
		}
		host := parsed.Hostname()
		if host != "" {
			addresses = append(addresses, host)
		}
	}
	return accessInfo{
		LANAddresses:   addresses,
		DevURLs:        lanURLs,
		ProductionURLs: lanURLs,
	}
}
