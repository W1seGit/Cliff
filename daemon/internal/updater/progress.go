package updater

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Stages of an update, in the order the user sees them.
const (
	StageDownloading = "downloading"
	StageVerifying   = "verifying"
	StageChecking    = "checking"
	StageBackup      = "backup"
	StageStopping    = "stopping"
	StageInstalling  = "installing"
	StageRestarting  = "restarting"
	StageFailed      = "failed"
)

// Progress is what the dashboard polls while an update runs.
type Progress struct {
	Active      bool   `json:"active"`
	Stage       string `json:"stage"`
	Message     string `json:"message"`
	FromVersion string `json:"fromVersion,omitempty"`
	ToVersion   string `json:"toVersion,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
}

func (m *Manager) beginProgress(from string, to string) {
	m.mu.Lock()
	m.progress = Progress{Active: true, FromVersion: from, ToVersion: to, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	m.mu.Unlock()
}

// SetStage records the current step and a plain-language description of it.
func (m *Manager) SetStage(stage string, message string) {
	m.mu.Lock()
	m.progress.Active = stage != StageFailed
	m.progress.Stage = stage
	m.progress.Message = message
	m.progress.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	hook := m.stageHook
	m.mu.Unlock()
	if hook != nil {
		hook(stage, message)
	}
}

// SetStageHook makes the manager call fn at every step, so a terminal can print progress.
func (m *Manager) SetStageHook(fn func(stage string, message string)) {
	m.mu.Lock()
	m.stageHook = fn
	m.mu.Unlock()
}

// Progress returns the current or most recent update step.
func (m *Manager) Progress() Progress {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.progress
}

// UpdateResult is the outcome of the last update, written by whichever process
// supervised the restart, and shown to the user once after Cliff is back.
type UpdateResult struct {
	// Status is "updated", "rolled-back" or "failed".
	Status  string `json:"status"`
	From    string `json:"from"`
	To      string `json:"to"`
	Message string `json:"message"`
	At      string `json:"at"`
	// Restarted lists the servers that were running before the update and are running again.
	Restarted []string `json:"restarted,omitempty"`
	// NotRestarted lists servers that could not be started again, each with the reason.
	NotRestarted []string `json:"notRestarted,omitempty"`
}

func updateResultPath(dataDir string) string {
	return filepath.Join(dataDir, "updates", "last-update.json")
}

// WriteUpdateResult stores the outcome of an update for the dashboard to show.
func WriteUpdateResult(dataDir string, result UpdateResult) error {
	if result.At == "" {
		result.At = time.Now().UTC().Format(time.RFC3339)
	}
	if err := os.MkdirAll(filepath.Dir(updateResultPath(dataDir)), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(updateResultPath(dataDir), data, 0o644)
}

// ReadUpdateResult returns the last update outcome that has not been dismissed, or nil.
func ReadUpdateResult(dataDir string) *UpdateResult {
	data, err := os.ReadFile(updateResultPath(dataDir))
	if err != nil {
		return nil
	}
	var result UpdateResult
	if err := json.Unmarshal(data, &result); err != nil || result.Status == "" {
		return nil
	}
	return &result
}

// ClearUpdateResult forgets the last update outcome once the user has seen it.
func ClearUpdateResult(dataDir string) {
	_ = os.Remove(updateResultPath(dataDir))
}

// humanSize formats a byte count for messages.
func humanSize(bytes int64) string {
	switch {
	case bytes >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	case bytes >= 1024:
		return fmt.Sprintf("%d KB", bytes/1024)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// HumanSize is humanSize for other packages.
func HumanSize(bytes int64) string { return humanSize(bytes) }
