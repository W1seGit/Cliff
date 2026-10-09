package httpserver

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func (h apiHandler) command(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Action   string `json:"action"`
		Command  string `json:"command"`
		PresetID string `json:"presetId"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid command body")
		return
	}
	if input.Action == "save-preset" {
		if _, err := h.store.SaveCommandPreset(r.Context(), r.PathValue("id"), input.Command); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		presets, err := h.store.ListCommandPresets(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "presets": presets})
		return
	}
	if input.Action == "delete-preset" {
		if err := h.store.DeleteCommandPreset(r.Context(), r.PathValue("id"), input.PresetID); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		presets, err := h.store.ListCommandPresets(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "presets": presets})
		return
	}
	if err := h.process.Command(r.PathValue("id"), input.Command); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.process.StatusForLight(r.PathValue("id")))
}

func (h apiHandler) commandPresets(w http.ResponseWriter, r *http.Request) {
	if _, ok, err := h.store.GetServer(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	presets, err := h.store.ListCommandPresets(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string][]store.CommandPreset{"presets": presets})
}

func (h apiHandler) logs(w http.ResponseWriter, r *http.Request) {
	logs := h.process.Logs(r.PathValue("id"))
	if r.URL.Query().Get("download") == "1" {
		server, ok, err := h.store.GetServer(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "Server not found")
			return
		}
		w.Header().Set("Content-Disposition", `attachment; filename="`+safeLogFileName(server.Name)+`"`)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		for _, line := range logs {
			_, _ = w.Write([]byte(line + "\n"))
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"logs": logs,
	})
}

func safeLogFileName(name string) string {
	trimmed := strings.Trim(strings.TrimSpace(regexp.MustCompile(`[^a-zA-Z0-9._-]+`).ReplaceAllString(name, "-")), "-")
	if trimmed == "" {
		trimmed = "server"
	}
	return trimmed + "-console.log"
}

func (h apiHandler) daemonLogs(w http.ResponseWriter, r *http.Request) {
	// "full=1" reads the entire log file from disk instead of the in-memory buffer
	if r.URL.Query().Get("full") == "1" {
		logPath := filepath.Join(h.config.DataDir, "logs", "daemon.log")
		data, err := os.ReadFile(logPath)
		if err != nil {
			if os.IsNotExist(err) {
				writeJSON(w, http.StatusOK, map[string][]string{"logs": {}})
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		lines := splitLogLines(string(data))
		if r.URL.Query().Get("download") == "1" {
			w.Header().Set("Content-Disposition", `attachment; filename="daemon.log"`)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			for _, line := range lines {
				_, _ = w.Write([]byte(line + "\n"))
			}
			return
		}
		writeJSON(w, http.StatusOK, map[string][]string{"logs": lines})
		return
	}

	if h.logBuffer == nil {
		writeJSON(w, http.StatusOK, map[string][]string{"logs": {}})
		return
	}
	lines := h.logBuffer.Lines()
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="daemon.log"`)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		for _, line := range lines {
			_, _ = w.Write([]byte(line + "\n"))
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string][]string{"logs": lines})
}

func splitLogLines(data string) []string {
	data = strings.TrimRight(data, "\r\n")
	if data == "" {
		return []string{}
	}
	return strings.Split(data, "\n")
}
