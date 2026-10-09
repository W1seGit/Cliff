package process

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
	"github.com/W1seGit/Cliff/daemon/internal/winproc"
)

// readyTimeout is the fallback for servers that never print a "Done (" line:
// after this long we stop reporting "starting" and treat the server as running.
var readyTimeout = 10 * time.Minute

type Lifecycle string

const (
	LifecycleStopped  Lifecycle = "stopped"
	LifecycleStarting Lifecycle = "starting"
	LifecycleRunning  Lifecycle = "running"
	LifecycleStopping Lifecycle = "stopping"
)

const (
	// startupGrace is how long Start waits to catch a server that dies right
	// away (bad Java, missing files). It does not mean the server is ready.
	startupGrace            = 2 * time.Second
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
	TickSamples      []TickSample   `json:"tickSamples,omitempty"`
	Tick             *TickSample    `json:"tick,omitempty"`
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

	lifecycleHandler func(LifecycleEvent)
}

type serverUsageHistory struct {
	usage        []UsageSample
	players      []PlayerSample
	ticks        []TickSample
	lastSampleAt time.Time
	memoryLimit  int64
}

type subscriber struct {
	serverID    string
	includeLogs bool
}

type managedProcess struct {
	serverID         string
	server           store.Server
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
	lastTick         *TickSample
	outputReaders    []*os.File
	probeUntil       time.Time
	stopRequested    bool
	readyAt          time.Time
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

// RunningServerIDs lists the servers that are starting, running or stopping.
func (m *Manager) RunningServerIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.running))
	for id, proc := range m.running {
		if proc != nil && proc.lifecycle != LifecycleStopped {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
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

	// Output goes through pipes made here rather than StdoutPipe/StderrPipe:
	// cmd.Wait closes those, which can cut off the last lines a crashing
	// server printed before the scanners have read them.
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		m.mu.Unlock()
		return Status{}, err
	}
	stderr, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdout.Close()
		_ = stdoutWriter.Close()
		m.mu.Unlock()
		return Status{}, err
	}
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter
	closeOutput := func() {
		for _, file := range []*os.File{stdout, stdoutWriter, stderr, stderrWriter} {
			_ = file.Close()
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		closeOutput()
		m.mu.Unlock()
		return Status{}, err
	}

	proc := &managedProcess{
		serverID:         server.ID,
		server:           server,
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

	slog.Info("starting server", "server", server.ID, "name", server.Name, "type", server.Type,
		"version", server.MinecraftVersion, "java", server.JavaPath, "dir", server.Path, "command", commandText)
	if err := cmd.Start(); err != nil {
		closeOutput()
		m.mu.Lock()
		delete(m.running, server.ID)
		m.mu.Unlock()
		slog.Error("could not launch the server process", "server", server.ID, "name", server.Name, "error", err)
		return Status{}, err
	}

	// The child has its own copies of the write ends now.
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	proc.outputReaders = []*os.File{stdout, stderr}
	proc.outputDone.Add(2)
	go m.scanOutput(proc, stdout)
	go m.scanOutput(proc, stderr)
	go m.wait(proc)
	go m.sampleLoop(proc)
	go m.readyWatchdog(proc)
	go m.tickLoop(proc)

	_ = args
	m.emitLifecycle(LifecycleEvent{Type: EventStarted, Server: server})
	status, err := m.waitForStartup(proc, startupGrace)
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
	proc.stopRequested = true
	m.mu.Unlock()
	slog.Info("stopping server", "server", serverID, "force", force)

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
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(proc.cmd.Process.Pid))
		winproc.Hide(kill)
		if err := kill.Run(); err == nil {
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
