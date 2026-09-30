package httpserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/buildinfo"
	"github.com/W1seGit/Cliff/daemon/internal/updater"
)

// preUpdateBackupsToKeep is how many database copies from before an update are retained.
const preUpdateBackupsToKeep = 3

// prepareForUpdate runs once the new version is downloaded, verified and test
// started, and just before its files replace the current ones. It copies the
// database first, so a problem there stops the update with nothing changed,
// then stops any running Minecraft servers so their worlds are saved.
func (h apiHandler) prepareForUpdate(ctx context.Context) error {
	if h.store != nil {
		backup := updater.PreUpdateBackupPath(h.config.DataDir, buildinfo.Version)
		h.updater.SetStage(updater.StageBackup, "Backing up Cliff's settings and server list (a small database copy). Your worlds are not copied.")
		if err := h.store.BackupTo(ctx, backup); err != nil {
			return fmt.Errorf("could not back up the database (nothing was changed): %w", err)
		}
		updater.PruneBackups(updater.PreUpdateBackupDir(h.config.DataDir), preUpdateBackupsToKeep)
	}
	if h.process != nil {
		if h.process.Status().Lifecycle != "stopped" {
			h.updater.SetStage(updater.StageStopping, "Stopping your running Minecraft server and saving the world...")
		}
		h.process.Shutdown(25 * time.Second)
	}
	return nil
}

// updatesCheck returns the current update status. If force=1 is passed,
// it fetches a fresh manifest instead of returning the cached result.
func (h apiHandler) updatesCheck(w http.ResponseWriter, r *http.Request) {
	if h.updater == nil {
		writeError(w, http.StatusServiceUnavailable, "update system not available")
		return
	}

	if r.URL.Query().Get("force") == "1" {
		result := h.updater.CheckNow(r.Context())
		writeJSON(w, http.StatusOK, result)
		return
	}

	result := h.updater.CachedCheck()
	writeJSON(w, http.StatusOK, result)
}

// updatesApply downloads and applies the latest update, then restarts the daemon.
func (h apiHandler) updatesApply(w http.ResponseWriter, r *http.Request) {
	if h.updater == nil {
		writeError(w, http.StatusServiceUnavailable, "update system not available")
		return
	}

	if h.updater.IsApplying() {
		writeError(w, http.StatusConflict, "an update is already being applied")
		return
	}

	result, err := h.updater.Apply(r.Context(), updater.ApplyHooks{BeforeSwap: h.prepareForUpdate})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Send the response first, then restart.
	writeJSON(w, http.StatusOK, result)

	if result.Restarting {
		binaryPath, _ := os.Executable()
		// The old version watches the restart. If the new one does not come up
		// healthy, it puts the old one back and starts it again.
		watchdogErr := h.updater.StartWatchdog(updater.WatchdogParams{
			Host:           h.config.Host,
			Port:           h.config.Port,
			DataDir:        h.config.DataDir,
			ServerRoot:     h.config.ServerRoot,
			WebDir:         h.config.WebDir,
			ExpectVersion:  result.NewVersion,
			FromVersion:    buildinfo.Version,
			TimeoutSeconds: 45,
		})
		if watchdogErr != nil {
			slog.Warn("update will restart without an automatic rollback", "error", watchdogErr)
		}
		updater.RestartAsync(binaryPath, os.Args[1:], 800*time.Millisecond)
	}
}

// updatesProgress reports which step of an update is running, for the progress list.
func (h apiHandler) updatesProgress(w http.ResponseWriter, r *http.Request) {
	if h.updater == nil {
		writeError(w, http.StatusServiceUnavailable, "update system not available")
		return
	}
	writeJSON(w, http.StatusOK, h.updater.Progress())
}

// updatesSafety reports the disk space used by the copies kept for rolling back.
func (h apiHandler) updatesSafety(w http.ResponseWriter, r *http.Request) {
	if h.updater == nil {
		writeError(w, http.StatusServiceUnavailable, "update system not available")
		return
	}
	writeJSON(w, http.StatusOK, h.updater.SafetyInfo())
}

// updatesClearSafety deletes the previous version and the database copies from before updates.
func (h apiHandler) updatesClearSafety(w http.ResponseWriter, r *http.Request) {
	if h.updater == nil {
		writeError(w, http.StatusServiceUnavailable, "update system not available")
		return
	}
	freed, err := h.updater.ClearSafetyCopies()
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "freedBytes": freed, "safety": h.updater.SafetyInfo()})
}

// updatesLastResult returns the outcome of the most recent update until the user dismisses it.
func (h apiHandler) updatesLastResult(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"result": updater.ReadUpdateResult(h.config.DataDir)})
}

func (h apiHandler) updatesDismissLastResult(w http.ResponseWriter, r *http.Request) {
	updater.ClearUpdateResult(h.config.DataDir)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
