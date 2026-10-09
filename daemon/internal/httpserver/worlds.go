package httpserver

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

type datapackInfo struct {
	Name      string       `json:"name"`
	Size      int64        `json:"size"`
	UpdatedAt string       `json:"updatedAt"`
	Enabled   bool         `json:"enabled"`
	Metadata  *modMetadata `json:"metadata,omitempty"`
}

type worldInfo struct {
	Name        string         `json:"name"`
	Active      bool           `json:"active"`
	Path        string         `json:"path"`
	UpdatedAt   string         `json:"updatedAt"`
	PlayerFiles int            `json:"playerFiles"`
	Datapacks   []datapackInfo `json:"datapacks"`
}

type worldsPayload struct {
	ActiveWorld string      `json:"activeWorld"`
	Worlds      []worldInfo `json:"worlds"`
}

func (h apiHandler) worlds(w http.ResponseWriter, r *http.Request) {
	server, ok, err := h.store.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	projectID := r.URL.Query().Get("projectId")
	details := r.URL.Query().Get("details") == "1"
	if projectID != "" && r.URL.Query().Get("source") == "modrinth-datapack" && details {
		payload, err := h.modrinthDatapackProjectDetails(r, projectID, server, r.URL.Query().Get("version"))
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, payload)
		return
	}
	if r.URL.Query().Get("source") == "modrinth-datapack" {
		results, err := searchModrinthDatapacks(r, server)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results})
		return
	}
	if name := r.URL.Query().Get("download"); name != "" {
		worldPath, fileName, rootName, err := worldArchiveTarget(server, name)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeZipArchive(w, fileName, worldPath, rootName)
		return
	}
	if name := r.URL.Query().Get("datapack"); name != "" {
		target, fileName, err := datapackDownloadTarget(server, r.URL.Query().Get("world"), name)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+fileName+`"`)
		http.ServeFile(w, r, target)
		return
	}
	writeJSON(w, http.StatusOK, listWorlds(server))
}

func boundedQueryInt(r *http.Request, key string, fallback int, min int, max int) int {
	value, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return fallback
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func (h apiHandler) worldAction(w http.ResponseWriter, r *http.Request) {
	server, ok, err := h.store.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	if strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
		h.worldUploadAction(w, r, server)
		return
	}

	var input struct {
		Action        string   `json:"action"`
		WorldName     string   `json:"worldName"`
		NextWorldName string   `json:"nextWorldName"`
		FileName      string   `json:"fileName"`
		FileNames     []string `json:"fileNames"`
		ProjectID     string   `json:"projectId"`
		VersionID     string   `json:"versionId"`
		Enabled       bool     `json:"enabled"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid world action body")
		return
	}

	// Block datapack deletion while the server is running to prevent file conflicts
	if (input.Action == "delete-datapack" || input.Action == "delete-selected-datapacks") && h.process.IsRunning(server.ID) {
		writeError(w, http.StatusConflict, "Stop the server before deleting datapacks")
		return
	}

	switch input.Action {
	case "set-active":
		if err := setActiveWorld(server, input.WorldName); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	case "delete-world":
		if err := deleteWorld(server, input.WorldName); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	case "rename-world":
		if err := renameWorld(server, input.WorldName, input.NextWorldName); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	case "delete-datapack":
		if err := deleteDatapack(server, input.WorldName, input.FileName); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	case "toggle-datapack":
		if err := setDatapackEnabled(server, input.WorldName, input.FileName, input.Enabled); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	case "enable-selected-datapacks":
		if err := setSelectedDatapacksEnabled(server, input.WorldName, input.FileNames, true); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	case "disable-selected-datapacks":
		if err := setSelectedDatapacksEnabled(server, input.WorldName, input.FileNames, false); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	case "delete-selected-datapacks":
		if err := deleteSelectedDatapacks(server, input.WorldName, input.FileNames); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	case "install-modrinth-datapack":
		if strings.TrimSpace(input.WorldName) == "" {
			writeError(w, http.StatusBadRequest, "World name is required")
			return
		}
		if strings.TrimSpace(input.ProjectID) == "" {
			writeError(w, http.StatusBadRequest, "Modrinth project id is required")
			return
		}
		files, err := h.installModrinthDatapack(r, server, input.WorldName, input.ProjectID, input.VersionID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		payload := listWorlds(server)
		writeJSON(w, http.StatusOK, map[string]any{"activeWorld": payload.ActiveWorld, "worlds": payload.Worlds, "files": files})
		return
	default:
		writeError(w, http.StatusBadRequest, "Unsupported world action")
		return
	}
	writeJSON(w, http.StatusOK, listWorlds(server))
}

func (h apiHandler) worldUploadAction(w http.ResponseWriter, r *http.Request, server store.Server) {
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "World upload form could not be read")
		return
	}

	action := ""
	worldName := ""
	uploaded := false
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "World upload form could not be read")
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
			if action != "import-world-zip" && action != "upload-datapack" {
				writeError(w, http.StatusBadRequest, "Unsupported world upload action")
				return
			}
		case "worldName":
			value, err := readMultipartTextPart(part)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			worldName = value
		case "file":
			if action == "" {
				writeError(w, http.StatusBadRequest, "Upload action must be sent before the file")
				return
			}
			switch action {
			case "import-world-zip":
				if err := importWorldZipFromPart(server, part, worldName); err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
			case "upload-datapack":
				if err := writeDatapack(server, worldName, part.FileName(), part); err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
			}
			uploaded = true
		}
	}
	if action != "import-world-zip" && action != "upload-datapack" {
		writeError(w, http.StatusBadRequest, "Unsupported world upload action")
		return
	}
	if !uploaded {
		writeError(w, http.StatusBadRequest, "Upload file is required")
		return
	}
	writeJSON(w, http.StatusOK, listWorlds(server))
}

func listWorlds(server store.Server) worldsPayload {
	activeWorld := stringProperty(readPropertiesRaw(filepath.Join(server.Path, "server.properties")), "level-name", "world")
	entries, err := os.ReadDir(server.Path)
	if err != nil {
		return worldsPayload{ActiveWorld: activeWorld, Worlds: []worldInfo{}}
	}
	worlds := []worldInfo{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		worldPath := filepath.Join(server.Path, entry.Name())
		if entry.Name() != activeWorld && !fileExists(filepath.Join(worldPath, "level.dat")) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		worlds = append(worlds, worldInfo{
			Name:        entry.Name(),
			Active:      entry.Name() == activeWorld,
			Path:        worldPath,
			UpdatedAt:   info.ModTime().UTC().Format(time.RFC3339),
			PlayerFiles: countPlayerFiles(filepath.Join(worldPath, "playerdata")),
			Datapacks:   listDatapacks(filepath.Join(worldPath, "datapacks"), server, entry.Name()),
		})
	}
	sort.Slice(worlds, func(left int, right int) bool {
		if worlds[left].Active != worlds[right].Active {
			return worlds[left].Active
		}
		return worlds[left].Name < worlds[right].Name
	})
	return worldsPayload{ActiveWorld: activeWorld, Worlds: worlds}
}

func countPlayerFiles(path string) int {
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".dat") {
			count++
		}
	}
	return count
}

func setActiveWorld(server store.Server, worldName string) error {
	safeWorld, err := safePathSegment(worldName, "World name")
	if err != nil {
		return err
	}
	worldPath, err := resolveInside(server.Path, safeWorld)
	if err != nil {
		return err
	}
	if info, err := os.Stat(worldPath); err != nil || !info.IsDir() {
		return errors.New("World folder not found")
	}
	return writeServerPropertiesFile(server, map[string]any{"levelName": safeWorld}, nil)
}

func deleteWorld(server store.Server, worldName string) error {
	safeWorld, err := safePathSegment(worldName, "World name")
	if err != nil {
		return err
	}
	activeWorld := stringProperty(readPropertiesRaw(filepath.Join(server.Path, "server.properties")), "level-name", "world")
	if safeWorld == activeWorld {
		return errors.New("Switch to another world before deleting the active world")
	}
	worldPath, err := resolveInside(server.Path, safeWorld)
	if err != nil {
		return err
	}
	if info, err := os.Stat(worldPath); err != nil || !info.IsDir() {
		return errors.New("World folder not found")
	}
	if !fileExists(filepath.Join(worldPath, "level.dat")) {
		return errors.New("Only Minecraft world folders can be deleted here")
	}
	return os.RemoveAll(worldPath)
}

func renameWorld(server store.Server, worldName string, nextWorldName string) error {
	safeWorld, err := safePathSegment(worldName, "World name")
	if err != nil {
		return err
	}
	safeNextWorld, err := safePathSegment(nextWorldName, "New world name")
	if err != nil {
		return err
	}
	if safeWorld == safeNextWorld {
		return errors.New("Choose a different world name")
	}
	worldPath, err := resolveInside(server.Path, safeWorld)
	if err != nil {
		return err
	}
	nextWorldPath, err := resolveInside(server.Path, safeNextWorld)
	if err != nil {
		return err
	}
	if info, err := os.Stat(worldPath); err != nil || !info.IsDir() {
		return errors.New("World folder not found")
	}
	if !fileExists(filepath.Join(worldPath, "level.dat")) {
		return errors.New("Only Minecraft world folders can be renamed here")
	}
	if _, err := os.Stat(nextWorldPath); err == nil {
		return errors.New("A world with that name already exists")
	}
	if err := os.Rename(worldPath, nextWorldPath); err != nil {
		return err
	}
	activeWorld := stringProperty(readPropertiesRaw(filepath.Join(server.Path, "server.properties")), "level-name", "world")
	if activeWorld == safeWorld {
		return writeServerPropertiesFile(server, map[string]any{"levelName": safeNextWorld}, nil)
	}
	return nil
}

func ensureAvailableWorldTarget(server store.Server, worldName string) (string, string, error) {
	safeWorld, err := safePathSegment(worldName, "World name")
	if err != nil {
		return "", "", err
	}
	target, err := resolveInside(server.Path, safeWorld)
	if err != nil {
		return "", "", err
	}
	if entries, err := os.ReadDir(target); err == nil && len(entries) > 0 {
		return "", "", errors.New("A world with that name already exists")
	}
	return safeWorld, target, nil
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func removeDisabledSuffix(value string) string {
	if strings.HasSuffix(strings.ToLower(value), ".disabled") {
		return value[:len(value)-len(".disabled")]
	}
	return value
}
