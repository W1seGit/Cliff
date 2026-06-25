package httpserver

import (
	"net/http"
	"os"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/updater"
)

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

	// Stop any running Minecraft servers before applying.
	if h.process != nil {
		h.process.Shutdown(15 * time.Second)
	}

	result, err := h.updater.Apply(r.Context())
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
