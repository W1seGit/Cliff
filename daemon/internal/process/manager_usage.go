package process

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

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
	Ticks        []TickSample   `json:"ticks,omitempty"`
	LastSampleAt string         `json:"lastSampleAt"`
	MemoryLimit  int64          `json:"memoryLimit"`
}

// saveUsageHistory writes a server's usage history to disk.
func (m *Manager) saveUsageHistory(serverID string) {
	h, ok := m.usageHistory[serverID]
	if !ok || (len(h.usage) == 0 && len(h.players) == 0 && len(h.ticks) == 0) {
		return
	}
	path := m.usageHistoryPath(serverID)
	data := persistedUsageHistory{
		Usage:        h.usage,
		Players:      h.players,
		Ticks:        h.ticks,
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
		ticks:        data.Ticks,
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
	var ticksFiltered []TickSample
	for _, s := range h.ticks {
		if t, err := time.Parse(time.RFC3339, s.At); err == nil && t.After(cutoff) {
			ticksFiltered = append(ticksFiltered, s)
		}
	}
	var lastTick *TickSample
	if proc != nil && proc.lastTick != nil {
		sample := *proc.lastTick
		lastTick = &sample
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
	if len(ticksFiltered) > maxSamples {
		ticksFiltered = downsampleTicks(ticksFiltered, maxSamples)
	}

	return &Usage{
		MemoryLimitBytes: &memLimit,
		Samples:          usageFiltered,
		PlayerSamples:    playerFiltered,
		TickSamples:      ticksFiltered,
		Tick:             lastTick,
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

// downsampleTicks averages tick readings into buckets, like downsampleUsage.
func downsampleTicks(samples []TickSample, target int) []TickSample {
	if len(samples) <= target || target <= 0 {
		return samples
	}
	step := len(samples) / target
	result := make([]TickSample, 0, target)
	for i := 0; i < len(samples); i += step {
		var tpsSum, msptSum float64
		var tpsCount, msptCount int
		end := min(i+step, len(samples))
		for j := i; j < end; j++ {
			if samples[j].TPS != nil {
				tpsSum += *samples[j].TPS
				tpsCount++
			}
			if samples[j].MSPT != nil {
				msptSum += *samples[j].MSPT
				msptCount++
			}
		}
		s := TickSample{At: samples[i].At}
		if tpsCount > 0 {
			v := tpsSum / float64(tpsCount)
			s.TPS = &v
		}
		if msptCount > 0 {
			v := msptSum / float64(msptCount)
			s.MSPT = &v
		}
		result = append(result, s)
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
