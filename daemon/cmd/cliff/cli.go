package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/buildinfo"
	"github.com/W1seGit/Cliff/daemon/internal/updater"
)

const (
	// detachedEnv marks a daemon started by `cliff start`; its stderr is the crash log.
	detachedEnv = "CLIFF_DETACHED"
	// tokenFileName holds the per-run secret that lets `cliff stop` ask for a clean shutdown.
	tokenFileName = "cliff.token"
	// startTimeout allows for a slow first launch, such as macOS scanning a new binary.
	startTimeout = 30 * time.Second
	// shutdownWait covers the daemon's own budget for stopping servers and saving worlds.
	shutdownWait = 45 * time.Second
)

// cliffState is written to <dataDir>/cliff-state.json by `cliff start`
// and read by `cliff status` and `cliff stop`.
type cliffState struct {
	PID        int      `json:"pid"`
	Port       int      `json:"port"`
	Host       string   `json:"host"`
	DataDir    string   `json:"dataDir"`
	ServerRoot string   `json:"serverRoot"`
	WebDir     string   `json:"webDir"`
	LogFile    string   `json:"logFile"`
	StartedAt  string   `json:"startedAt"`
	LocalURL   string   `json:"localUrl"`
	LANURLs    []string `json:"lanUrls"`
}

type daemonHealth struct {
	Daemon string `json:"daemon"`
	Self   struct {
		PID int `json:"pid"`
	} `json:"self"`
	StartedAt string   `json:"startedAt"`
	LocalURL  string   `json:"localUrl"`
	LANURLs   []string `json:"lanUrls"`
}

// daemonInfo describes a running Cliff daemon found by findDaemon.
type daemonInfo struct {
	PID   int
	Port  int
	State *cliffState
	// Responding is true when the daemon answered a health check just now.
	Responding bool
}

// installRoot returns the directory containing the cliff binary.
// This is the install root (e.g. ~/.cliff).
func installRoot() string {
	exe, err := os.Executable()
	if err != nil {
		cwd, _ := os.Getwd()
		return cwd
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil && resolved != "" {
		exe = resolved
	}
	return filepath.Dir(exe)
}

// defaultDataDir returns the default data directory relative to the install root.
func defaultDataDir() string {
	return resolveCLIPath(os.Getenv("CLIFF_DATA_DIR"), filepath.Join(installRoot(), "data"))
}

func resolveCLIPath(value string, fallback string) string {
	if value == "" {
		value = fallback
	}
	if path, err := filepath.Abs(value); err == nil {
		return path
	}
	return value
}

// stateFilePath returns the path to the cliff-state.json file.
func stateFilePath(dataDir string) string {
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	return filepath.Join(dataDir, "cliff-state.json")
}

// pidFilePath returns the path to the cliff.pid file (for shell script compat).
func pidFilePath(dataDir string) string {
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	return filepath.Join(dataDir, "cliff.pid")
}

func tokenFilePath(dataDir string) string {
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	return filepath.Join(dataDir, tokenFileName)
}

// ---- stop token (used by the daemon and by `cliff stop`) ----

func newShutdownToken() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func writeShutdownToken(dataDir string, token string) error {
	if token == "" {
		return nil
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(tokenFilePath(dataDir), []byte(token), 0o600)
}

func removeShutdownToken(dataDir string) {
	_ = os.Remove(tokenFilePath(dataDir))
}

// ---- cliff start ----

func runStart(args []string) {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	var port int
	var host string
	var dataDir string
	var serverRoot string
	var webDir string
	fs.IntVar(&port, "port", getenvInt("CLIFF_PORT", 8080), "HTTP port to bind")
	fs.IntVar(&port, "p", getenvInt("CLIFF_PORT", 8080), "HTTP port to bind (shorthand)")
	fs.StringVar(&host, "host", getenv("CLIFF_HOST", "0.0.0.0"), "host interface to bind")
	fs.StringVar(&dataDir, "data-dir", os.Getenv("CLIFF_DATA_DIR"), "panel data directory (default: <install-dir>/data)")
	fs.StringVar(&serverRoot, "server-root", os.Getenv("CLIFF_SERVER_ROOT"), "Minecraft server storage root")
	fs.StringVar(&webDir, "web-dir", os.Getenv("CLIFF_WEB_DIR"), "static dashboard directory")
	fs.Parse(args)

	root := installRoot()

	// Resolve defaults relative to the install root.
	dataDir = resolveCLIPath(dataDir, filepath.Join(root, "data"))
	serverRoot = resolveCLIPath(serverRoot, filepath.Join(root, "servers"))
	webDir = resolveCLIPath(webDir, filepath.Join(root, "web"))

	logFile := filepath.Join(dataDir, "logs", "cliff.log")
	errorLogFile := filepath.Join(dataDir, "logs", "cliff-error.log")

	// Refuse to start a second daemon, but only when the PID really is Cliff.
	if existing := findDaemonByFiles(dataDir); existing != nil {
		fmt.Fprintf(os.Stderr, "Cliff is already running (PID %d) on port %d.\n", existing.PID, existing.Port)
		fmt.Fprintf(os.Stderr, "Run 'cliff stop' first, or use 'cliff status' to check.\n")
		os.Exit(1)
	}

	// Ensure directories exist.
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create data directory: %s\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(logFile), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create log directory: %s\n", err)
		os.Exit(1)
	}

	// The daemon writes cliff.log itself (with rotation). Whatever reaches its
	// stdout or stderr, such as a crash, goes to the error log.
	errOut, err := os.OpenFile(errorLogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open error log file: %s\n", err)
		os.Exit(1)
	}

	// Build the daemon command: `cliff daemon --host ... --port ... ...`
	self, _ := os.Executable()
	daemonArgs := []string{"daemon",
		"--host", host,
		"--port", fmt.Sprint(port),
		"--data-dir", dataDir,
		"--server-root", serverRoot,
		"--web-dir", webDir,
		"--log-file", logFile,
	}

	cmd := exec.Command(self, daemonArgs...)
	cmd.Stdout = errOut
	cmd.Stderr = errOut
	cmd.Dir = root
	cmd.Env = append(os.Environ(), detachedEnv+"=1")

	// Detach from the terminal.
	setDetachFlags(cmd)

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start daemon: %s\n", err)
		os.Exit(1)
	}
	_ = errOut.Close()

	pid := cmd.Process.Pid

	// Reap the child in the background so a crash during startup is seen as an
	// exit, not a zombie that still looks alive.
	exited := make(chan struct{})
	go func() {
		_, _ = cmd.Process.Wait()
		close(exited)
	}()

	startedAt := time.Now().UTC()
	lanURLs := detectLANURLs(port)
	state := cliffState{
		PID:        pid,
		Port:       port,
		Host:       host,
		DataDir:    dataDir,
		ServerRoot: serverRoot,
		WebDir:     webDir,
		LogFile:    logFile,
		StartedAt:  startedAt.Format(time.RFC3339),
		LocalURL:   fmt.Sprintf("http://localhost:%d", port),
		LANURLs:    lanURLs,
	}

	// Wait until this exact process answers health checks before writing state.
	deadline := time.Now().Add(startTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			fmt.Fprintf(os.Stderr, "Cliff failed to start. Check %s and %s for details.\n", logFile, errorLogFile)
			os.Exit(1)
		default:
		}
		if health := readDaemonHealth(port); health != nil {
			if health.Self.PID == pid {
				_ = os.WriteFile(pidFilePath(dataDir), []byte(fmt.Sprintf("%d\n", pid)), 0o644)
				writeState(dataDir, state)
				printStarted(state, pid, logFile)
				return
			}
			if health.Self.PID > 0 {
				_ = killProcess(pid)
				fmt.Fprintf(os.Stderr, "Cliff is already running on port %d (PID %d).\n", port, health.Self.PID)
				fmt.Fprintln(os.Stderr, "Run 'cliff stop' first, or choose another port with 'cliff start -p <port>'.")
				os.Exit(1)
			}
		}
		time.Sleep(200 * time.Millisecond)
	}

	_ = killProcess(pid)
	fmt.Fprintf(os.Stderr, "Cliff did not become ready within %s. Check %s and %s for details.\n", startTimeout, logFile, errorLogFile)
	os.Exit(1)
}

func printStarted(state cliffState, pid int, logFile string) {
	fmt.Printf("Cliff started (PID %d)\n", pid)
	fmt.Printf("  Local:   %s\n", state.LocalURL)
	for _, url := range state.LANURLs {
		fmt.Printf("  Network: %s\n", url)
	}
	fmt.Printf("  Logs:    %s\n", logFile)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  cliff status   Check status\n")
	fmt.Printf("  cliff logs     View daemon logs\n")
	fmt.Printf("  cliff stop     Stop Cliff\n")
}

// ---- finding and stopping the daemon ----

// findDaemon locates a running Cliff daemon. It tries, in order: the state
// file, the PID file, then a health probe on the saved and default ports, so a
// daemon started another way (run.sh, a different data dir) is still found.
// A stale state or PID file is cleaned up. It returns nil when nothing is running.
func findDaemon(dataDir string) *daemonInfo {
	if info := findDaemonByFiles(dataDir); info != nil {
		return info
	}
	for _, port := range candidatePorts() {
		if health := readDaemonHealth(port); health != nil && health.Self.PID > 0 {
			return &daemonInfo{PID: health.Self.PID, Port: port, State: stateFromHealth(port, health, dataDir), Responding: true}
		}
	}
	clearDaemonFiles(dataDir)
	return nil
}

// findDaemonByFiles only looks at this data directory's state and PID files.
// `cliff start` uses it so a second install on another port is not blocked by
// a daemon that belongs to a different data directory.
func findDaemonByFiles(dataDir string) *daemonInfo {
	if state := readState(dataDir); state != nil {
		if state.Port > 0 {
			if health := readDaemonHealth(state.Port); health != nil && health.Self.PID > 0 {
				return &daemonInfo{PID: health.Self.PID, Port: state.Port, State: state, Responding: true}
			}
		}
		// Not answering yet (or hung): trust the PID only if it is really Cliff.
		if processAlive(state.PID) && looksLikeCliff(state.PID) {
			return &daemonInfo{PID: state.PID, Port: state.Port, State: state}
		}
	}
	if pid := readPIDFile(dataDir); pid > 0 && processAlive(pid) && looksLikeCliff(pid) {
		return &daemonInfo{PID: pid, State: readState(dataDir)}
	}
	return nil
}

// defaultProbePorts are tried after CLIFF_PORT when looking for a daemon with no state file.
var defaultProbePorts = []int{8080}

func candidatePorts() []int {
	ports := []int{}
	seen := map[int]bool{}
	for _, port := range append([]int{getenvInt("CLIFF_PORT", 0)}, defaultProbePorts...) {
		if port > 0 && port < 65536 && !seen[port] {
			seen[port] = true
			ports = append(ports, port)
		}
	}
	return ports
}

func stateFromHealth(port int, health *daemonHealth, dataDir string) *cliffState {
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	return &cliffState{
		PID:       health.Self.PID,
		Port:      port,
		DataDir:   dataDir,
		StartedAt: health.StartedAt,
		LocalURL:  health.LocalURL,
		LANURLs:   health.LANURLs,
	}
}

func readPIDFile(dataDir string) int {
	raw, err := os.ReadFile(pidFilePath(dataDir))
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	return pid
}

func clearDaemonFiles(dataDir string) {
	_ = os.Remove(stateFilePath(dataDir))
	_ = os.Remove(pidFilePath(dataDir))
	_ = os.Remove(tokenFilePath(dataDir))
}

// looksLikeCliff guards against a recycled PID that now belongs to another program.
func looksLikeCliff(pid int) bool {
	return strings.Contains(strings.ToLower(pidCommandName(pid)), "cliff")
}

// requestGracefulShutdown asks the daemon to stop itself over loopback. It
// works the same on every platform and lets the daemon stop Minecraft servers
// and save worlds. It reports whether the daemon accepted the request.
func requestGracefulShutdown(port int, dataDir string) bool {
	if port <= 0 {
		return false
	}
	token, err := os.ReadFile(tokenFilePath(dataDir))
	if err != nil || len(bytes.TrimSpace(token)) == 0 {
		return false
	}
	request, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/api/internal/shutdown", port), nil)
	if err != nil {
		return false
	}
	request.Header.Set("X-Cliff-Token", strings.TrimSpace(string(token)))
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode == http.StatusAccepted
}

// stopDaemon shuts the daemon down cleanly, waiting long enough for it to stop
// Minecraft servers and save worlds, and only force-kills as a last resort.
func stopDaemon(info *daemonInfo, dataDir string) error {
	graceful := requestGracefulShutdown(info.Port, dataDir)
	if !graceful {
		if err := stopProcess(info.PID); err != nil && processAlive(info.PID) {
			return fmt.Errorf("could not signal PID %d: %w", info.PID, err)
		}
	}

	announced := false
	deadline := time.Now().Add(shutdownWait)
	start := time.Now()
	for time.Now().Before(deadline) {
		if !processAlive(info.PID) {
			break
		}
		if !announced && time.Since(start) > 3*time.Second {
			fmt.Println("Waiting for Cliff to stop servers and save worlds...")
			announced = true
		}
		time.Sleep(200 * time.Millisecond)
	}

	if processAlive(info.PID) {
		fmt.Fprintln(os.Stderr, "Cliff did not stop in time; forcing it to stop. A running Minecraft server may need a moment to release its port.")
		_ = killProcess(info.PID)
		time.Sleep(time.Second)
		if processAlive(info.PID) {
			return fmt.Errorf("PID %d is still running", info.PID)
		}
	}
	clearDaemonFiles(dataDir)
	return nil
}

// ---- cliff stop ----

func runStop(args []string) {
	fs := flag.NewFlagSet("stop", flag.ExitOnError)
	var dataDir string
	fs.StringVar(&dataDir, "data-dir", "", "panel data directory (default: <install-dir>/data)")
	fs.Parse(args)

	info := findDaemon(dataDir)
	if info == nil {
		fmt.Println("Cliff is not running.")
		return
	}

	fmt.Printf("Stopping Cliff (PID %d)...\n", info.PID)
	if err := stopDaemon(info, dataDir); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to stop Cliff: %s\n", err)
		os.Exit(1)
	}
	fmt.Println("Cliff stopped.")
}

// ---- cliff status ----

func runStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	var dataDir string
	fs.StringVar(&dataDir, "data-dir", "", "panel data directory (default: <install-dir>/data)")
	fs.Parse(args)

	info := findDaemon(dataDir)
	if info == nil {
		fmt.Println("Cliff is not running.")
		fmt.Println("Run 'cliff start' to start the daemon.")
		return
	}

	build := buildinfo.Current()
	if info.Responding {
		fmt.Printf("Cliff %s — running\n", build.Version)
	} else {
		fmt.Printf("Cliff %s — process is up but the dashboard is not answering yet\n", build.Version)
	}
	fmt.Printf("  PID:         %d\n", info.PID)
	state := info.State
	if info.Port > 0 {
		fmt.Printf("  Port:        %d\n", info.Port)
	}
	if state == nil {
		return
	}
	if state.LocalURL != "" {
		fmt.Printf("  Local URL:   %s\n", state.LocalURL)
	}
	for _, url := range state.LANURLs {
		fmt.Printf("  Network URL: %s\n", url)
	}

	// Calculate uptime.
	if state.StartedAt != "" {
		startedAt, err := time.Parse(time.RFC3339, state.StartedAt)
		if err == nil {
			uptime := time.Since(startedAt).Round(time.Second)
			fmt.Printf("  Uptime:      %s\n", formatUptime(uptime))
			fmt.Printf("  Started:     %s\n", startedAt.Local().Format("2006-01-02 15:04:05"))
		}
	}

	if state.DataDir != "" {
		fmt.Printf("  Data dir:    %s\n", state.DataDir)
	}
	if state.ServerRoot != "" {
		fmt.Printf("  Server root: %s\n", state.ServerRoot)
	}
	if state.LogFile != "" {
		fmt.Printf("  Log file:    %s\n", state.LogFile)
	}
}

// ---- cliff logs ----

func runLogs(args []string) {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	var dataDir string
	var tail int
	var follow bool
	fs.StringVar(&dataDir, "data-dir", "", "panel data directory (default: <install-dir>/data)")
	fs.IntVar(&tail, "tail", 80, "number of recent log lines to print")
	fs.IntVar(&tail, "n", 80, "number of recent log lines to print (shorthand)")
	fs.BoolVar(&follow, "follow", false, "keep printing new log lines (Ctrl+C to stop)")
	fs.BoolVar(&follow, "f", false, "keep printing new log lines (shorthand)")
	fs.Parse(args)

	state := readState(dataDir)
	logFile := ""
	if state != nil && state.LogFile != "" {
		logFile = state.LogFile
	} else {
		resolvedDataDir := dataDir
		if resolvedDataDir == "" {
			resolvedDataDir = defaultDataDir()
		}
		logFile = filepath.Join(resolvedDataDir, "logs", "cliff.log")
	}

	file, err := os.Open(logFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "No daemon logs found at %s.\n", logFile)
		fmt.Fprintln(os.Stderr, "Run 'cliff start' to start Cliff, then try 'cliff logs' again.")
		os.Exit(1)
	}
	defer file.Close()

	// Only the end of the file matters, so never read a huge log in full.
	const window = 256 * 1024
	info, _ := file.Stat()
	size := int64(0)
	if info != nil {
		size = info.Size()
	}
	offset := int64(0)
	if size > window {
		offset = size - window
	}
	buffer := make([]byte, size-offset)
	if _, err := file.ReadAt(buffer, offset); err != nil && err != io.EOF {
		fmt.Fprintf(os.Stderr, "Could not read %s: %s\n", logFile, err)
		os.Exit(1)
	}

	fmt.Printf("Cliff daemon logs: %s\n\n", logFile)
	for _, line := range tailLines(string(buffer), tail) {
		fmt.Println(line)
	}
	if !follow {
		return
	}

	position := size
	for {
		time.Sleep(500 * time.Millisecond)
		current, err := os.Stat(logFile)
		if err != nil {
			continue
		}
		if current.Size() < position {
			// The log was rotated or truncated; start over from the top.
			position = 0
			file.Close()
			if file, err = os.Open(logFile); err != nil {
				continue
			}
		}
		if current.Size() == position {
			continue
		}
		chunk := make([]byte, current.Size()-position)
		n, _ := file.ReadAt(chunk, position)
		position += int64(n)
		fmt.Print(string(chunk[:n]))
	}
}

// tailLines returns the last n lines of text (all lines when n <= 0).
func tailLines(text string, n int) []string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// ---- cliff update ----

func runUpdate(args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	var checkOnly bool
	var dataDir string
	var webDir string
	fs.BoolVar(&checkOnly, "check", false, "only check for updates, don't apply")
	fs.StringVar(&dataDir, "data-dir", os.Getenv("CLIFF_DATA_DIR"), "panel data directory (default: <install-dir>/data)")
	fs.StringVar(&webDir, "web-dir", os.Getenv("CLIFF_WEB_DIR"), "static dashboard directory")
	fs.Parse(args)

	root := installRoot()
	dataDirProvided := dataDir != ""
	webDirProvided := webDir != ""
	dataDir = resolveCLIPath(dataDir, filepath.Join(root, "data"))
	webDir = resolveCLIPath(webDir, filepath.Join(root, "web"))
	info := findDaemon(dataDir)
	var state *cliffState
	if info != nil {
		state = info.State
	}
	if state != nil {
		if !dataDirProvided {
			dataDir = resolveCLIPath(state.DataDir, dataDir)
		}
		if !webDirProvided {
			webDir = resolveCLIPath(state.WebDir, webDir)
		}
	}

	self, _ := os.Executable()
	mgr := updater.NewManager(self, webDir, dataDir)

	fmt.Println("Checking for updates...")
	result := mgr.CheckNow(context.Background())

	fmt.Printf("  Current version: %s\n", result.CurrentVersion)
	fmt.Printf("  Latest version:  %s\n", result.LatestVersion)

	if result.Error != "" {
		fmt.Fprintf(os.Stderr, "  Error: %s\n", result.Error)
		os.Exit(1)
	}

	if !result.UpdateAvailable {
		fmt.Println("Cliff is up to date.")
		return
	}

	fmt.Printf("  Released:        %s\n", result.BuiltAt)
	if result.ArchiveSize > 0 {
		fmt.Printf("  Download size:   %s\n", formatBytes(result.ArchiveSize))
	}

	if checkOnly {
		fmt.Println("\nUpdate available. Run 'cliff update' to install it.")
		return
	}

	wasRunning := info != nil

	// Bring the daemon back with the same settings it had.
	restartDaemon := func() {
		restartState := cliffState{DataDir: dataDir, WebDir: webDir}
		if state != nil {
			restartState = *state
		}
		restartDataDir := resolveCLIPath(restartState.DataDir, dataDir)
		restartServerRoot := resolveCLIPath(restartState.ServerRoot, filepath.Join(root, "servers"))
		restartWebDir := resolveCLIPath(restartState.WebDir, webDir)
		restartHost := restartState.Host
		if restartHost == "" {
			restartHost = "0.0.0.0"
		}
		restartPort := restartState.Port
		if restartPort < 1 || restartPort > 65535 {
			restartPort = info.Port
		}
		if restartPort < 1 || restartPort > 65535 {
			restartPort = 8080
		}
		restartArgs := []string{
			"start", "--host", restartHost, "--port", fmt.Sprint(restartPort),
			"--data-dir", restartDataDir, "--server-root", restartServerRoot, "--web-dir", restartWebDir,
		}
		updater.RestartAsync(self, restartArgs, 500*time.Millisecond)
		time.Sleep(2 * time.Second)
	}

	// Download and verify first. Only once the new version is safely on disk is
	// the daemon stopped and the database copied, so a failed download changes nothing.
	stopped := false
	fmt.Println("Downloading update...")
	applyResult, err := mgr.Apply(context.Background(), updater.ApplyHooks{
		BeforeSwap: func(context.Context) error {
			backup := updater.PreUpdateBackupPath(dataDir, buildinfo.Current().Version)
			if wasRunning {
				fmt.Println("Stopping running daemon...")
				if err := stopDaemon(info, dataDir); err != nil {
					return fmt.Errorf("could not stop the daemon (PID %d): %w", info.PID, err)
				}
				stopped = true
			}
			if err := updater.CopyDatabase(filepath.Join(dataDir, "dashboard.sqlite"), backup); err != nil {
				return fmt.Errorf("could not back up the database: %w", err)
			}
			updater.PruneBackups(updater.PreUpdateBackupDir(dataDir), 3)
			fmt.Println("Applying update...")
			return nil
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Update failed: %s\n", err)
		if stopped {
			fmt.Fprintln(os.Stderr, "Restarting the previous version...")
			restartDaemon()
		}
		os.Exit(1)
	}

	if applyResult.Success {
		fmt.Printf("Update successful: %s\n", applyResult.Message)
		fmt.Println("If the new version misbehaves, 'cliff rollback' restores the previous one.")
		if wasRunning {
			fmt.Println("Restarting daemon...")
			restartDaemon()
		} else {
			fmt.Println("Cliff was not running before the update; start it with 'cliff start'.")
		}
	} else {
		fmt.Fprintf(os.Stderr, "Update failed: %s\n", applyResult.Message)
		if stopped {
			restartDaemon()
		}
		os.Exit(1)
	}
}

// ---- cliff rollback ----

func runRollback(args []string) {
	fs := flag.NewFlagSet("rollback", flag.ExitOnError)
	var yes bool
	var dataDir string
	var webDir string
	fs.BoolVar(&yes, "yes", false, "skip confirmation prompt")
	fs.BoolVar(&yes, "y", false, "skip confirmation prompt (shorthand)")
	fs.StringVar(&dataDir, "data-dir", os.Getenv("CLIFF_DATA_DIR"), "panel data directory (default: <install-dir>/data)")
	fs.StringVar(&webDir, "web-dir", os.Getenv("CLIFF_WEB_DIR"), "static dashboard directory")
	fs.Parse(args)

	root := installRoot()
	dataDir = resolveCLIPath(dataDir, filepath.Join(root, "data"))
	webDir = resolveCLIPath(webDir, filepath.Join(root, "web"))
	self, _ := os.Executable()
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}

	info := findDaemon(dataDir)
	if info != nil && info.State != nil && info.State.WebDir != "" {
		webDir = resolveCLIPath(info.State.WebDir, webDir)
	}

	if !yes {
		fmt.Println("This puts back the version of Cliff you had before the last update.")
		fmt.Println("Your servers, worlds and settings are not changed.")
		if !confirmPrompt("Type 'yes' to continue: ") {
			fmt.Println("Rollback cancelled. Nothing was changed.")
			return
		}
	}

	if info != nil {
		fmt.Printf("Stopping Cliff (PID %d)...\n", info.PID)
		if err := stopDaemon(info, dataDir); err != nil {
			fmt.Fprintf(os.Stderr, "Could not stop Cliff: %s. Nothing was changed.\n", err)
			os.Exit(1)
		}
	}

	restored, err := updater.Rollback(self, webDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Rollback failed: %s\n", err)
		os.Exit(1)
	}
	for _, path := range restored {
		fmt.Printf("Restored: %s\n", path)
	}
	fmt.Printf("Database copies from before updates are kept in %s.\n", updater.PreUpdateBackupDir(dataDir))
	if info != nil {
		fmt.Println("Start Cliff again with: cliff start")
	}
}

// ---- helpers ----

func readState(dataDir string) *cliffState {
	path := stateFilePath(dataDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var state cliffState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}
	return &state
}

func writeState(dataDir string, state cliffState) {
	path := stateFilePath(dataDir)
	data, _ := json.MarshalIndent(state, "", "  ")
	_ = os.WriteFile(path, data, 0o644)
}

func readDaemonHealth(port int) *daemonHealth {
	client := &http.Client{Timeout: 800 * time.Millisecond}
	response, err := client.Get(fmt.Sprintf("http://localhost:%d/api/health", port))
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil
	}
	var health daemonHealth
	if err := json.NewDecoder(response.Body).Decode(&health); err != nil {
		return nil
	}
	if health.Daemon != "cliff" {
		return nil
	}
	return &health
}

// processAlive reports whether pid is a live (not exited, not zombie) process.
func processAlive(pid int) bool {
	return pidExists(pid) && !pidIsZombie(pid)
}

func stopProcess(pid int) error {
	return syscallKill(pid, syscall.SIGTERM)
}

func killProcess(pid int) error {
	return syscallKill(pid, syscall.SIGKILL)
}

func detectLANURLs(port int) []string {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	urls := []string{}
	for _, addr := range addresses {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP == nil || ipNet.IP.IsLoopback() || ipNet.IP.IsLinkLocalUnicast() {
			continue
		}
		ip := ipNet.IP.To4()
		if ip == nil {
			continue
		}
		// Skip 169.254.x.x (APIPA/link-local)
		if ip[0] == 169 && ip[1] == 254 {
			continue
		}
		urls = append(urls, fmt.Sprintf("http://%s:%d", ip.String(), port))
	}
	return urls
}

func formatUptime(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh %dm", hours, minutes)
}

func formatBytes(bytes int64) string {
	if bytes >= 1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	}
	if bytes >= 1024 {
		return fmt.Sprintf("%d KB", bytes/1024)
	}
	return fmt.Sprintf("%d B", bytes)
}

func homeDir() string {
	dir, _ := os.UserHomeDir()
	return dir
}
