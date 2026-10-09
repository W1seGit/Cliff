package httpserver

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/config"
	"github.com/W1seGit/Cliff/daemon/internal/logbuf"
	"github.com/W1seGit/Cliff/daemon/internal/process"
	"github.com/W1seGit/Cliff/daemon/internal/store"
	"github.com/W1seGit/Cliff/daemon/internal/updater"
)

type Options struct {
	Config           config.Config
	Store            *store.Store
	Process          *process.Manager
	StartedAt        time.Time
	SchedulerContext context.Context
	LogBuffer        *logbuf.Buffer
	Updater          *updater.Manager
	// Shutdown and ShutdownToken enable POST /api/internal/shutdown for `cliff stop`.
	Shutdown      func()
	ShutdownToken string
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
		playitBuild:   newPlayitBuildManager(),
		logBuffer:     options.LogBuffer,
		updater:       options.Updater,
		loginLimiter:  newLoginLimiter(),
		notifier:      newWebhookNotifier(options.Store),
		restarter:     newCrashRestarter(),

		shutdown:      options.Shutdown,
		shutdownToken: options.ShutdownToken,

		createProgress: newCreateProgressStore(),
	}
	lifecycleContext := options.SchedulerContext
	if lifecycleContext == nil {
		lifecycleContext = context.Background()
	}
	manager.SetLifecycleHandler(func(event process.LifecycleEvent) { api.handleLifecycle(lifecycleContext, event) })
	if options.SchedulerContext != nil {
		go api.runScheduler(options.SchedulerContext)
		go api.resumeAfterRestart(options.SchedulerContext)
	}

	mux.HandleFunc("GET /api/health", api.health)
	mux.HandleFunc("POST /api/internal/shutdown", api.shutdownDaemon)
	mux.HandleFunc("GET /api/internal/running-servers", api.internalRunningServers)
	mux.HandleFunc("POST /api/internal/resume-servers", api.internalResumeServers)
	mux.HandleFunc("GET /api/auth/me", api.authMe)
	mux.HandleFunc("POST /api/auth/setup", api.authSetup)
	mux.HandleFunc("POST /api/auth/login", api.authLogin)
	mux.HandleFunc("POST /api/auth/logout", api.authLogout)
	mux.HandleFunc("PATCH /api/auth/account", api.requireUser(api.authAccount))
	mux.HandleFunc("POST /api/auth/2fa/setup", api.requireUser(api.twoFactorSetup))
	mux.HandleFunc("POST /api/auth/2fa/enable", api.requireUser(api.twoFactorEnable))
	mux.HandleFunc("POST /api/auth/2fa/disable", api.requireUser(api.twoFactorDisable))
	mux.HandleFunc("GET /api/users", api.requireAdmin(api.users))
	mux.HandleFunc("POST /api/users", api.requireAdmin(api.createUser))
	mux.HandleFunc("PATCH /api/users/{id}", api.requireAdmin(api.updateUser))
	mux.HandleFunc("DELETE /api/users/{id}", api.requireAdmin(api.deleteUser))
	mux.HandleFunc("POST /api/users/{id}/reset-2fa", api.requireAdmin(api.resetUserTwoFactor))
	mux.HandleFunc("GET /api/minecraft/versions", api.requireUser(api.minecraftVersions))
	mux.HandleFunc("GET /api/java/runtimes", api.requireAdmin(api.javaRuntimes))
	mux.HandleFunc("POST /api/java/runtimes", api.requireAdmin(api.installJavaRuntime))
	mux.HandleFunc("DELETE /api/java/runtimes", api.requireAdmin(api.uninstallJavaRuntime))
	mux.HandleFunc("GET /api/public-access/playit/agent", api.requireAdmin(api.playitAgentStatus))
	mux.HandleFunc("POST /api/public-access/playit/agent/install", api.requireAdmin(api.installPlayitAgent))
	mux.HandleFunc("POST /api/public-access/playit/agent/check-deps", api.requireAdmin(api.checkPlayitDeps))
	mux.HandleFunc("POST /api/public-access/playit/agent/install-deps", api.requireAdmin(api.installPlayitDeps))
	mux.HandleFunc("POST /api/public-access/playit/agent/start", api.requireAdmin(api.startPlayitAgent))
	mux.HandleFunc("POST /api/public-access/playit/agent/stop", api.requireAdmin(api.stopPlayitAgent))
	mux.HandleFunc("POST /api/public-access/playit/agent/uninstall", api.requireAdmin(api.uninstallPlayitAgent))
	mux.HandleFunc("POST /api/public-access/playit/agent/reset", api.requireAdmin(api.resetPlayitAgent))
	mux.HandleFunc("GET /api/jvm/presets", api.requireUser(api.jvmPresets))
	mux.HandleFunc("GET /api/webhooks", api.requireAdmin(api.webhooks))
	mux.HandleFunc("POST /api/webhooks", api.requireAdmin(api.createWebhook))
	mux.HandleFunc("PATCH /api/webhooks/{id}", api.requireAdmin(api.updateWebhook))
	mux.HandleFunc("DELETE /api/webhooks/{id}", api.requireAdmin(api.deleteWebhook))
	mux.HandleFunc("POST /api/webhooks/{id}/test", api.requireAdmin(api.testWebhook))
	mux.HandleFunc("GET /api/settings", api.requireUser(api.settings))
	mux.HandleFunc("PUT /api/settings", api.requireAdmin(api.updateSettings))
	mux.HandleFunc("GET /api/daemon-logs", api.requireAdmin(api.daemonLogs))
	mux.HandleFunc("GET /api/servers", api.requireUser(api.servers))
	mux.HandleFunc("POST /api/servers", api.requireAdmin(api.createServer))
	mux.HandleFunc("GET /api/create-progress/{id}", api.requireAdmin(api.createProgressHandler))
	mux.HandleFunc("GET /api/servers/{id}", api.requirePerm(store.PermView, api.serverDetail))
	mux.HandleFunc("GET /api/servers/{id}/health", api.requirePerm(store.PermView, api.serverHealth))
	mux.HandleFunc("PATCH /api/servers/{id}", api.requirePerm(store.PermSettings, api.updateServer))
	mux.HandleFunc("DELETE /api/servers/{id}", api.requireAdmin(api.deleteServer))
	mux.HandleFunc("GET /api/servers/{id}/public-access", api.requirePerm(store.PermView, api.serverPublicAccess))
	mux.HandleFunc("PUT /api/servers/{id}/public-access", api.requirePerm(store.PermSettings, api.saveServerPublicAccess))
	mux.HandleFunc("DELETE /api/servers/{id}/public-access", api.requirePerm(store.PermSettings, api.deleteServerPublicAccess))
	mux.HandleFunc("GET /api/servers/{id}/properties", api.requirePerm(store.PermSettings, api.serverProperties))
	mux.HandleFunc("PUT /api/servers/{id}/properties", api.requirePerm(store.PermSettings, api.updateServerProperties))
	mux.HandleFunc("GET /api/servers/{id}/players", api.requirePerm(store.PermPlayers, api.players))
	mux.HandleFunc("POST /api/servers/{id}/players", api.requirePerm(store.PermPlayers, api.updatePlayers))
	mux.HandleFunc("GET /api/servers/{id}/files", api.requirePerm(store.PermFiles, api.files))
	mux.HandleFunc("POST /api/servers/{id}/files", api.requirePerm(store.PermFiles, api.fileAction))
	mux.HandleFunc("GET /api/servers/{id}/worlds", api.requirePerm(store.PermWorlds, api.worlds))
	mux.HandleFunc("POST /api/servers/{id}/worlds", api.requirePerm(store.PermWorlds, api.worldAction))
	mux.HandleFunc("GET /api/servers/{id}/mods", api.requirePerm(store.PermMods, api.mods))
	mux.HandleFunc("POST /api/servers/{id}/mods", api.requirePerm(store.PermMods, api.modAction))
	mux.HandleFunc("POST /api/servers/{id}/modpack", api.requirePerm(store.PermMods, api.importMrpack))
	mux.HandleFunc("GET /api/runtime", api.requireUser(api.runtime))
	mux.HandleFunc("GET /api/updates/check", api.requireAdmin(api.updatesCheck))
	mux.HandleFunc("POST /api/updates/apply", api.requireAdmin(api.updatesApply))
	mux.HandleFunc("POST /api/daemon/stop", api.requireAdmin(api.daemonStop))
	mux.HandleFunc("POST /api/daemon/restart", api.requireAdmin(api.daemonRestart))
	mux.HandleFunc("GET /api/updates/progress", api.requireAdmin(api.updatesProgress))
	mux.HandleFunc("GET /api/updates/servers", api.requireAdmin(api.updatesServers))
	mux.HandleFunc("GET /api/updates/safety", api.requireAdmin(api.updatesSafety))
	mux.HandleFunc("DELETE /api/updates/safety", api.requireAdmin(api.updatesClearSafety))
	mux.HandleFunc("GET /api/updates/last-result", api.requireAdmin(api.updatesLastResult))
	mux.HandleFunc("DELETE /api/updates/last-result", api.requireAdmin(api.updatesDismissLastResult))
	mux.HandleFunc("GET /api/servers/{id}/usage", api.requirePerm(store.PermView, api.serverUsage))
	mux.HandleFunc("POST /api/servers/{id}/upgrade", api.requirePerm(store.PermSettings, api.upgradeServer))
	mux.HandleFunc("POST /api/servers/{id}/start", api.requirePerm(store.PermPower, api.start))
	mux.HandleFunc("POST /api/servers/{id}/stop", api.requirePerm(store.PermPower, api.stop))
	mux.HandleFunc("POST /api/servers/{id}/restart", api.requirePerm(store.PermPower, api.restart))
	mux.HandleFunc("GET /api/servers/{id}/command", api.requirePerm(store.PermConsole, api.commandPresets))
	mux.HandleFunc("POST /api/servers/{id}/command", api.requirePerm(store.PermConsole, api.command))
	mux.HandleFunc("GET /api/servers/{id}/backups", api.requirePerm(store.PermBackups, api.backups))
	mux.HandleFunc("GET /api/servers/{id}/backups/diff", api.requirePerm(store.PermBackups, api.backupDiff))
	mux.HandleFunc("POST /api/servers/{id}/backups", api.requirePerm(store.PermBackups, api.backupAction))
	mux.HandleFunc("GET /api/servers/{id}/logs", api.requirePerm(store.PermConsole, api.logs))
	mux.HandleFunc("GET /api/servers/{id}/console", api.requirePerm(store.PermView, api.console))
	mux.Handle("/", spaFileServer(options.Config.WebDir))

	return withErrorLogging(withCommonHeaders(options.Config.AllowedOrigins, mux))
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
	playitBuild   *playitBuildManager
	logBuffer     *logbuf.Buffer
	updater       *updater.Manager
	loginLimiter  *loginLimiter
	notifier      *webhookNotifier
	restarter     *crashRestarter

	shutdown      func()
	shutdownToken string

	createProgress *createProgressStore
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
