package httpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	urlpath "path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/buildinfo"
	"github.com/W1seGit/Cliff/daemon/internal/config"
	javamanager "github.com/W1seGit/Cliff/daemon/internal/java"
	"github.com/W1seGit/Cliff/daemon/internal/logbuf"
	"github.com/W1seGit/Cliff/daemon/internal/process"
	"github.com/W1seGit/Cliff/daemon/internal/store"
)

type Options struct {
	Config           config.Config
	Store            *store.Store
	Process          *process.Manager
	StartedAt        time.Time
	SchedulerContext context.Context
	LogBuffer        *logbuf.Buffer
}

func New(options Options) http.Handler {
	mux := http.NewServeMux()
	manager := options.Process
	if manager == nil {
		manager = process.NewManager(options.Config.DataDir)
	}
	startedAt := options.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	api := apiHandler{
		config:        options.Config,
		store:         options.Store,
		process:       manager,
		startedAt:     startedAt.UTC(),
		storageCache:  &storageUsageCache{},
		metadataCache: &metadataCache{},
		sizeCache:     &directorySizeCache{},
		healthCache:   &serverHealthCache{},
		playit:        newPlayitAgentManager(),
		logBuffer:     options.LogBuffer,
	}
	if options.SchedulerContext != nil {
		go api.runScheduler(options.SchedulerContext)
	}

	mux.HandleFunc("GET /api/health", api.health)
	mux.HandleFunc("GET /api/auth/me", api.authMe)
	mux.HandleFunc("POST /api/auth/setup", api.authSetup)
	mux.HandleFunc("POST /api/auth/login", api.authLogin)
	mux.HandleFunc("POST /api/auth/logout", api.authLogout)
	mux.HandleFunc("PATCH /api/auth/account", api.requireUser(api.authAccount))
	mux.HandleFunc("GET /api/minecraft/versions", api.requireUser(api.minecraftVersions))
	mux.HandleFunc("GET /api/java/runtimes", api.requireUser(api.javaRuntimes))
	mux.HandleFunc("POST /api/java/runtimes", api.requireUser(api.installJavaRuntime))
	mux.HandleFunc("DELETE /api/java/runtimes", api.requireUser(api.uninstallJavaRuntime))
	mux.HandleFunc("GET /api/public-access/playit/agent", api.requireUser(api.playitAgentStatus))
	mux.HandleFunc("POST /api/public-access/playit/agent/install", api.requireUser(api.installPlayitAgent))
	mux.HandleFunc("POST /api/public-access/playit/agent/start", api.requireUser(api.startPlayitAgent))
	mux.HandleFunc("POST /api/public-access/playit/agent/stop", api.requireUser(api.stopPlayitAgent))
	mux.HandleFunc("POST /api/public-access/playit/agent/uninstall", api.requireUser(api.uninstallPlayitAgent))
	mux.HandleFunc("POST /api/public-access/playit/agent/reset", api.requireUser(api.resetPlayitAgent))
	mux.HandleFunc("GET /api/settings", api.requireUser(api.settings))
	mux.HandleFunc("PUT /api/settings", api.requireUser(api.updateSettings))
	mux.HandleFunc("GET /api/daemon-logs", api.requireUser(api.daemonLogs))
	mux.HandleFunc("GET /api/servers", api.requireUser(api.servers))
	mux.HandleFunc("POST /api/servers", api.requireUser(api.createServer))
	mux.HandleFunc("GET /api/servers/{id}", api.requireUser(api.serverDetail))
	mux.HandleFunc("GET /api/servers/{id}/health", api.requireUser(api.serverHealth))
	mux.HandleFunc("PATCH /api/servers/{id}", api.requireUser(api.updateServer))
	mux.HandleFunc("DELETE /api/servers/{id}", api.requireUser(api.deleteServer))
	mux.HandleFunc("GET /api/servers/{id}/public-access", api.requireUser(api.serverPublicAccess))
	mux.HandleFunc("PUT /api/servers/{id}/public-access", api.requireUser(api.saveServerPublicAccess))
	mux.HandleFunc("DELETE /api/servers/{id}/public-access", api.requireUser(api.deleteServerPublicAccess))
	mux.HandleFunc("GET /api/servers/{id}/properties", api.requireUser(api.serverProperties))
	mux.HandleFunc("PUT /api/servers/{id}/properties", api.requireUser(api.updateServerProperties))
	mux.HandleFunc("GET /api/servers/{id}/players", api.requireUser(api.players))
	mux.HandleFunc("POST /api/servers/{id}/players", api.requireUser(api.updatePlayers))
	mux.HandleFunc("GET /api/servers/{id}/files", api.requireUser(api.files))
	mux.HandleFunc("POST /api/servers/{id}/files", api.requireUser(api.fileAction))
	mux.HandleFunc("GET /api/servers/{id}/worlds", api.requireUser(api.worlds))
	mux.HandleFunc("POST /api/servers/{id}/worlds", api.requireUser(api.worldAction))
	mux.HandleFunc("GET /api/servers/{id}/mods", api.requireUser(api.mods))
	mux.HandleFunc("POST /api/servers/{id}/mods", api.requireUser(api.modAction))
	mux.HandleFunc("GET /api/runtime", api.requireUser(api.runtime))
	mux.HandleFunc("GET /api/servers/{id}/usage", api.requireUser(api.serverUsage))
	mux.HandleFunc("POST /api/servers/{id}/start", api.requireUser(api.start))
	mux.HandleFunc("POST /api/servers/{id}/stop", api.requireUser(api.stop))
	mux.HandleFunc("POST /api/servers/{id}/restart", api.requireUser(api.restart))
	mux.HandleFunc("GET /api/servers/{id}/command", api.requireUser(api.commandPresets))
	mux.HandleFunc("POST /api/servers/{id}/command", api.requireUser(api.command))
	mux.HandleFunc("GET /api/servers/{id}/backups", api.requireUser(api.backups))
	mux.HandleFunc("POST /api/servers/{id}/backups", api.requireUser(api.backupAction))
	mux.HandleFunc("GET /api/servers/{id}/logs", api.requireUser(api.logs))
	mux.HandleFunc("GET /api/servers/{id}/console", api.requireUser(api.console))
	mux.Handle("/", spaFileServer(options.Config.WebDir))

	return withErrorLogging(withCommonHeaders(mux))
}

type apiHandler struct {
	config        config.Config
	store         *store.Store
	process       *process.Manager
	startedAt     time.Time
	storageCache  *storageUsageCache
	metadataCache *metadataCache
	sizeCache     *directorySizeCache
	healthCache   *serverHealthCache
	playit        *playitAgentManager
	logBuffer     *logbuf.Buffer
}

type storageUsageCache struct {
	mu        sync.Mutex
	root      string
	value     storageUsage
	expiresAt time.Time
}

type directorySizeCache struct {
	mu      sync.Mutex
	entries map[string]directorySizeCacheEntry
}

type directorySizeCacheEntry struct {
	size      int64
	expiresAt time.Time
}

type serverHealthCache struct {
	mu      sync.Mutex
	entries map[string]serverHealthCacheEntry
}

type serverHealthCacheEntry struct {
	fingerprint string
	health      serverHealth
	expiresAt   time.Time
}

type settingsResponse struct {
	ServerRoot       string        `json:"serverRoot"`
	DataDir          string        `json:"dataDir"`
	LogFile          string        `json:"logFile"`
	SnapshotsEnabled bool          `json:"snapshotsEnabled"`
	CurseForgeAPIKey string        `json:"curseForgeApiKey"`
	Storage          *storageUsage `json:"storage,omitempty"`
	Access           accessInfo    `json:"access"`
}

type storageUsage struct {
	RootExists                bool   `json:"rootExists"`
	ServerRootSizeBytes       int64  `json:"serverRootSizeBytes"`
	RegisteredServerSizeBytes int64  `json:"registeredServerSizeBytes"`
	SnapshotsSizeBytes        int64  `json:"snapshotsSizeBytes"`
	BackupCount               int    `json:"backupCount"`
	FreeBytes                 *int64 `json:"freeBytes"`
	TotalBytes                *int64 `json:"totalBytes"`
	UpdatedAt                 string `json:"updatedAt"`
}

type accessInfo struct {
	LANAddresses   []string `json:"lanAddresses"`
	DevURLs        []string `json:"devUrls"`
	ProductionURLs []string `json:"productionUrls"`
}

type daemonSelfMetrics struct {
	PID               int    `json:"pid"`
	Goroutines        int    `json:"goroutines"`
	HeapAllocBytes    uint64 `json:"heapAllocBytes"`
	HeapSysBytes      uint64 `json:"heapSysBytes"`
	HeapIdleBytes     uint64 `json:"heapIdleBytes"`
	HeapReleasedBytes uint64 `json:"heapReleasedBytes"`
	StackInuseBytes   uint64 `json:"stackInuseBytes"`
	NextGCBytes       uint64 `json:"nextGcBytes"`
	NumGC             uint32 `json:"numGc"`
}

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
		SnapshotsEnabled: settings.SnapshotsEnabled,
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

func (h apiHandler) servers(w http.ResponseWriter, r *http.Request) {
	servers, err := h.store.ListServers(r.Context())
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
	if r.URL.Query().Get("light") == "1" {
		writeJSON(w, http.StatusOK, h.process.StatusLight())
		return
	}
	writeJSON(w, http.StatusOK, h.process.Status())
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
	if value, ok := raw["snapshotsEnabled"].(bool); ok {
		next.SnapshotsEnabled = value
	}
	if value, ok := raw["scheduledSnapshotsEnabled"].(bool); ok {
		next.ScheduledSnapshotsEnabled = value
	}
	if value, ok := numberValue(raw["snapshotIntervalMinutes"]); ok {
		next.SnapshotIntervalMinutes = value
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
	resolved, err := javamanager.Resolver{DataDir: h.config.DataDir}.Resolve(r.Context(), server.JavaPath, server.MinecraftVersion)
	if err != nil {
		return server, fmt.Errorf("managed Java setup failed: %w", err)
	}
	server.JavaPath = resolved
	return server, nil
}

func (h apiHandler) command(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Action   string `json:"action"`
		Command  string `json:"command"`
		PresetID string `json:"presetId"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid command body")
		return
	}
	if input.Action == "save-preset" {
		if _, err := h.store.SaveCommandPreset(r.Context(), r.PathValue("id"), input.Command); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		presets, err := h.store.ListCommandPresets(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "presets": presets})
		return
	}
	if input.Action == "delete-preset" {
		if err := h.store.DeleteCommandPreset(r.Context(), r.PathValue("id"), input.PresetID); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		presets, err := h.store.ListCommandPresets(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "presets": presets})
		return
	}
	if err := h.process.Command(r.PathValue("id"), input.Command); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.process.StatusForLight(r.PathValue("id")))
}

func (h apiHandler) commandPresets(w http.ResponseWriter, r *http.Request) {
	if _, ok, err := h.store.GetServer(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	presets, err := h.store.ListCommandPresets(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string][]store.CommandPreset{"presets": presets})
}

func (h apiHandler) logs(w http.ResponseWriter, r *http.Request) {
	logs := h.process.Logs(r.PathValue("id"))
	if r.URL.Query().Get("download") == "1" {
		server, ok, err := h.store.GetServer(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "Server not found")
			return
		}
		w.Header().Set("Content-Disposition", `attachment; filename="`+safeLogFileName(server.Name)+`"`)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		for _, line := range logs {
			_, _ = w.Write([]byte(line + "\n"))
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"logs": logs,
	})
}

func safeLogFileName(name string) string {
	trimmed := strings.Trim(strings.TrimSpace(regexp.MustCompile(`[^a-zA-Z0-9._-]+`).ReplaceAllString(name, "-")), "-")
	if trimmed == "" {
		trimmed = "server"
	}
	return trimmed + "-console.log"
}

func (h apiHandler) daemonLogs(w http.ResponseWriter, r *http.Request) {
	// "full=1" reads the entire log file from disk instead of the in-memory buffer
	if r.URL.Query().Get("full") == "1" {
		logPath := filepath.Join(h.config.DataDir, "logs", "daemon.log")
		data, err := os.ReadFile(logPath)
		if err != nil {
			if os.IsNotExist(err) {
				writeJSON(w, http.StatusOK, map[string][]string{"logs": {}})
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		lines := splitLogLines(string(data))
		if r.URL.Query().Get("download") == "1" {
			w.Header().Set("Content-Disposition", `attachment; filename="daemon.log"`)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			for _, line := range lines {
				_, _ = w.Write([]byte(line + "\n"))
			}
			return
		}
		writeJSON(w, http.StatusOK, map[string][]string{"logs": lines})
		return
	}

	if h.logBuffer == nil {
		writeJSON(w, http.StatusOK, map[string][]string{"logs": {}})
		return
	}
	lines := h.logBuffer.Lines()
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="daemon.log"`)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		for _, line := range lines {
			_, _ = w.Write([]byte(line + "\n"))
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string][]string{"logs": lines})
}

func splitLogLines(data string) []string {
	data = strings.TrimRight(data, "\r\n")
	if data == "" {
		return []string{}
	}
	return strings.Split(data, "\n")
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

func spaFileServer(root string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		requestPath := staticRequestPath(r.URL.Path)
		if requestPath != "" {
			fullPath := filepath.Join(root, requestPath)
			if info, err := os.Stat(fullPath); err == nil && !info.IsDir() {
				setStaticCacheHeaders(w, r.URL.Path, false)
				http.ServeFile(w, r, fullPath)
				return
			}
		}

		indexPath := filepath.Join(root, "index.html")
		if _, err := os.Stat(indexPath); err == nil {
			setStaticCacheHeaders(w, r.URL.Path, true)
			http.ServeFile(w, r, indexPath)
			return
		}

		writeError(w, http.StatusNotFound, "static dashboard has not been built into the daemon web directory")
	})
}

func staticRequestPath(requestPath string) string {
	decodedPath, err := url.PathUnescape(requestPath)
	if err != nil {
		return ""
	}
	normalizedPath := strings.ReplaceAll(decodedPath, "\\", "/")
	segments := strings.Split(normalizedPath, "/")
	for _, segment := range segments {
		if segment == ".." || strings.Contains(segment, ":") {
			return ""
		}
	}
	cleaned := strings.TrimPrefix(urlpath.Clean("/"+normalizedPath), "/")
	if cleaned == "." || cleaned == "" {
		return "index.html"
	}
	localPath := filepath.FromSlash(cleaned)
	if !filepath.IsLocal(localPath) {
		return ""
	}
	return localPath
}

func setStaticCacheHeaders(w http.ResponseWriter, requestPath string, spaFallback bool) {
	if spaFallback || requestPath == "/" || strings.HasSuffix(requestPath, ".html") {
		w.Header().Set("Cache-Control", "no-cache")
		return
	}
	if strings.HasPrefix(requestPath, "/_next/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
}

func withCommonHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (rec *statusRecorder) WriteHeader(code int) {
	rec.status = code
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := rec.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("ResponseWriter does not support Hijack")
}

func (rec *statusRecorder) Flush() {
	if f, ok := rec.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rec *statusRecorder) Unwrap() http.ResponseWriter {
	return rec.ResponseWriter
}

func withErrorLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		if rec.status >= 500 {
			slog.Error("HTTP request failed", "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration", time.Since(start).String())
		} else if rec.status >= 400 {
			slog.Debug("HTTP client error", "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration", time.Since(start).String())
		}
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	if status >= 500 {
		slog.Error("HTTP server error", "status", status, "message", message)
	}
	writeJSON(w, status, map[string]string{"error": message})
}

const maxJSONRequestBytes int64 = 1 * 1024 * 1024

func readJSON(r *http.Request, value any) error {
	defer r.Body.Close()
	return decodeBoundedRequestJSON(r.Body, value)
}

func decodeBoundedRequestJSON(reader io.Reader, value any) error {
	limited := &io.LimitedReader{R: reader, N: maxJSONRequestBytes + 1}
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(value); err != nil {
		if limited.N <= 0 {
			return errors.New("JSON request body is too large")
		}
		return err
	}
	if limited.N <= 0 {
		return errors.New("JSON request body is too large")
	}
	return nil
}

func numberValue(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	default:
		return 0, false
	}
}
