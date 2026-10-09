package process

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"time"
)

func (m *Manager) scanOutput(proc *managedProcess, reader io.Reader) {
	defer proc.outputDone.Done()
	scanner := bufio.NewScanner(reader)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if m.handleTickReply(proc, line) {
			continue
		}
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
	if match := playerJoinRe.FindStringSubmatch(line); match != nil {
		m.mu.Lock()
		counted := m.running[proc.serverID] == proc
		if counted {
			proc.playerCount++
		}
		count := proc.playerCount
		m.mu.Unlock()
		if counted {
			m.emitLifecycle(LifecycleEvent{Type: EventPlayerJoin, Server: proc.server, Player: match[1], PlayerCount: count})
		}
		return
	}
	// Player leave detection
	if match := playerLeaveRe.FindStringSubmatch(line); match != nil {
		m.mu.Lock()
		counted := m.running[proc.serverID] == proc
		if counted && proc.playerCount > 0 {
			proc.playerCount--
		}
		count := proc.playerCount
		m.mu.Unlock()
		if counted {
			m.emitLifecycle(LifecycleEvent{Type: EventPlayerLeave, Server: proc.server, Player: match[1], PlayerCount: count})
		}
		return
	}
}

func (m *Manager) wait(proc *managedProcess) {
	err := proc.cmd.Wait()
	// The readers reach end-of-file once the server (and anything it started
	// holding the pipe) is gone; after a grace period they are closed anyway.
	waitForOutputScanners(proc, 2*time.Second)
	for _, reader := range proc.outputReaders {
		_ = reader.Close()
	}
	message := "Server exited"
	if err != nil {
		message = "Server exited: " + err.Error()
	}
	m.pushLog(proc, message)
	m.logExit(proc, err)

	exitCode := -1
	if proc.cmd.ProcessState != nil {
		exitCode = proc.cmd.ProcessState.ExitCode()
	}
	m.mu.Lock()
	stopRequested := proc.stopRequested
	wasReady := !proc.readyAt.IsZero()
	tail := lastLogLines(proc.logs, 15)
	if m.running[proc.serverID] == proc {
		m.rememberLocked(proc.serverID, proc.logs)
		delete(m.running, proc.serverID)
	}
	m.mu.Unlock()
	proc.exitOnce.Do(func() { close(proc.exited) })
	m.publish(Event{Type: "status", ServerID: proc.serverID, Status: m.StatusLight()})

	// An exit nobody asked for with a non-zero code is a crash. A clean exit
	// (the in-game /stop command, for instance) counts as a normal stop.
	event := LifecycleEvent{
		Type:     EventStopped,
		Server:   proc.server,
		ExitCode: exitCode,
		Uptime:   time.Since(proc.startedAt),
		WasReady: wasReady,
	}
	if !stopRequested && exitCode != 0 {
		event.Type = EventCrashed
		event.LastOutput = tail
	}
	m.emitLifecycle(event)
}

// logExit records how a server process ended. An exit the user asked for is
// routine; one during startup or after the server was running is not, so the
// last lines of its output are kept in the daemon log for diagnosis.
func (m *Manager) logExit(proc *managedProcess, waitErr error) {
	exitCode := -1
	if proc.cmd.ProcessState != nil {
		exitCode = proc.cmd.ProcessState.ExitCode()
	}
	m.mu.Lock()
	stopRequested := proc.stopRequested
	wasReady := !proc.readyAt.IsZero()
	tail := lastLogLines(proc.logs, 15)
	m.mu.Unlock()
	uptime := time.Since(proc.startedAt).Round(time.Second).String()

	switch {
	case stopRequested:
		slog.Info("server stopped", "server", proc.serverID, "exitCode", exitCode, "uptime", uptime)
	case !wasReady:
		slog.Error("server exited before it finished starting", "server", proc.serverID, "exitCode", exitCode, "uptime", uptime, "error", waitErr, "lastOutput", tail)
	default:
		slog.Error("server stopped unexpectedly", "server", proc.serverID, "exitCode", exitCode, "uptime", uptime, "error", waitErr, "lastOutput", tail)
	}
}

// lastLogLines joins the last n non-empty lines with " | " so one log entry
// tells the whole story.
func lastLogLines(lines []string, n int) string {
	picked := make([]string, 0, n)
	for index := len(lines) - 1; index >= 0 && len(picked) < n; index-- {
		if line := strings.TrimSpace(lines[index]); line != "" {
			picked = append([]string{line}, picked...)
		}
	}
	return strings.Join(picked, " | ")
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
		m.mu.Lock()
		proc.readyAt = time.Now()
		m.mu.Unlock()
		slog.Info("server is ready", "server", proc.serverID, "startup", time.Since(proc.startedAt).Round(100*time.Millisecond).String())
		m.emitLifecycle(LifecycleEvent{Type: EventReady, Server: proc.server, Uptime: time.Since(proc.startedAt), WasReady: true})
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
		// Still booting. Report "starting" and let the "Done (" line, an exit,
		// or the ready watchdog decide what happens next.
		return m.StatusForLight(proc.serverID), nil
	}
}

// readyWatchdog gives up waiting for the ready message after readyTimeout so a
// server that never prints "Done (" does not stay "starting" forever.
func (m *Manager) readyWatchdog(proc *managedProcess) {
	timer := time.NewTimer(readyTimeout)
	defer timer.Stop()
	select {
	case <-proc.ready:
	case <-proc.exited:
	case <-timer.C:
		m.pushLog(proc, fmt.Sprintf("Cliff: no ready message after %s, treating the server as running", readyTimeout.Round(time.Second)))
		slog.Warn("server never printed its ready message; treating it as running", "server", proc.serverID, "after", readyTimeout.Round(time.Second).String())
		m.markRunning(proc)
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
