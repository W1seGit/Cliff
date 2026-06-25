package httpserver

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	javamanager "github.com/W1seGit/Cliff/daemon/internal/java"
	"github.com/W1seGit/Cliff/daemon/internal/store"
)

const (
	serverHealthCacheTTL        = 5 * time.Second
	maxServerHealthCacheEntries = 256
)

type serverHealthCheck struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

type serverHealthCounts struct {
	Mods         int `json:"mods"`
	DisabledMods int `json:"disabledMods"`
	Worlds       int `json:"worlds"`
	Datapacks    int `json:"datapacks"`
	PlayerFiles  int `json:"playerFiles"`
}

type serverHealth struct {
	Status      string              `json:"status"`
	ActiveWorld string              `json:"activeWorld"`
	Counts      serverHealthCounts  `json:"counts"`
	Checks      []serverHealthCheck `json:"checks"`
}

func (h apiHandler) serverHealth(w http.ResponseWriter, r *http.Request) {
	server, ok, err := h.store.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]serverHealth{"health": h.cachedServerHealth(server)})
}

func (h apiHandler) cachedServerHealth(server store.Server) serverHealth {
	if h.healthCache == nil {
		return readDaemonServerHealth(server)
	}
	return h.healthCache.get(server, serverHealthCacheTTL, readDaemonServerHealth)
}

func (h apiHandler) invalidateServerHealth(serverID string) {
	if h.healthCache != nil {
		h.healthCache.delete(serverID)
	}
}

func (c *serverHealthCache) get(server store.Server, ttl time.Duration, compute func(store.Server) serverHealth) serverHealth {
	now := time.Now()
	fingerprint := serverHealthFingerprint(server)
	c.mu.Lock()
	if c.entries != nil {
		if entry, ok := c.entries[server.ID]; ok && entry.fingerprint == fingerprint && now.Before(entry.expiresAt) {
			health := cloneServerHealth(entry.health)
			c.mu.Unlock()
			return health
		}
	} else {
		c.entries = map[string]serverHealthCacheEntry{}
	}
	c.mu.Unlock()

	health := compute(server)

	c.mu.Lock()
	c.entries[server.ID] = serverHealthCacheEntry{
		fingerprint: fingerprint,
		health:      cloneServerHealth(health),
		expiresAt:   now.Add(ttl),
	}
	c.pruneLocked(now)
	c.mu.Unlock()
	return health
}

func (c *serverHealthCache) delete(serverID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, serverID)
}

func (c *serverHealthCache) pruneLocked(now time.Time) {
	for key, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
	for len(c.entries) > maxServerHealthCacheEntries {
		var oldestKey string
		var oldest time.Time
		for key, entry := range c.entries {
			if oldestKey == "" || entry.expiresAt.Before(oldest) {
				oldestKey = key
				oldest = entry.expiresAt
			}
		}
		delete(c.entries, oldestKey)
	}
}

func serverHealthFingerprint(server store.Server) string {
	return strings.Join([]string{
		server.ID,
		server.Path,
		server.Type,
		server.MinecraftVersion,
		server.LoaderVersion,
		server.JavaPath,
		server.LaunchJar,
		strconv.Itoa(server.MinMemoryMB),
		strconv.Itoa(server.MaxMemoryMB),
		strconv.Itoa(server.Port),
	}, "\x00")
}

func cloneServerHealth(health serverHealth) serverHealth {
	health.Checks = append([]serverHealthCheck(nil), health.Checks...)
	return health
}

func readDaemonServerHealth(server store.Server) serverHealth {
	serverPath := filepath.Clean(server.Path)
	checks := []serverHealthCheck{}
	folderInfo, folderErr := os.Stat(serverPath)
	folderExists := folderErr == nil && folderInfo.IsDir()

	checks = append(checks, healthCheck("folder", "Server folder", stateFromBool(folderExists, "error"), detailFromBool(folderExists, serverPath, "Folder is missing or is not a directory")))
	checks = append(checks, launchTargetCheck(server, serverPath, folderExists))
	checks = append(checks, javaHealthCheck(server))

	properties := readProperties(filepath.Join(serverPath, "server.properties"))
	activeWorld := propertyValue(properties, "level-name", "world")
	propertyPort := intProperty(properties, "server-port", 25565)

	if folderExists && fileExists(filepath.Join(serverPath, "eula.txt")) && readEULAAccepted(filepath.Join(serverPath, "eula.txt")) {
		checks = append(checks, healthCheck("eula", "Minecraft EULA", "ok", "Accepted"))
	} else {
		checks = append(checks, healthCheck("eula", "Minecraft EULA", "warn", "Accept the EULA in server settings before first launch"))
	}

	if len(properties) > 0 {
		checks = append(checks, healthCheck("properties", "server.properties", "ok", "Port "+strconv.Itoa(propertyPort)+", world "+activeWorld))
		if propertyPort != server.Port {
			checks = append(checks, healthCheck("port", "Port alignment", "warn", "Dashboard profile uses "+strconv.Itoa(server.Port)+", but server.properties uses "+strconv.Itoa(propertyPort)))
		}
	} else {
		checks = append(checks, healthCheck("properties", "server.properties", "warn", "Missing; Minecraft can generate it on first launch"))
	}

	worlds, datapacks, playerFiles, _ := countWorlds(serverPath, activeWorld)

	if server.MinMemoryMB <= server.MaxMemoryMB {
		checks = append(checks, healthCheck("memory", "Memory profile", "ok", strconv.Itoa(server.MinMemoryMB)+"M min / "+strconv.Itoa(server.MaxMemoryMB)+"M max"))
	} else {
		checks = append(checks, healthCheck("memory", "Memory profile", "error", "Minimum memory is greater than maximum memory"))
	}

	enabledMods := countFilesWithExt(filepath.Join(serverPath, "mods"), ".jar")
	disabledMods := countFilesWithExt(filepath.Join(serverPath, ".dashboard-disabled-mods"), ".jar")
	if server.Type == "vanilla" && enabledMods > 0 {
		checks = append(checks, healthCheck("mods", "Mod loader files", "warn", strconv.Itoa(enabledMods)+" enabled mod jars on a vanilla profile"))
	} else if enabledMods > 0 {
		checks = append(checks, healthCheck("mods", "Mod loader files", "ok", strconv.Itoa(enabledMods)+" enabled, "+strconv.Itoa(disabledMods)+" disabled"))
	}

	return serverHealth{
		Status:      healthStatus(checks),
		ActiveWorld: activeWorld,
		Counts: serverHealthCounts{
			Mods:         enabledMods,
			DisabledMods: disabledMods,
			Worlds:       worlds,
			Datapacks:    datapacks,
			PlayerFiles:  playerFiles,
		},
		Checks: checks,
	}
}

func launchTargetCheck(server store.Server, serverPath string, folderExists bool) serverHealthCheck {
	launchTarget := strings.TrimSpace(server.LaunchJar)
	if launchTarget == "" {
		return healthCheck("launch", "Launch target", "error", "No launch jar, run script, or batch file is configured")
	}
	launchPath := filepath.Clean(filepath.Join(serverPath, launchTarget))
	if launchPath != serverPath && !strings.HasPrefix(launchPath, serverPath+string(os.PathSeparator)) {
		return healthCheck("launch", "Launch target", "error", "Launch target must stay inside the server folder")
	}
	if !folderExists || !fileExists(launchPath) {
		return healthCheck("launch", "Launch target", "error", launchTarget+" was not found")
	}
	return healthCheck("launch", "Launch target", "ok", launchTarget)
}

func javaHealthCheck(server store.Server) serverHealthCheck {
	javaPath := strings.TrimSpace(server.JavaPath)
	required := requiredJavaMajor(server.MinecraftVersion)
	if javaPath == "" || javaPath == "auto" || strings.HasPrefix(javaPath, "managed:") {
		return healthCheck("java", "Java runtime", "ok", "Managed Java selected for Minecraft "+server.MinecraftVersion+"; target Java "+strconv.Itoa(required)+"+")
	}
	if looksLikePath(javaPath) {
		if fileExists(filepath.Clean(javaPath)) {
			return healthCheck("java", "Java runtime", "ok", javaPath)
		}
		return healthCheck("java", "Java runtime", "error", javaPath+" was not found")
	}
	return healthCheck("java", "Java runtime", "ok", javaPath+" from PATH; Minecraft "+server.MinecraftVersion+" target Java "+strconv.Itoa(required)+"+")
}

func requiredJavaMajor(version string) int {
	return javamanager.RequiredMajor(version)
}

func looksLikePath(value string) bool {
	return strings.ContainsAny(value, `\/`) || strings.HasSuffix(strings.ToLower(value), ".exe")
}

func healthCheck(id string, label string, state string, detail string) serverHealthCheck {
	return serverHealthCheck{ID: id, Label: label, State: state, Detail: detail}
}

func healthStatus(checks []serverHealthCheck) string {
	status := "ready"
	for _, check := range checks {
		if check.State == "error" {
			return "blocked"
		}
		if check.State == "warn" {
			status = "attention"
		}
	}
	return status
}

func stateFromBool(ok bool, failure string) string {
	if ok {
		return "ok"
	}
	return failure
}

func detailFromBool(ok bool, success string, failure string) string {
	if ok {
		return success
	}
	return failure
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func readEULAAccepted(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.EqualFold(line, "eula=true") {
			return true
		}
	}
	return false
}

func readProperties(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	properties := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if found {
			properties[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return properties
}

func propertyValue(properties map[string]string, key string, fallback string) string {
	if value := strings.TrimSpace(properties[key]); value != "" {
		return value
	}
	return fallback
}

func intProperty(properties map[string]string, key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(properties[key]))
	if err != nil {
		return fallback
	}
	return value
}

func countWorlds(serverPath string, activeWorld string) (int, int, int, bool) {
	entries, err := os.ReadDir(serverPath)
	if err != nil {
		return 0, 0, 0, false
	}
	worlds := 0
	datapacks := 0
	playerFiles := 0
	activeWorldExists := false
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		worldPath := filepath.Join(serverPath, entry.Name())
		if !fileExists(filepath.Join(worldPath, "level.dat")) {
			continue
		}
		worlds++
		if entry.Name() == activeWorld {
			activeWorldExists = true
			playerFiles = countFilesWithExt(filepath.Join(worldPath, "playerdata"), ".dat")
		}
		datapacks += countDatapacks(filepath.Join(worldPath, "datapacks"))
	}
	if activeWorldExists && worlds == 0 {
		worlds = 1
	}
	return worlds, datapacks, playerFiles, activeWorldExists
}

func countDatapacks(path string) int {
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0
	}
	total := 0
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(strings.ToLower(entry.Name()), ".zip") {
			total++
		}
	}
	return total
}

func countFilesWithExt(path string, extension string) int {
	if !dirExists(path) {
		return 0
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0
	}
	total := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), extension) {
			total++
		}
	}
	return total
}
