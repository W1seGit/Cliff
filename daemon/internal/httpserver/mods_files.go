package httpserver

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func (h apiHandler) downloadMod(w http.ResponseWriter, r *http.Request, server store.Server, fileName string, enabled bool) {
	safeName := safeBaseName(fileName)
	if !strings.HasSuffix(strings.ToLower(safeName), ".jar") {
		writeError(w, http.StatusBadRequest, "Only .jar mod files can be downloaded")
		return
	}
	dir := modsDisabledDir(server)
	if enabled {
		dir = modsActiveDir(server)
	}
	target := filepath.Join(server.Path, dir, safeName)
	if !fileExists(target) {
		writeError(w, http.StatusNotFound, "Mod file not found")
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(safeName, `"`, "")+`"`)
	w.Header().Set("Content-Type", "application/java-archive")
	http.ServeFile(w, r, target)
}

func (h apiHandler) uploadMod(w http.ResponseWriter, r *http.Request, server store.Server) {
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "Mod upload form could not be read")
		return
	}

	action := ""
	uploadedName := ""
	session := &uploadSession{h: h, ctx: r.Context(), server: server}
	var results []uploadResult
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "Mod upload form could not be read")
			return
		}
		switch part.FormName() {
		case "action":
			value, err := readMultipartTextPart(part)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			action = value
			if action != "upload" && action != "upload-auto" {
				writeError(w, http.StatusBadRequest, "Unsupported mod upload action")
				return
			}
		case "worldName":
			value, err := readMultipartTextPart(part)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			session.worldName = value
		case "file":
			if action == "upload-auto" {
				results = append(results, session.handlePart(part)...)
				continue
			}
			if action != "upload" {
				writeError(w, http.StatusBadRequest, "Upload action must be sent before the mod jar")
				return
			}
			safeName, err := uniqueModFileName(server, part.FileName())
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			modsPath := filepath.Join(server.Path, modsActiveDir(server))
			if err := os.MkdirAll(modsPath, 0o755); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if err := writeUploadedFile(part, filepath.Join(modsPath, safeName)); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			uploadedName = safeName
		}
	}
	if action == "upload-auto" {
		if len(results) == 0 {
			writeError(w, http.StatusBadRequest, "No files were uploaded")
			return
		}
		added := []string{}
		for _, result := range results {
			if result.Status == "added" {
				added = append(added, result.Name)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": len(added) > 0, "files": added, "results": results})
		return
	}
	if action != "upload" {
		writeError(w, http.StatusBadRequest, "Unsupported mod upload action")
		return
	}
	if uploadedName == "" {
		writeError(w, http.StatusBadRequest, "Mod jar is required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files": []string{uploadedName}})
}

func uniqueModFileName(server store.Server, fileName string) (string, error) {
	safeName := safeBaseName(fileName)
	if !strings.HasSuffix(strings.ToLower(safeName), ".jar") {
		return "", errors.New("Only .jar mod files are supported")
	}
	extension := filepath.Ext(safeName)
	base := strings.TrimSuffix(safeName, extension)
	candidate := safeName
	index := 2
	activeDir := modsActiveDir(server)
	disabledDir := modsDisabledDir(server)
	for fileExists(filepath.Join(server.Path, activeDir, candidate)) || fileExists(filepath.Join(server.Path, disabledDir, candidate)) {
		candidate = base + "-" + strconv.Itoa(index) + extension
		index++
	}
	return candidate, nil
}

func moveMod(server store.Server, fileName string, enable bool) error {
	safeName := safeBaseName(fileName)
	if !strings.HasSuffix(strings.ToLower(safeName), ".jar") {
		return errors.New("Only .jar mod files can be moved")
	}
	activePath := filepath.Join(server.Path, modsActiveDir(server))
	disabledPath := filepath.Join(server.Path, modsDisabledDir(server))
	_ = os.MkdirAll(activePath, 0o755)
	_ = os.MkdirAll(disabledPath, 0o755)
	source := filepath.Join(activePath, safeName)
	destination := filepath.Join(disabledPath, safeName)
	if enable {
		source, destination = destination, source
	}
	if !fileExists(source) {
		return errors.New("Mod file not found")
	}
	if fileExists(destination) {
		return errors.New("A mod file with that name already exists")
	}
	return os.Rename(source, destination)
}

func moveAllMods(server store.Server, enable bool) ([]string, error) {
	activePath := filepath.Join(server.Path, modsActiveDir(server))
	disabledPath := filepath.Join(server.Path, modsDisabledDir(server))
	if err := os.MkdirAll(activePath, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(disabledPath, 0o755); err != nil {
		return nil, err
	}
	sourceDir := activePath
	if enable {
		sourceDir = disabledPath
	}
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	moved := []string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".jar") {
			continue
		}
		if err := moveMod(server, entry.Name(), enable); err != nil {
			return moved, err
		}
		moved = append(moved, entry.Name())
	}
	return moved, nil
}

func moveSelectedMods(server store.Server, mods []struct {
	FileName string `json:"fileName"`
	Enabled  bool   `json:"enabled"`
}, enable bool) ([]string, error) {
	moved := []string{}
	for _, mod := range mods {
		if mod.Enabled == enable {
			continue
		}
		if err := moveMod(server, mod.FileName, enable); err == nil {
			moved = append(moved, safeBaseName(mod.FileName))
		}
	}
	return moved, nil
}

func deleteModFile(server store.Server, fileName string, enabled bool) error {
	safeName := safeBaseName(fileName)
	if !strings.HasSuffix(strings.ToLower(safeName), ".jar") {
		return errors.New("Only .jar mod files can be deleted")
	}
	dir := modsDisabledDir(server)
	if enabled {
		dir = modsActiveDir(server)
	}
	target := filepath.Join(server.Path, dir, safeName)
	if !fileExists(target) {
		return errors.New("Mod file not found")
	}
	if err := os.Remove(target); err != nil {
		return err
	}
	return removeModMetadata(server, []string{safeName})
}

func deleteSelectedMods(server store.Server, mods []struct {
	FileName string `json:"fileName"`
	Enabled  bool   `json:"enabled"`
}) ([]string, error) {
	deleted := []string{}
	for _, mod := range mods {
		if err := deleteModFile(server, mod.FileName, mod.Enabled); err == nil {
			deleted = append(deleted, safeBaseName(mod.FileName))
		}
	}
	return deleted, nil
}
