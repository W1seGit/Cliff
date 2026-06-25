package process

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

type Lifecycle string

const (
	LifecycleStopped  Lifecycle = "stopped"
	LifecycleStarting Lifecycle = "starting"
	LifecycleRunning  Lifecycle = "running"
	LifecycleStopping Lifecycle = "stopping"
)

const (
	startupWait             = 5 * time.Second
	maxRetainedLogLines     = 1000
	maxRetainedLogLineBytes = 16 * 1024
	maxUsageSamples         = 36
	maxPlayerSamples        = 36
	maxHistorySamples       = 17280 // 24h at 5s intervals
	maxHistoryPlayerSamples = 17280
	sampleInterval          = 5 * time.Second
	subscriberQueueSize     = 64
)

const truncatedLogSuffix = " ... [truncated]"

type Status struct {
	RunningServerID string            `json:"runningServerId"`
	Lifecycle       Lifecycle         `json:"lifecycle"`
	PID             int               `json:"pid"`
	StartedAt       string            `json:"startedAt"`
	UptimeSeconds   int64             `json:"uptimeSeconds"`
	Command         string            `json:"command"`
	LaunchTarget    string            `json:"launchTarget"`
	Usage           *Usage            `json:"usage,omitempty"`
	Servers         map[string]Status `json:"servers,omitempty"`
}

type UsageSample struct {
	At          string   `json:"at"`
	CPUPercent  *float64 `json:"cpuPercent"`
	MemoryBytes *int64   `json:"memoryBytes"`
}

type PlayerSample struct {
	At    string `json:"at"`
	Count int    `json:"count"`
}

type Usage struct {
	CPUPercent       *float64       `json:"cpuPercent"`
	MemoryBytes      *int64         `json:"memoryBytes"`
	MemoryLimitBytes *int64         `json:"memoryLimitBytes"`
	Samples          []UsageSample  `json:"samples"`
	PlayerSamples    []PlayerSample `json:"playerSamples,omitempty"`
	LastSampleAt     string         `json:"lastSampleAt,omitempty"`
}

type Event struct {
	Type     string `json:"type"`
	ServerID string `json:"serverId"`
	Line     string `json:"line,omitempty"`
	Status   Status `json:"status,omitempty"`
}

type Manager struct {
	mu           sync.Mutex
	running      map[string]*managedProcess
	history      map[string][]string
	usageHistory map[string]*serverUsageHistory
	subscribers  map[chan Event]subscriber
	dataDir      string
}

type serverUsageHistory struct {
	usage        []UsageSample
	players      []PlayerSample
	lastSampleAt time.Time
	memoryLimit  int64
}

type subscriber struct {
	serverID    string
	includeLogs bool
}

type managedProcess struct {
	serverID         string
	cmd              *exec.Cmd
	stdin            io.WriteCloser
	lifecycle        Lifecycle
	startedAt        time.Time
	command          string
	launchTarget     string
	logs             []string
	memoryLimitBytes int64
	usageSamples     []UsageSample
	lastCPUSeconds   *float64
	lastSampleAt     time.Time
	lastUsageReadAt  time.Time
	lastUsage        *Usage
	playerCount      int
	playerSamples    []PlayerSample
	ready            chan struct{}
	exited           chan struct{}
	readyOnce        sync.Once
	exitOnce         sync.Once
	outputDone       sync.WaitGroup
}

func NewManager(dataDir string) *Manager {
	m := &Manager{
		running:      map[string]*managedProcess{},
		history:      map[string][]string{},
		usageHistory: map[string]*serverUsageHistory{},
		subscribers:  map[chan Event]subscriber{},
		dataDir:      dataDir,
	}
	m.loadAllUsageHistory()
	return m
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	if len(m.running) == 0 {
		m.mu.Unlock()
		return Status{Lifecycle: LifecycleStopped}
	}
	var latest *managedProcess
	procs := make(map[string]*managedProcess, len(m.running))
	for serverID, candidate := range m.running {
		procs[serverID] = candidate
		if latest == nil || candidate.startedAt.After(latest.startedAt) {
			latest = candidate
		}
	}
	status := statusForProcess(latest, false)
	status.Servers = m.statusesLocked(false)
	m.mu.Unlock()

	if latest != nil {
		status.Usage = m.collectUsage(latest, status.PID)
	}
	for serverID, serverStatus := range status.Servers {
		if proc := procs[serverID]; proc != nil {
			serverStatus.Usage = m.collectUsage(proc, serverStatus.PID)
			status.Servers[serverID] = serverStatus
		}
	}
	return status
}

func (m *Manager) StatusLight() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked(false)
}

func (m *Manager) StatusFor(serverID string) Status {
	m.mu.Lock()
	proc := m.running[serverID]
	status := statusForProcess(proc, false)
	m.mu.Unlock()
	if proc != nil {
		status.Usage = m.collectUsage(proc, status.PID)
	}
	return status
}

func (m *Manager) StatusForLight(serverID string) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusForLocked(serverID, false)
}

func (m *Manager) IsRunning(serverID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	proc := m.running[serverID]
	return proc != nil && proc.lifecycle != LifecycleStopped
}

func (m *Manager) Logs(serverID string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if proc := m.running[serverID]; proc != nil {
		return append([]string(nil), proc.logs...)
	}
	return append([]string(nil), m.history[serverID]...)
}

func (m *Manager) Forget(serverID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.history, serverID)
}

func (m *Manager) Start(server store.Server) (Status, error) {
	m.mu.Lock()
	if m.running[server.ID] != nil {
		m.mu.Unlock()
		return Status{}, errors.New("server is already running")
	}
	cmd, args, commandText, err := launchCommand(server)
	if err != nil {
		m.mu.Unlock()
		return Status{}, err
	}
	cmd.Dir = server.Path

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		m.mu.Unlock()
		return Status{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		m.mu.Unlock()
		return Status{}, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		m.mu.Unlock()
		return Status{}, err
	}

	proc := &managedProcess{
		serverID:         server.ID,
		cmd:              cmd,
		stdin:            stdin,
		lifecycle:        LifecycleStarting,
		startedAt:        time.Now().UTC(),
		command:          commandText,
		launchTarget:     server.LaunchJar,
		memoryLimitBytes: int64(server.MaxMemoryMB) * 1024 * 1024,
		logs: []string{
			fmt.Sprintf("Starting %s from %s", server.Name, filepath.Join(server.Path, server.LaunchJar)),
			fmt.Sprintf("Using command: %s", commandText),
		},
		ready:  make(chan struct{}),
		exited: make(chan struct{}),
	}
	m.running[server.ID] = proc
	m.rememberLocked(proc.serverID, proc.logs)
	m.mu.Unlock()

	if err := cmd.Start(); err != nil {
		m.mu.Lock()
		delete(m.running, server.ID)
		m.mu.Unlock()
		return Status{}, err
	}

	proc.outputDone.Add(2)
	go m.scanOutput(proc, stdout)
	go m.scanOutput(proc, stderr)
	go m.wait(proc)
	go m.sampleLoop(proc)

	_ = args
	status, err := m.waitForStartup(proc, startupWait)
	if err != nil {
		return status, err
	}
	m.publish(Event{Type: "status", ServerID: server.ID, Status: m.StatusLight()})
	return status, nil
}

func (m *Manager) Stop(serverID string, force bool) (Status, error) {
	m.mu.Lock()
	proc := m.running[serverID]
	if proc == nil {
		m.mu.Unlock()
		return Status{Lifecycle: LifecycleStopped}, nil
	}
	proc.lifecycle = LifecycleStopping
	m.mu.Unlock()

	if force {
		m.pushLog(proc, "Force stop requested")
		_ = killProcessTree(proc)
		status := m.StatusLight()
		m.publish(Event{Type: "status", ServerID: serverID, Status: m.StatusLight()})
		return statusForServer(status, serverID), nil
	}

	m.pushLog(proc, "Stop requested")
	if proc.stdin != nil {
		_, _ = io.WriteString(proc.stdin, "stop\n")
	}
	status := m.StatusLight()
	m.publish(Event{Type: "status", ServerID: serverID, Status: m.StatusLight()})
	return statusForServer(status, serverID), nil
}

func (m *Manager) StopAndWait(serverID string, force bool, timeout time.Duration) (Status, error) {
	status, err := m.Stop(serverID, force)
	if err != nil {
		return status, err
	}
	if status.Lifecycle == LifecycleStopped {
		return status, nil
	}
	if m.WaitStopped(serverID, timeout) {
		return m.StatusForLight(serverID), nil
	}
	return m.StatusForLight(serverID), nil
}

func (m *Manager) Restart(server store.Server, force bool, timeout time.Duration) (Status, error) {
	if m.IsRunning(server.ID) {
		if _, err := m.Stop(server.ID, force); err != nil {
			return Status{}, err
		}
		if !m.WaitStopped(server.ID, timeout) {
			if !force {
				return m.StatusForLight(server.ID), errors.New("server did not stop before restart timeout")
			}
			return m.StatusForLight(server.ID), errors.New("server could not be force-stopped before restart")
		}
	}
	return m.Start(server)
}

func (m *Manager) Shutdown(timeout time.Duration) {
	m.mu.Lock()
	procs := make([]*managedProcess, 0, len(m.running))
	serverIDs := make([]string, 0, len(m.running))
	for serverID, proc := range m.running {
		serverIDs = append(serverIDs, serverID)
		procs = append(procs, proc)
	}
	m.mu.Unlock()

	for _, serverID := range serverIDs {
		_, _ = m.Stop(serverID, false)
	}

	deadline := time.Now().Add(timeout)
	for _, proc := range procs {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		if !waitForProcessExit(proc, remaining) {
			break
		}
	}
	if m.runningCount() == 0 {
		return
	}

	m.mu.Lock()
	remaining := make([]*managedProcess, 0, len(m.running))
	for _, proc := range m.running {
		remaining = append(remaining, proc)
	}
	m.mu.Unlock()

	for _, proc := range remaining {
		m.pushLog(proc, "Force stop requested after daemon shutdown timeout")
		_ = killProcessTree(proc)
	}

	deadline = time.Now().Add(5 * time.Second)
	for _, proc := range remaining {
		remainingTimeout := time.Until(deadline)
		if remainingTimeout <= 0 {
			break
		}
		_ = waitForProcessExit(proc, remainingTimeout)
	}
}

func (m *Manager) WaitStopped(serverID string, timeout time.Duration) bool {
	m.mu.Lock()
	proc := m.running[serverID]
	m.mu.Unlock()
	if proc == nil {
		return true
	}
	if !waitForProcessExit(proc, timeout) {
		return false
	}
	return !m.IsRunning(serverID)
}

func waitForProcessExit(proc *managedProcess, timeout time.Duration) bool {
	if timeout <= 0 {
		select {
		case <-proc.exited:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-proc.exited:
		return true
	case <-timer.C:
		return false
	}
}

func killProcessTree(proc *managedProcess) error {
	if proc == nil || proc.cmd == nil || proc.cmd.Process == nil {
		return nil
	}
	if runtime.GOOS == "windows" {
		err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(proc.cmd.Process.Pid)).Run()
		if err == nil {
			return nil
		}
		return proc.cmd.Process.Kill()
	}
	return proc.cmd.Process.Kill()
}

func (m *Manager) runningCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.running)
}

func (m *Manager) Command(serverID string, command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return errors.New("command is required")
	}

	m.mu.Lock()
	proc := m.running[serverID]
	if proc == nil || proc.stdin == nil {
		m.mu.Unlock()
		return errors.New("server is not running")
	}
	m.mu.Unlock()

	m.pushLog(proc, "> "+command)
	_, err := io.WriteString(proc.stdin, command+"\n")
	return err
}

func (m *Manager) Subscribe() (<-chan Event, func()) {
	return m.SubscribeFor("", true)
}

func (m *Manager) SubscribeFor(serverID string, includeLogs bool) (<-chan Event, func()) {
	ch := make(chan Event, subscriberQueueSize)
	m.mu.Lock()
	m.subscribers[ch] = subscriber{serverID: serverID, includeLogs: includeLogs}
	m.mu.Unlock()
	return ch, func() {
		m.mu.Lock()
		delete(m.subscribers, ch)
		close(ch)
		m.mu.Unlock()
	}
}

func (m *Manager) scanOutput(proc *managedProcess, reader io.Reader) {
	defer proc.outputDone.Done()
	scanner := bufio.NewScanner(reader)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		m.pushLog(proc, line)
		if strings.Contains(line, "Done (") {
			m.markRunning(proc)
		}
		m.parseLogLine(proc, line)
	}
}

func (m *Manager) sampleLoop(proc *managedProcess) {
	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()
	saveTicker := time.NewTicker(30 * time.Second)
	defer saveTicker.Stop()
	for {
		select {
		case <-proc.exited:
			m.saveUsageHistory(proc.serverID)
			return
		case <-ticker.C:
			pid := 0
			if proc.cmd != nil && proc.cmd.Process != nil {
				pid = proc.cmd.Process.Pid
			}
			m.collectUsage(proc, pid)
		case <-saveTicker.C:
			m.saveUsageHistory(proc.serverID)
		}
	}
}

// Patterns for parsing Minecraft server log lines.
// Player join:  "[HH:MM:SS] [Server thread/INFO]: PlayerName joined the game"
// Player leave: "[HH:MM:SS] [Server thread/INFO]: PlayerName left the game"
// Note: "logged in with" and "lost connection" are NOT used because they
// fire alongside "joined"/"left" for the same event, causing double counting.
var (
	playerJoinRe  = regexp.MustCompile(`([A-Za-z0-9_]{3,16}) joined the game`)
	playerLeaveRe = regexp.MustCompile(`([A-Za-z0-9_]{3,16}) left the game`)
)

func (m *Manager) parseLogLine(proc *managedProcess, line string) {
	// Player join detection
	if playerJoinRe.MatchString(line) {
		m.mu.Lock()
		if m.running[proc.serverID] == proc {
			proc.playerCount++
		}
		m.mu.Unlock()
		return
	}
	// Player leave detection
	if playerLeaveRe.MatchString(line) {
		m.mu.Lock()
		if m.running[proc.serverID] == proc && proc.playerCount > 0 {
			proc.playerCount--
		}
		m.mu.Unlock()
		return
	}
}

func (m *Manager) wait(proc *managedProcess) {
	err := proc.cmd.Wait()
	waitForOutputScanners(proc, 2*time.Second)
	message := "Server exited"
	if err != nil {
		message = "Server exited: " + err.Error()
	}
	m.pushLog(proc, message)

	m.mu.Lock()
	if m.running[proc.serverID] == proc {
		m.rememberLocked(proc.serverID, proc.logs)
		delete(m.running, proc.serverID)
	}
	m.mu.Unlock()
	proc.exitOnce.Do(func() { close(proc.exited) })
	m.publish(Event{Type: "status", ServerID: proc.serverID, Status: m.StatusLight()})
}

func waitForOutputScanners(proc *managedProcess, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		proc.outputDone.Wait()
		close(done)
	}()
	if timeout <= 0 {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func (m *Manager) markRunning(proc *managedProcess) {
	changed := false
	m.mu.Lock()
	if m.running[proc.serverID] == proc && proc.lifecycle == LifecycleStarting {
		proc.lifecycle = LifecycleRunning
		changed = true
	}
	m.mu.Unlock()
	if changed {
		proc.readyOnce.Do(func() { close(proc.ready) })
	}
	m.publish(Event{Type: "status", ServerID: proc.serverID, Status: m.StatusLight()})
}

func (m *Manager) waitForStartup(proc *managedProcess, timeout time.Duration) (Status, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-proc.ready:
		return m.StatusLight(), nil
	case <-proc.exited:
		return m.StatusForLight(proc.serverID), errors.New("server exited during startup. Check the console for details.")
	case <-timer.C:
		m.markRunning(proc)
		return m.StatusLight(), nil
	}
}

func (m *Manager) pushLog(proc *managedProcess, line string) {
	line = normalizeLogLine(line)
	if line == "" {
		return
	}

	m.mu.Lock()
	if m.running[proc.serverID] != proc {
		m.mu.Unlock()
		return
	}
	proc.logs = append(proc.logs, line)
	if len(proc.logs) > maxRetainedLogLines {
		proc.logs = proc.logs[len(proc.logs)-maxRetainedLogLines:]
	}
	m.mu.Unlock()
	m.publish(Event{Type: "log", ServerID: proc.serverID, Line: line})
}

func normalizeLogLine(line string) string {
	line = strings.TrimRight(line, "\r\n")
	if len(line) <= maxRetainedLogLineBytes {
		return line
	}
	limit := maxRetainedLogLineBytes - len(truncatedLogSuffix)
	if limit < 0 {
		limit = 0
	}
	return line[:limit] + truncatedLogSuffix
}

func (m *Manager) rememberLocked(serverID string, logs []string) {
	copyLogs := append([]string(nil), logs...)
	if len(copyLogs) > maxRetainedLogLines {
		copyLogs = copyLogs[len(copyLogs)-maxRetainedLogLines:]
	}
	m.history[serverID] = copyLogs
}

func (m *Manager) statusLocked(includeUsage bool) Status {
	if len(m.running) == 0 {
		return Status{Lifecycle: LifecycleStopped}
	}
	var proc *managedProcess
	for _, candidate := range m.running {
		if proc == nil || candidate.startedAt.After(proc.startedAt) {
			proc = candidate
		}
	}
	status := statusForProcess(proc, includeUsage)
	status.Servers = m.statusesLocked(includeUsage)
	return status
}

func (m *Manager) statusesLocked(includeUsage bool) map[string]Status {
	statuses := make(map[string]Status, len(m.running))
	for serverID, proc := range m.running {
		statuses[serverID] = statusForProcess(proc, includeUsage)
	}
	return statuses
}

func (m *Manager) statusForLocked(serverID string, includeUsage bool) Status {
	return statusForProcess(m.running[serverID], includeUsage)
}

func statusForProcess(proc *managedProcess, includeUsage bool) Status {
	if proc == nil {
		return Status{Lifecycle: LifecycleStopped}
	}
	pid := 0
	if proc.cmd.Process != nil {
		pid = proc.cmd.Process.Pid
	}
	status := Status{
		RunningServerID: proc.serverID,
		Lifecycle:       proc.lifecycle,
		PID:             pid,
		StartedAt:       proc.startedAt.Format(time.RFC3339),
		UptimeSeconds:   int64(time.Since(proc.startedAt).Seconds()),
		Command:         proc.command,
		LaunchTarget:    proc.launchTarget,
	}
	if includeUsage {
		status.Usage = usageFromLast(proc)
	}
	return status
}

func statusForServer(status Status, serverID string) Status {
	if status.Servers != nil {
		if serverStatus, ok := status.Servers[serverID]; ok {
			return serverStatus
		}
	}
	if status.RunningServerID == serverID {
		return status
	}
	return Status{Lifecycle: LifecycleStopped}
}

func (m *Manager) collectUsage(proc *managedProcess, pid int) *Usage {
	if pid == 0 {
		m.mu.Lock()
		defer m.mu.Unlock()
		return usageFromLast(proc)
	}
	now := time.Now().UTC()
	m.mu.Lock()
	if proc.lastUsage != nil && now.Sub(proc.lastUsageReadAt) < 5*time.Second {
		defer m.mu.Unlock()
		return usageFromLast(proc)
	}
	m.mu.Unlock()

	raw := readProcessUsage(pid)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running[proc.serverID] != proc {
		return usageFromLast(proc)
	}
	if proc.lastUsage != nil && now.Sub(proc.lastUsageReadAt) < 5*time.Second {
		return usageFromLast(proc)
	}
	var cpuPercent *float64
	if raw.cpuPercent != nil {
		value := clampPercent(*raw.cpuPercent)
		cpuPercent = &value
	} else if raw.cpuSeconds != nil {
		if proc.lastCPUSeconds != nil && !proc.lastSampleAt.IsZero() && now.After(proc.lastSampleAt) {
			elapsed := now.Sub(proc.lastSampleAt).Seconds()
			delta := *raw.cpuSeconds - *proc.lastCPUSeconds
			if elapsed > 0 && delta >= 0 {
				value := clampPercent((delta / elapsed / float64(runtime.NumCPU())) * 100)
				cpuPercent = &value
			}
		}
		value := *raw.cpuSeconds
		proc.lastCPUSeconds = &value
		proc.lastSampleAt = now
	}
	sample := UsageSample{
		At:          now.Format(time.RFC3339),
		CPUPercent:  cpuPercent,
		MemoryBytes: raw.memoryBytes,
	}
	rememberUsageSample(proc, sample)
	playerSample := PlayerSample{At: now.Format(time.RFC3339), Count: proc.playerCount}
	rememberPlayerSample(proc, playerSample)
	// Also store in Manager-level history (persists after server stops)
	m.rememberHistorySample(proc.serverID, sample, playerSample, now, proc.memoryLimitBytes)
	proc.lastUsageReadAt = now
	proc.lastUsage = &Usage{
		CPUPercent:       cpuPercent,
		MemoryBytes:      raw.memoryBytes,
		MemoryLimitBytes: &proc.memoryLimitBytes,
		Samples:          append([]UsageSample(nil), proc.usageSamples...),
		PlayerSamples:    append([]PlayerSample(nil), proc.playerSamples...),
		LastSampleAt:     now.Format(time.RFC3339),
	}
	return usageFromLast(proc)
}

func rememberUsageSample(proc *managedProcess, sample UsageSample) {
	proc.usageSamples = append(proc.usageSamples, sample)
	if len(proc.usageSamples) > maxUsageSamples {
		proc.usageSamples = proc.usageSamples[len(proc.usageSamples)-maxUsageSamples:]
	}
}

func rememberPlayerSample(proc *managedProcess, sample PlayerSample) {
	proc.playerSamples = append(proc.playerSamples, sample)
	if len(proc.playerSamples) > maxPlayerSamples {
		proc.playerSamples = proc.playerSamples[len(proc.playerSamples)-maxPlayerSamples:]
	}
}

func (m *Manager) rememberHistorySample(serverID string, usage UsageSample, player PlayerSample, now time.Time, memLimit int64) {
	h, ok := m.usageHistory[serverID]
	if !ok {
		h = &serverUsageHistory{}
		m.usageHistory[serverID] = h
	}
	h.usage = append(h.usage, usage)
	if len(h.usage) > maxHistorySamples {
		h.usage = h.usage[len(h.usage)-maxHistorySamples:]
	}
	h.players = append(h.players, player)
	if len(h.players) > maxHistoryPlayerSamples {
		h.players = h.players[len(h.players)-maxHistoryPlayerSamples:]
	}
	h.lastSampleAt = now
	h.memoryLimit = memLimit
}

// usageHistoryPath returns the file path for a server's persisted usage history.
func (m *Manager) usageHistoryPath(serverID string) string {
	dir := filepath.Join(m.dataDir, "usage-history")
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, serverID+".json")
}

type persistedUsageHistory struct {
	Usage        []UsageSample  `json:"usage"`
	Players      []PlayerSample `json:"players"`
	LastSampleAt string         `json:"lastSampleAt"`
	MemoryLimit  int64          `json:"memoryLimit"`
}

// saveUsageHistory writes a server's usage history to disk.
func (m *Manager) saveUsageHistory(serverID string) {
	h, ok := m.usageHistory[serverID]
	if !ok || (len(h.usage) == 0 && len(h.players) == 0) {
		return
	}
	path := m.usageHistoryPath(serverID)
	data := persistedUsageHistory{
		Usage:        h.usage,
		Players:      h.players,
		LastSampleAt: h.lastSampleAt.Format(time.RFC3339),
		MemoryLimit:  h.memoryLimit,
	}
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, b, 0o644)
}

// loadUsageHistory loads a server's usage history from disk.
func (m *Manager) loadUsageHistory(serverID string) {
	path := m.usageHistoryPath(serverID)
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var data persistedUsageHistory
	if err := json.Unmarshal(b, &data); err != nil {
		return
	}
	lastSampleAt, _ := time.Parse(time.RFC3339, data.LastSampleAt)
	m.usageHistory[serverID] = &serverUsageHistory{
		usage:        data.Usage,
		players:      data.Players,
		lastSampleAt: lastSampleAt,
		memoryLimit:  data.MemoryLimit,
	}
}

// loadAllUsageHistory loads all persisted usage history files from disk.
func (m *Manager) loadAllUsageHistory() {
	dir := filepath.Join(m.dataDir, "usage-history")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		serverID := strings.TrimSuffix(entry.Name(), ".json")
		m.loadUsageHistory(serverID)
	}
}

// UsageForWindow returns usage and player samples within the given time window,
// downsampled if necessary to keep payload size reasonable.
// Works even when the server is stopped, using persisted history.
func (m *Manager) UsageForWindow(serverID string, window time.Duration) *Usage {
	m.mu.Lock()
	h, ok := m.usageHistory[serverID]
	proc := m.running[serverID]
	var memLimit int64
	if proc != nil {
		memLimit = proc.memoryLimitBytes
	} else if ok && h.memoryLimit > 0 {
		memLimit = h.memoryLimit
	}
	if !ok && proc == nil {
		m.mu.Unlock()
		return &Usage{
			Samples:       []UsageSample{},
			PlayerSamples: []PlayerSample{},
		}
	}
	if !ok {
		m.mu.Unlock()
		return &Usage{
			MemoryLimitBytes: &memLimit,
			Samples:          []UsageSample{},
			PlayerSamples:    []PlayerSample{},
		}
	}
	cutoff := time.Now().UTC().Add(-window - 30*time.Second)
	// Filter usage history
	var usageFiltered []UsageSample
	for _, s := range h.usage {
		if t, err := time.Parse(time.RFC3339, s.At); err == nil && t.After(cutoff) {
			usageFiltered = append(usageFiltered, s)
		}
	}
	// Filter player history
	var playerFiltered []PlayerSample
	for _, s := range h.players {
		if t, err := time.Parse(time.RFC3339, s.At); err == nil && t.After(cutoff) {
			playerFiltered = append(playerFiltered, s)
		}
	}
	lastSampleAt := h.lastSampleAt
	m.mu.Unlock()

	// Downsample if too many samples
	maxSamples := 300
	if len(usageFiltered) > maxSamples {
		usageFiltered = downsampleUsage(usageFiltered, maxSamples)
	}
	if len(playerFiltered) > maxSamples {
		playerFiltered = downsamplePlayers(playerFiltered, maxSamples)
	}

	return &Usage{
		MemoryLimitBytes: &memLimit,
		Samples:          usageFiltered,
		PlayerSamples:    playerFiltered,
		LastSampleAt:     lastSampleAt.Format(time.RFC3339),
	}
}

func downsampleUsage(samples []UsageSample, target int) []UsageSample {
	if len(samples) <= target || target <= 0 {
		return samples
	}
	step := len(samples) / target
	result := make([]UsageSample, 0, target)
	for i := 0; i < len(samples); i += step {
		// Average over the bucket
		var cpuSum, memSum float64
		var cpuCount, memCount int
		end := i + step
		if end > len(samples) {
			end = len(samples)
		}
		for j := i; j < end; j++ {
			if samples[j].CPUPercent != nil {
				cpuSum += *samples[j].CPUPercent
				cpuCount++
			}
			if samples[j].MemoryBytes != nil {
				memSum += float64(*samples[j].MemoryBytes)
				memCount++
			}
		}
		s := UsageSample{At: samples[i].At}
		if cpuCount > 0 {
			v := cpuSum / float64(cpuCount)
			s.CPUPercent = &v
		}
		if memCount > 0 {
			v := int64(memSum / float64(memCount))
			s.MemoryBytes = &v
		}
		result = append(result, s)
	}
	return result
}

func downsamplePlayers(samples []PlayerSample, target int) []PlayerSample {
	if len(samples) <= target || target <= 0 {
		return samples
	}
	step := len(samples) / target
	result := make([]PlayerSample, 0, target)
	for i := 0; i < len(samples); i += step {
		// Take max player count in bucket
		maxCount := 0
		end := i + step
		if end > len(samples) {
			end = len(samples)
		}
		for j := i; j < end; j++ {
			if samples[j].Count > maxCount {
				maxCount = samples[j].Count
			}
		}
		result = append(result, PlayerSample{At: samples[i].At, Count: maxCount})
	}
	return result
}

func usageFromLast(proc *managedProcess) *Usage {
	if proc.lastUsage != nil {
		copyUsage := *proc.lastUsage
		copyUsage.Samples = append([]UsageSample(nil), proc.lastUsage.Samples...)
		copyUsage.PlayerSamples = append([]PlayerSample(nil), proc.lastUsage.PlayerSamples...)
		return &copyUsage
	}
	return &Usage{
		MemoryLimitBytes: &proc.memoryLimitBytes,
		Samples:          append([]UsageSample(nil), proc.usageSamples...),
		PlayerSamples:    append([]PlayerSample(nil), proc.playerSamples...),
	}
}

type rawUsage struct {
	cpuSeconds  *float64
	cpuPercent  *float64
	memoryBytes *int64
}

type processUsageRow struct {
	pid        int
	parentPID  int
	rssKB      int64
	cpuPercent float64
}

func readProcessUsage(pid int) rawUsage {
	if runtime.GOOS == "windows" {
		return readWindowsProcessUsage(pid)
	}
	return readUnixProcessUsage(pid)
}

func readWindowsProcessUsage(pid int) rawUsage {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	script := fmt.Sprintf(`
$root = %d
$children = @{}
Get-CimInstance Win32_Process | ForEach-Object {
  if (-not $children.ContainsKey([int]$_.ParentProcessId)) { $children[[int]$_.ParentProcessId] = New-Object System.Collections.Generic.List[int] }
  $children[[int]$_.ParentProcessId].Add([int]$_.ProcessId)
}
$ids = New-Object System.Collections.Generic.HashSet[int]
$queue = New-Object System.Collections.Generic.Queue[int]
[void]$ids.Add($root)
$queue.Enqueue($root)
while ($queue.Count -gt 0) {
  $parent = $queue.Dequeue()
  if ($children.ContainsKey($parent)) {
    foreach ($child in $children[$parent]) {
      if ($ids.Add($child)) { $queue.Enqueue($child) }
    }
  }
}
$cpu = 0.0
$mem = 0
foreach ($id in $ids) {
  try {
    $p = Get-Process -Id $id -ErrorAction Stop
    if ($null -ne $p.CPU) { $cpu += [double]$p.CPU }
    if ($null -ne $p.WorkingSet64) { $mem += [int64]$p.WorkingSet64 }
  } catch {}
}
[Console]::WriteLine((@{ CPU = $cpu; WorkingSet64 = $mem; ProcessCount = $ids.Count } | ConvertTo-Json -Compress))
`, pid)
	output, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-Command", script).Output()
	if err != nil {
		return rawUsage{}
	}
	return parseWindowsUsageJSON(output)
}

func readUnixProcessUsage(pid int) rawUsage {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ps", "-o", "pid=,ppid=,rss=,%cpu=", "-ax").Output()
	if err != nil {
		return rawUsage{}
	}
	return collectUsageFromRows(pid, parseUnixProcessTable(output))
}

func parseWindowsUsageJSON(output []byte) rawUsage {
	var parsed struct {
		CPU          *float64 `json:"CPU"`
		WorkingSet64 *int64   `json:"WorkingSet64"`
		ProcessCount int      `json:"ProcessCount"`
	}
	if err := json.Unmarshal(output, &parsed); err != nil {
		return rawUsage{}
	}
	return rawUsage{cpuSeconds: parsed.CPU, memoryBytes: parsed.WorkingSet64}
}

func parseUnixProcessTable(output []byte) []processUsageRow {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	rows := make([]processUsageRow, 0, len(lines))
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) < 4 {
			continue
		}
		pid, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		parentPID, _ := strconv.Atoi(parts[1])
		rssKB, _ := strconv.ParseInt(parts[2], 10, 64)
		cpuPercent, _ := strconv.ParseFloat(parts[3], 64)
		rows = append(rows, processUsageRow{pid: pid, parentPID: parentPID, rssKB: rssKB, cpuPercent: cpuPercent})
	}
	return rows
}

func collectUsageFromRows(rootPID int, rows []processUsageRow) rawUsage {
	ids := map[int]struct{}{rootPID: {}}
	changed := true
	for changed {
		changed = false
		for _, row := range rows {
			if _, parentKnown := ids[row.parentPID]; parentKnown {
				if _, known := ids[row.pid]; !known {
					ids[row.pid] = struct{}{}
					changed = true
				}
			}
		}
	}

	var cpu float64
	var memory int64
	for _, row := range rows {
		if _, ok := ids[row.pid]; !ok {
			continue
		}
		cpu += row.cpuPercent
		memory += row.rssKB * 1024
	}
	return rawUsage{cpuPercent: &cpu, memoryBytes: &memory}
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func (m *Manager) publish(event Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for ch, subscription := range m.subscribers {
		if subscription.serverID != "" && event.ServerID != "" && event.ServerID != subscription.serverID {
			continue
		}
		if event.Type == "log" && !subscription.includeLogs {
			continue
		}
		select {
		case ch <- event:
		default:
		}
	}
}

func launchCommand(server store.Server) (*exec.Cmd, []string, string, error) {
	if server.LaunchJar == "" {
		return nil, nil, "", errors.New("no launch target configured")
	}
	launchPath := filepath.Join(server.Path, server.LaunchJar)
	if _, err := os.Stat(launchPath); err != nil {
		return nil, nil, "", fmt.Errorf("launch target not found: %w", err)
	}

	lower := strings.ToLower(server.LaunchJar)
	if isInstallerLaunchJar(lower) {
		if replacement := detectBetterLaunchTarget(server.Path); replacement != "" {
			return nil, nil, "", fmt.Errorf("launch target %s is an installer jar, not a server launcher. Set the launch target to %s instead", server.LaunchJar, replacement)
		}
		return nil, nil, "", fmt.Errorf("launch target %s is an installer jar, not a server launcher. Run the installer first or choose the generated server launch target", server.LaunchJar)
	}
	var command string
	var args []string
	switch {
	case runtime.GOOS == "windows" && strings.HasSuffix(lower, ".bat"):
		command = "cmd.exe"
		args = []string{"/c", server.LaunchJar}
	case strings.HasSuffix(lower, ".sh"):
		command = "sh"
		args = []string{server.LaunchJar}
	default:
		command = strings.TrimSpace(server.JavaPath)
		if command == "" || command == "auto" || strings.HasPrefix(command, "managed:") {
			command = "java"
		}
		args = []string{
			fmt.Sprintf("-Xms%dM", server.MinMemoryMB),
			fmt.Sprintf("-Xmx%dM", server.MaxMemoryMB),
			"-jar",
			server.LaunchJar,
		}
		args = append(args, splitArgs(server.ExtraArgs)...)
		args = append(args, "nogui")
	}

	cmd := exec.Command(command, args...)
	return cmd, args, strings.Join(append([]string{command}, args...), " "), nil
}

// isInstallerLaunchJar reports whether the launch jar is a mod-loader
// installer rather than a Minecraft server jar. Installer jars don't
// accept the nogui flag.
func isInstallerLaunchJar(lower string) bool {
	return strings.Contains(lower, "installer")
}

func detectBetterLaunchTarget(serverPath string) string {
	scripts := []string{"run.sh", "start.sh", "start.command", "server.sh", "run.bat", "start.bat", "server.bat"}
	for _, script := range scripts {
		if fileExists(filepath.Join(serverPath, script)) {
			return script
		}
	}
	entries, err := os.ReadDir(serverPath)
	if err != nil {
		return ""
	}
	jars := []string{}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".jar") {
			jars = append(jars, entry.Name())
		}
	}
	for _, jar := range jars {
		if strings.EqualFold(jar, "fabric-server-launch.jar") {
			return jar
		}
	}
	for _, jar := range jars {
		lower := strings.ToLower(jar)
		if !isInstallerLaunchJar(lower) && strings.Contains(lower, "server") {
			return jar
		}
	}
	for _, jar := range jars {
		if !isInstallerLaunchJar(strings.ToLower(jar)) {
			return jar
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func splitArgs(input string) []string {
	args := []string{}
	current := strings.Builder{}
	quote := rune(0)
	escaping := false

	for _, char := range input {
		if escaping {
			current.WriteRune(char)
			escaping = false
			continue
		}
		if char == '\\' {
			escaping = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			} else {
				current.WriteRune(char)
			}
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			continue
		}
		if char == ' ' || char == '\t' || char == '\n' || char == '\r' {
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(char)
	}

	if escaping {
		current.WriteRune('\\')
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}
