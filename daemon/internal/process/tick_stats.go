package process

import (
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	tickProbeInterval = 30 * time.Second
	// tickProbeWindow is how long after a probe its reply lines are hidden from
	// the console, so the sampling does not fill the log.
	tickProbeWindow = 3 * time.Second
	maxTickSamples  = 2880 // 24h at 30s intervals
)

// TickSample is one reading of how fast a server is ticking. MSPT is the
// average milliseconds per tick; TPS is capped at the normal 20.
type TickSample struct {
	At   string   `json:"at"`
	TPS  *float64 `json:"tps"`
	MSPT *float64 `json:"mspt"`
}

var (
	tickQueryAvgRe = regexp.MustCompile(`Average time per tick: ([0-9]+(?:\.[0-9]+)?)\s*ms`)
	paperTPSRe     = regexp.MustCompile(`TPS from last 1m, 5m, 15m: \*?([0-9]+(?:\.[0-9]+)?)`)
	tickReplyRe    = regexp.MustCompile(`The game is running|Target tick rate|Average time per tick|Percentiles:|TPS from last`)
)

// tickProbeCommand picks the console command that reports tick speed for a
// server, or "" when the version offers none. "tick query" exists in vanilla
// style servers from 1.20.3; Paper-family servers also answer "tps" earlier.
func tickProbeCommand(serverType string, minecraftVersion string) string {
	if versionAtLeast(minecraftVersion, 1, 20, 3) {
		return "tick query"
	}
	switch serverType {
	case "paper", "purpur", "folia":
		return "tps"
	}
	return ""
}

// versionAtLeast compares a release version like "1.20.4" or "26.1" with the
// given major.minor.patch. Versions it cannot read (snapshots) count as older.
func versionAtLeast(version string, major int, minor int, patch int) bool {
	parts := strings.Split(strings.TrimSpace(version), ".")
	numbers := [3]int{}
	if len(parts) < 2 || len(parts) > 3 {
		return false
	}
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			return false
		}
		numbers[index] = value
	}
	want := [3]int{major, minor, patch}
	for index := range numbers {
		if numbers[index] != want[index] {
			return numbers[index] > want[index]
		}
	}
	return true
}

// parseTickReply reads a tick-speed value from one console line. It returns
// the sample fields it found and whether the line was a probe reply at all.
func parseTickReply(line string) (tps *float64, mspt *float64, isReply bool) {
	if !tickReplyRe.MatchString(line) {
		return nil, nil, false
	}
	if match := tickQueryAvgRe.FindStringSubmatch(line); match != nil {
		if value, err := strconv.ParseFloat(match[1], 64); err == nil {
			mspt = &value
			rate := 20.0
			if value > 50 {
				rate = 1000 / value
			}
			tps = &rate
		}
	}
	if match := paperTPSRe.FindStringSubmatch(line); match != nil {
		if value, err := strconv.ParseFloat(match[1], 64); err == nil {
			if value > 20 {
				value = 20
			}
			tps = &value
		}
	}
	return tps, mspt, true
}

// handleTickReply records a tick reading and reports whether the line was one
// of the probe replies (which are then kept out of the console).
func (m *Manager) handleTickReply(proc *managedProcess, line string) bool {
	m.mu.Lock()
	probing := time.Now().Before(proc.probeUntil)
	m.mu.Unlock()
	if !probing {
		return false
	}
	tps, mspt, isReply := parseTickReply(line)
	if !isReply {
		return false
	}
	if tps != nil || mspt != nil {
		m.rememberTickSample(proc, TickSample{At: time.Now().UTC().Format(time.RFC3339), TPS: tps, MSPT: mspt})
	}
	return true
}

func (m *Manager) rememberTickSample(proc *managedProcess, sample TickSample) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running[proc.serverID] != proc {
		return
	}
	history, ok := m.usageHistory[proc.serverID]
	if !ok {
		history = &serverUsageHistory{}
		m.usageHistory[proc.serverID] = history
	}
	history.ticks = append(history.ticks, sample)
	if len(history.ticks) > maxTickSamples {
		history.ticks = history.ticks[len(history.ticks)-maxTickSamples:]
	}
	proc.lastTick = &sample
}

// LastTick returns the newest tick reading for a running server.
func (m *Manager) LastTick(serverID string) *TickSample {
	m.mu.Lock()
	defer m.mu.Unlock()
	if proc := m.running[serverID]; proc != nil && proc.lastTick != nil {
		sample := *proc.lastTick
		return &sample
	}
	return nil
}

// tickLoop asks the server how fast it is ticking every half minute once it
// is ready. Servers that offer no such command are not probed.
func (m *Manager) tickLoop(proc *managedProcess) {
	command := tickProbeCommand(proc.server.Type, proc.server.MinecraftVersion)
	if command == "" {
		return
	}
	select {
	case <-proc.ready:
	case <-proc.exited:
		return
	}
	ticker := time.NewTicker(tickProbeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-proc.exited:
			return
		case <-ticker.C:
			m.mu.Lock()
			live := m.running[proc.serverID] == proc && proc.lifecycle == LifecycleRunning && proc.stdin != nil
			if live {
				proc.probeUntil = time.Now().Add(tickProbeWindow)
			}
			m.mu.Unlock()
			if live {
				_, _ = io.WriteString(proc.stdin, command+"\n")
			}
		}
	}
}
