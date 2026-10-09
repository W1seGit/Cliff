package httpserver

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

type modFile struct {
	FileName  string       `json:"fileName"`
	Path      string       `json:"path"`
	Enabled   bool         `json:"enabled"`
	Size      int64        `json:"size"`
	UpdatedAt string       `json:"updatedAt"`
	Metadata  *modMetadata `json:"metadata,omitempty"`
}

type modMetadata struct {
	Source             string                 `json:"source"`
	ProjectID          string                 `json:"projectId"`
	Slug               string                 `json:"slug,omitempty"`
	Title              string                 `json:"title"`
	Author             string                 `json:"author,omitempty"`
	Summary            string                 `json:"summary,omitempty"`
	Description        string                 `json:"description,omitempty"`
	IconURL            string                 `json:"iconUrl,omitempty"`
	PageURL            string                 `json:"pageUrl,omitempty"`
	VersionID          string                 `json:"versionId,omitempty"`
	VersionName        string                 `json:"versionName,omitempty"`
	VersionNumber      string                 `json:"versionNumber,omitempty"`
	DependencyWarnings []modDependencyWarning `json:"dependencyWarnings,omitempty"`
	InstalledAt        string                 `json:"installedAt"`
}

type modDependencyWarning struct {
	ProjectID     string `json:"projectId"`
	VersionID     string `json:"versionId,omitempty"`
	Title         string `json:"title"`
	Slug          string `json:"slug,omitempty"`
	Summary       string `json:"summary,omitempty"`
	IconURL       string `json:"iconUrl,omitempty"`
	VersionNumber string `json:"versionNumber,omitempty"`
}

type modSearchOptions struct {
	Version     string
	Loader      string
	Category    string
	ProjectType string
	Sort        string
	Side        string
	Limit       int
	Offset      int
}

var modrinthSortIndex = map[string]string{
	"relevance":  "relevance",
	"downloads":  "downloads",
	"popularity": "follows",
	"updated":    "updated",
}

func (h apiHandler) mods(w http.ResponseWriter, r *http.Request) {
	server, ok, err := h.store.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	if !serverTypeNeedsLoader(server.Type) && !serverTypeNeedsPlugins(server.Type) {
		writeJSON(w, http.StatusOK, map[string]any{"mods": []modFile{}, "disabled": true})
		return
	}
	if r.URL.Query().Get("export") == "mrpack" {
		h.exportMrpack(w, r, server)
		return
	}
	if download := r.URL.Query().Get("download"); download != "" {
		h.downloadMod(w, r, server, download, r.URL.Query().Get("enabled") != "0")
		return
	}
	query := r.URL.Query().Get("q")
	source := r.URL.Query().Get("source")
	projectID := r.URL.Query().Get("projectId")
	details := r.URL.Query().Get("details") == "1"
	options := readModSearchOptions(r, server)
	if projectID != "" && source == "modrinth" && details {
		payload, err := h.modrinthProjectDetails(r, projectID, server, options)
		if err != nil {
			h.writeModSearchError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, payload)
		return
	}
	if projectID != "" && source == "modrinth" {
		versions, err := h.compatibleModrinthVersions(r, projectID, server, options)
		if err != nil {
			h.writeModSearchError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"versions": versions})
		return
	}
	if source == "modrinth" || source == "modrinth-pack" {
		if source == "modrinth-pack" {
			options.ProjectType = "modpack"
		}
		results, nextOffset, err := h.searchModrinth(r, query, server, options)
		if err != nil {
			h.writeModSearchError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results, "nextOffset": nextOffset})
		return
	}
	if source == "curseforge" || source == "curseforge-pack" {
		writeError(w, http.StatusNotImplemented, "CurseForge integration is not available yet. Use Modrinth instead.")
		return
	}
	if source != "" {
		writeJSON(w, http.StatusOK, map[string]any{"results": []any{}, "nextOffset": options.Offset + options.Limit})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mods": listServerMods(server)})
}

func (h apiHandler) modAction(w http.ResponseWriter, r *http.Request) {
	server, ok, err := h.store.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	if !serverTypeNeedsLoader(server.Type) && !serverTypeNeedsPlugins(server.Type) {
		writeError(w, http.StatusBadRequest, "Mods are disabled for this server type")
		return
	}
	if strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
		h.uploadMod(w, r, server)
		return
	}
	var input struct {
		Action   string `json:"action"`
		FileName string `json:"fileName"`
		Enabled  any    `json:"enabled"`
		Mods     []struct {
			FileName string `json:"fileName"`
			Enabled  bool   `json:"enabled"`
		} `json:"mods"`
		FileNames           []string               `json:"fileNames"`
		ProjectID           string                 `json:"projectId"`
		VersionID           string                 `json:"versionId"`
		IncludeDependencies *bool                  `json:"includeDependencies"`
		DependencyWarnings  []modDependencyWarning `json:"dependencyWarnings"`
		Dependencies        []modDependencyWarning `json:"dependencies"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid mod action body")
		return
	}
	// Block deletion while the server is running to prevent file conflicts
	if (input.Action == "delete" || input.Action == "delete-selected") && h.process.IsRunning(server.ID) {
		writeError(w, http.StatusConflict, "Stop the server before deleting mods")
		return
	}
	switch input.Action {
	case "disable":
		if err := moveMod(server, input.FileName, false); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "enable":
		if err := moveMod(server, input.FileName, true); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "delete":
		if err := deleteModFile(server, input.FileName, truthy(input.Enabled)); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "disable-all", "enable-all":
		files, err := moveAllMods(server, input.Action == "enable-all")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files": files})
	case "disable-selected", "enable-selected":
		files, err := moveSelectedMods(server, input.Mods, input.Action == "enable-selected")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files": files})
	case "delete-selected":
		files, err := deleteSelectedMods(server, input.Mods)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files": files})
	case "check-updates":
		h.checkModUpdates(w, r, server)
	case "update-mods":
		h.updateMods(w, r, server, input.FileNames)
	case "modrinth-install-plan":
		plan, err := h.modrinthInstallPlan(r, input.ProjectID, server, input.VersionID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, plan)
	case "modrinth-dependency-details":
		dependencies, err := h.modrinthDependencyDetails(r, input.Dependencies)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"dependencies": dependencies})
	case "install-modrinth":
		includeDependencies := input.IncludeDependencies == nil || *input.IncludeDependencies
		files, err := h.installModrinth(r, input.ProjectID, server, input.VersionID, includeDependencies, input.DependencyWarnings)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files": files})
	case "install-modrinth-modpack":
		files, err := h.installModrinthModpack(r, input.ProjectID, server, input.VersionID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files": files})
	case "install-modrinth-dependencies":
		files, err := h.installModrinthDependencies(r, input.Dependencies, server)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if input.FileName != "" {
			_ = updateModMetadata(server, input.FileName, func(metadata modMetadata) modMetadata {
				metadata.DependencyWarnings = nil
				return metadata
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files": files})
	default:
		writeError(w, http.StatusBadRequest, "Unsupported mod action")
	}
}

func readModSearchOptions(r *http.Request, server store.Server) modSearchOptions {
	query := r.URL.Query()
	limit := atoiDefault(query.Get("limit"), 20)
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	offset := atoiDefault(query.Get("offset"), 0)
	if offset < 0 {
		offset = 0
	}
	return modSearchOptions{
		Version:     firstNonEmpty(query.Get("version"), server.MinecraftVersion),
		Loader:      strings.TrimSpace(query.Get("loader")),
		Category:    strings.TrimSpace(query.Get("category")),
		ProjectType: strings.TrimSpace(query.Get("projectType")),
		Sort:        strings.TrimSpace(query.Get("sort")),
		Side:        strings.TrimSpace(query.Get("side")),
		Limit:       limit,
		Offset:      offset,
	}
}

func (h apiHandler) writeModSearchError(w http.ResponseWriter, err error) {
	message := err.Error()
	if strings.Contains(message, "429") {
		writeError(w, http.StatusTooManyRequests, "Modrinth rate limit reached. Wait a moment before loading more results.")
		return
	}
	writeError(w, http.StatusBadGateway, message)
}

// modsActiveDir returns the active directory for mod/plugin jars based on
// server type. Plugin servers (paper/purpur/folia) use "plugins", while
// loader servers (fabric/forge/neoforge) use "mods".
func modsActiveDir(server store.Server) string {
	if serverTypeNeedsPlugins(server.Type) {
		return "plugins"
	}
	return "mods"
}

// modsDisabledDir returns the disabled directory for mod/plugin jars.
func modsDisabledDir(server store.Server) string {
	if serverTypeNeedsPlugins(server.Type) {
		return ".dashboard-disabled-plugins"
	}
	return ".dashboard-disabled-mods"
}

func listServerMods(server store.Server) []modFile {
	metadata := readModMetadata(server)
	collect := func(dir string, enabled bool) []modFile {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return []modFile{}
		}
		mods := []modFile{}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".jar") {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			fileName := entry.Name()
			meta := metadata[fileName]
			mods = append(mods, modFile{
				FileName:  fileName,
				Path:      filepath.Join(dir, fileName),
				Enabled:   enabled,
				Size:      info.Size(),
				UpdatedAt: info.ModTime().UTC().Format(time.RFC3339),
				Metadata:  meta,
			})
		}
		return mods
	}
	activePath := filepath.Join(server.Path, modsActiveDir(server))
	disabledPath := filepath.Join(server.Path, modsDisabledDir(server))
	mods := append(collect(activePath, true), collect(disabledPath, false)...)
	sort.Slice(mods, func(i, j int) bool { return strings.ToLower(mods[i].FileName) < strings.ToLower(mods[j].FileName) })
	return mods
}

func modMetadataPath(server store.Server) string {
	return filepath.Join(server.Path, ".dashboard-mods.json")
}

func readModMetadata(server store.Server) map[string]*modMetadata {
	metadata := map[string]*modMetadata{}
	data, err := os.ReadFile(modMetadataPath(server))
	if err != nil {
		return metadata
	}
	_ = json.Unmarshal(data, &metadata)
	if metadata == nil {
		return map[string]*modMetadata{}
	}
	return metadata
}

func writeModMetadata(server store.Server, metadata map[string]*modMetadata) error {
	if err := os.MkdirAll(server.Path, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(modMetadataPath(server), append(data, '\n'), 0o644)
}

func saveModMetadata(server store.Server, fileName string, metadata modMetadata) error {
	index := readModMetadata(server)
	index[filepath.Base(fileName)] = &metadata
	return writeModMetadata(server, index)
}

func updateModMetadata(server store.Server, fileName string, updater func(modMetadata) modMetadata) error {
	index := readModMetadata(server)
	safeName := filepath.Base(fileName)
	current := index[safeName]
	if current == nil {
		return nil
	}
	next := updater(*current)
	index[safeName] = &next
	return writeModMetadata(server, index)
}

func removeModMetadata(server store.Server, fileNames []string) error {
	index := readModMetadata(server)
	changed := false
	for _, fileName := range fileNames {
		safeName := filepath.Base(fileName)
		if index[safeName] != nil {
			delete(index, safeName)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return writeModMetadata(server, index)
}

func jsonArrayParam(value string) string {
	data, _ := json.Marshal([]string{value})
	return string(data)
}

func safeBaseName(value string) string {
	return filepath.Base(strings.TrimSpace(value))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstPresent(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}

func stringSliceContains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func truthy(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return typed == "1" || strings.EqualFold(typed, "true")
	case float64:
		return typed != 0
	default:
		return false
	}
}
