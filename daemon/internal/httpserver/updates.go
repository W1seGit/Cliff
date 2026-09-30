package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/buildinfo"
	"github.com/W1seGit/Cliff/daemon/internal/updater"
)

// preUpdateBackupsToKeep is how many database copies from before an update are retained.
const preUpdateBackupsToKeep = 3

// prepareForUpdate runs once the new version is downloaded and verified, and
// just before its files replace the current ones. It copies the database first,
// so a problem there stops the update with nothing changed, then stops any
// running Minecraft servers so their worlds are saved.
func (h apiHandler) prepareForUpdate(ctx context.Context) error {
	if h.store != nil {
		backup := updater.PreUpdateBackupPath(h.config.DataDir, buildinfo.Version)
		if err := h.store.BackupTo(ctx, backup); err != nil {
			return fmt.Errorf("could not back up the database (nothing was changed): %w", err)
		}
		updater.PruneBackups(updater.PreUpdateBackupDir(h.config.DataDir), preUpdateBackupsToKeep)
	}
	if h.process != nil {
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
		updater.RestartAsync(binaryPath, os.Args[1:], 800*time.Millisecond)
	}
}
