package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/buildinfo"
	"github.com/W1seGit/Cliff/daemon/internal/updater"
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
	Daemon        string   `json:"daemon"`
	StartedAt     string   `json:"startedAt"`
	LocalURL      string   `json:"localUrl"`
	LANURLs       []string `json:"lanUrls"`
	UptimeSeconds int64    `json:"uptimeSeconds"`
	Self          struct {
		PID int `json:"pid"`
	} `json:"self"`
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
	return filepath.Join(installRoot(), "data")
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
	fs.StringVar(&dataDir, "data-dir", "", "panel data directory (default: <install-dir>/data)")
	fs.StringVar(&serverRoot, "server-root", "", "Minecraft server storage root")
	fs.StringVar(&webDir, "web-dir", "", "static dashboard directory")
	fs.Parse(args)

	root := installRoot()

	// Resolve defaults relative to the install root.
	if dataDir == "" {
		dataDir = filepath.Join(root, "data")
	}
	if serverRoot == "" {
		serverRoot = filepath.Join(root, "servers")
	}
	if webDir == "" {
		webDir = filepath.Join(root, "web")
	}

	logFile := filepath.Join(dataDir, "logs", "cliff.log")
	errorLogFile := filepath.Join(dataDir, "logs", "cliff-error.log")

	// Check if already running.
	if state := readState(dataDir); state != nil && processAlive(state.PID) {
		fmt.Fprintf(os.Stderr, "Cliff is already running (PID %d) on port %d.\n", state.PID, state.Port)
		fmt.Fprintf(os.Stderr, "Run 'cliff stop' first, or use 'cliff status' to check.\n")
		os.Exit(1)
	} else if state := recoverStateFromHealth(port, host, dataDir, serverRoot, webDir); state != nil {
		writeState(dataDir, *state)
		_ = os.WriteFile(pidFilePath(dataDir), []byte(fmt.Sprintf("%d\n", state.PID)), 0o644)
		fmt.Fprintf(os.Stderr, "Cliff is already running (PID %d) on port %d.\n", state.PID, state.Port)
		fmt.Fprintf(os.Stderr, "Run 'cliff status' to check status, or 'cliff stop' to stop.\n")
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

	// Open log files.
	logOut, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open log file: %s\n", err)
		os.Exit(1)
	}
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
	cmd.Stdout = logOut
	cmd.Stderr = errOut
	cmd.Dir = root

	// Detach from the terminal.
	setDetachFlags(cmd)

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start daemon: %s\n", err)
		os.Exit(1)
	}

	pid := cmd.Process.Pid

	// Release the process so it doesn't become a zombie.
	_ = cmd.Process.Release()

	// Write PID file (for shell script compatibility).
	_ = os.WriteFile(pidFilePath(dataDir), []byte(fmt.Sprintf("%d\n", pid)), 0o644)

	// Write state file.
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
	writeState(dataDir, state)

	// Wait briefly to see if the process exits immediately (e.g. port in use).
	time.Sleep(1 * time.Second)
	if !processAlive(pid) {
		fmt.Fprintf(os.Stderr, "Cliff failed to start. Check %s for details.\n", errorLogFile)
		os.Remove(stateFilePath(dataDir))
		os.Remove(pidFilePath(dataDir))
		os.Exit(1)
	}

	fmt.Printf("Cliff started (PID %d)\n", pid)
	fmt.Printf("  Local:   %s\n", state.LocalURL)
	for _, url := range lanURLs {
		fmt.Printf("  Network: %s\n", url)
	}
	fmt.Printf("  Logs:    %s\n", logFile)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  cliff status   Check status\n")
	fmt.Printf("  cliff logs     View daemon logs\n")
	fmt.Printf("  cliff stop     Stop Cliff\n")
}

// ---- cliff stop ----

func runStop(args []string) {
	fs := flag.NewFlagSet("stop", flag.ExitOnError)
	var dataDir string
	fs.StringVar(&dataDir, "data-dir", "", "panel data directory (default: <install-dir>/data)")
	fs.Parse(args)

	state := readState(dataDir)
	if state == nil {
		// Also try reading the PID file (shell script compat).
		pidStr, err := os.ReadFile(pidFilePath(dataDir))
		if err != nil {
			state = recoverStateFromHealth(getenvInt("CLIFF_PORT", 8080), getenv("CLIFF_HOST", "0.0.0.0"), dataDir, "", "")
			if state == nil {
				fmt.Println("Cliff is not running.")
				return
			}
			writeState(dataDir, *state)
			_ = os.WriteFile(pidFilePath(dataDir), []byte(fmt.Sprintf("%d\n", state.PID)), 0o644)
		} else {
			var pid int
			fmt.Sscanf(string(pidStr), "%d", &pid)
			if pid == 0 || !processAlive(pid) {
				os.Remove(pidFilePath(dataDir))
				state = recoverStateFromHealth(getenvInt("CLIFF_PORT", 8080), getenv("CLIFF_HOST", "0.0.0.0"), dataDir, "", "")
				if state == nil {
					fmt.Println("Cliff is not running.")
					return
				}
				writeState(dataDir, *state)
				_ = os.WriteFile(pidFilePath(dataDir), []byte(fmt.Sprintf("%d\n", state.PID)), 0o644)
			} else {
				state = &cliffState{PID: pid}
			}
		}
	}

	if !processAlive(state.PID) {
		os.Remove(stateFilePath(dataDir))
		os.Remove(pidFilePath(dataDir))
		fmt.Println("Cliff is not running (stale PID file removed).")
		return
	}

	// Send SIGTERM (or equivalent on Windows).
	if err := stopProcess(state.PID); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to stop Cliff (PID %d): %s\n", state.PID, err)
		os.Exit(1)
	}

	// Wait up to 15 seconds for the process to exit.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(state.PID) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	if processAlive(state.PID) {
		// Force kill.
		_ = killProcess(state.PID)
		time.Sleep(500 * time.Millisecond)
	}

	os.Remove(stateFilePath(dataDir))
	os.Remove(pidFilePath(dataDir))

	fmt.Println("Cliff stopped.")
}

// ---- cliff status ----

func runStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	var dataDir string
	fs.StringVar(&dataDir, "data-dir", "", "panel data directory (default: <install-dir>/data)")
	fs.Parse(args)

	state := readState(dataDir)
	if state == nil {
		state = recoverStateFromHealth(getenvInt("CLIFF_PORT", 8080), getenv("CLIFF_HOST", "0.0.0.0"), dataDir, "", "")
		if state == nil {
			fmt.Println("Cliff is not running.")
			fmt.Println("Run 'cliff start' to start the daemon.")
			return
		}
		writeState(dataDir, *state)
		_ = os.WriteFile(pidFilePath(dataDir), []byte(fmt.Sprintf("%d\n", state.PID)), 0o644)
	}

	if !processAlive(state.PID) {
		os.Remove(stateFilePath(dataDir))
		os.Remove(pidFilePath(dataDir))
		state = recoverStateFromHealth(state.Port, state.Host, dataDir, state.ServerRoot, state.WebDir)
		if state == nil {
			fmt.Println("Cliff is not running (stale state file removed).")
			return
		}
		writeState(dataDir, *state)
		_ = os.WriteFile(pidFilePath(dataDir), []byte(fmt.Sprintf("%d\n", state.PID)), 0o644)
	}

	info := buildinfo.Current()
	fmt.Printf("Cliff %s — running\n", info.Version)
	fmt.Printf("  PID:         %d\n", state.PID)
	fmt.Printf("  Port:        %d\n", state.Port)
	fmt.Printf("  Local URL:   %s\n", state.LocalURL)
	for _, url := range state.LANURLs {
		fmt.Printf("  Network URL: %s\n", url)
	}

	// Calculate uptime.
	if state.StartedAt != "" {
		startedAt, err := time.Parse(time.RFC3339, state.StartedAt)
		if err == nil {
			uptime := time.Since(startedAt).Round(time.Second)
			fmt.Printf("  Uptime:      %s\n", formatUptime(uptime))
			fmt.Printf("  Started:     %s\n", startedAt.Format("2006-01-02 15:04:05"))
		}
	}

	fmt.Printf("  Data dir:    %s\n", state.DataDir)
	fmt.Printf("  Server root: %s\n", state.ServerRoot)
	fmt.Printf("  Log file:    %s\n", state.LogFile)
}

// ---- cliff logs ----

func runLogs(args []string) {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	var dataDir string
	var tail int
	fs.StringVar(&dataDir, "data-dir", "", "panel data directory (default: <install-dir>/data)")
	fs.IntVar(&tail, "tail", 80, "number of recent log lines to print")
	fs.IntVar(&tail, "n", 80, "number of recent log lines to print (shorthand)")
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

	data, err := os.ReadFile(logFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "No daemon logs found at %s.\n", logFile)
		fmt.Fprintln(os.Stderr, "Run 'cliff start' to start Cliff, then try 'cliff logs' again.")
		os.Exit(1)
	}

	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if tail > 0 && len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}

	fmt.Printf("Cliff daemon logs: %s\n\n", logFile)
	for _, line := range lines {
		fmt.Println(line)
	}
}

// ---- cliff update ----

func runUpdate(args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	var checkOnly bool
	fs.BoolVar(&checkOnly, "check", false, "only check for updates, don't apply")
	fs.Parse(args)

	root := installRoot()
	dataDir := filepath.Join(root, "data")
	webDir := filepath.Join(root, "web")

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

	// Stop the daemon if it's running.
	if state := readState(dataDir); state != nil && processAlive(state.PID) {
		fmt.Println("Stopping running daemon...")
		_ = stopProcess(state.PID)
		// Wait for it to exit.
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if !processAlive(state.PID) {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		os.Remove(stateFilePath(dataDir))
		os.Remove(pidFilePath(dataDir))
	}

	fmt.Println("Downloading and applying update...")
	applyResult, err := mgr.Apply(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Update failed: %s\n", err)
		os.Exit(1)
	}

	if applyResult.Success {
		fmt.Printf("Update successful: %s\n", applyResult.Message)
		fmt.Println("Restarting...")
		updater.RestartAsync(self, os.Args[1:], 500*time.Millisecond)
		// Wait for restart to happen.
		time.Sleep(2 * time.Second)
	} else {
		fmt.Fprintf(os.Stderr, "Update failed: %s\n", applyResult.Message)
		os.Exit(1)
	}
}

// ---- cliff uninstall ----

func runUninstall(args []string) {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	var yes bool
	fs.BoolVar(&yes, "yes", false, "skip confirmation prompt")
	fs.BoolVar(&yes, "y", false, "skip confirmation prompt (shorthand)")
	fs.Parse(args)

	root := installRoot()
	dataDir := filepath.Join(root, "data")

	// Stop the daemon if running.
	if state := readState(dataDir); state != nil && processAlive(state.PID) {
		fmt.Printf("Stopping daemon (PID %d)...\n", state.PID)
		_ = stopProcess(state.PID)
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if !processAlive(state.PID) {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if processAlive(state.PID) {
			_ = killProcess(state.PID)
		}
		os.Remove(stateFilePath(dataDir))
		os.Remove(pidFilePath(dataDir))
	}

	if !yes {
		fmt.Printf("This will remove Cliff from:\n  %s\n", root)
		fmt.Printf("This includes all server data, worlds, and configuration.\n")
		fmt.Printf("Are you sure? Type 'yes' to confirm: ")
		var response string
		fmt.Scanln(&response)
		if response != "yes" {
			fmt.Println("Uninstall cancelled.")
			return
		}
	}

	// Remove the symlink from PATH.
	removeSymlink()

	// Remove the install directory. On Windows, the running cliff.exe can't
	// delete itself, so we remove everything except the binary and tell the
	// user to delete the remaining folder.
	self, _ := os.Executable()
	removedAll := true
	entries, err := os.ReadDir(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read install directory: %s\n", err)
		os.Exit(1)
	}
	for _, entry := range entries {
		entryPath := filepath.Join(root, entry.Name())
		// Skip the binary itself — it's locked because we're running from it.
		if runtime.GOOS == "windows" && strings.EqualFold(entryPath, self) {
			continue
		}
		if err := os.RemoveAll(entryPath); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to remove %s: %s\n", entryPath, err)
			removedAll = false
		}
	}

	if runtime.GOOS == "windows" && removedAll {
		// Try to remove the now-empty directory (will fail if binary is still there).
		_ = os.Remove(root)
		fmt.Printf("Cliff uninstalled. Delete the remaining folder: %s\n", root)
	} else if removedAll {
		os.Remove(root)
		fmt.Println("Cliff has been uninstalled.")
	} else {
		fmt.Fprintf(os.Stderr, "Some files could not be removed. Delete manually: %s\n", root)
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

func recoverStateFromHealth(port int, host string, dataDir string, serverRoot string, webDir string) *cliffState {
	if port <= 0 {
		port = 8080
	}
	if host == "" {
		host = "0.0.0.0"
	}
	root := installRoot()
	if dataDir == "" {
		dataDir = filepath.Join(root, "data")
	}
	if serverRoot == "" {
		serverRoot = filepath.Join(root, "servers")
	}
	if webDir == "" {
		webDir = filepath.Join(root, "web")
	}

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
	if health.Daemon != "cliff" || health.Self.PID <= 0 || !processAlive(health.Self.PID) {
		return nil
	}
	startedAt := health.StartedAt
	if startedAt == "" && health.UptimeSeconds > 0 {
		startedAt = time.Now().Add(-time.Duration(health.UptimeSeconds) * time.Second).UTC().Format(time.RFC3339)
	}
	localURL := health.LocalURL
	if localURL == "" {
		localURL = fmt.Sprintf("http://localhost:%d", port)
	}
	return &cliffState{
		PID:        health.Self.PID,
		Port:       port,
		Host:       host,
		DataDir:    dataDir,
		ServerRoot: serverRoot,
		WebDir:     webDir,
		LogFile:    filepath.Join(dataDir, "logs", "cliff.log"),
		StartedAt:  startedAt,
		LocalURL:   localURL,
		LANURLs:    health.LANURLs,
	}
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscallKill(pid, 0) == nil
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

// removeSymlink removes the cliff symlink from PATH locations.
func removeSymlink() {
	binaryName := "cliff"
	if runtime.GOOS == "windows" {
		binaryName = "cliff.exe"
	}

	candidates := []string{
		filepath.Join("/usr/local/bin", binaryName),
		filepath.Join(homeDir(), ".local", "bin", binaryName),
	}

	if runtime.GOOS == "windows" {
		// On Windows, the install dir itself is added to PATH, not a symlink.
		// Nothing to remove here — the install dir removal handles it.
		return
	}

	for _, path := range candidates {
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 || (info.Mode().IsRegular() && info.Size() < 1024) {
			// It's a symlink or a very small file (likely a wrapper script).
			if err := os.Remove(path); err == nil {
				fmt.Printf("Removed: %s\n", path)
			}
		}
	}
}

func homeDir() string {
	dir, _ := os.UserHomeDir()
	return dir
}

// Ensure unused imports don't cause errors.
var _ io.Writer = os.Stdout
