package httpserver

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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

type modrinthProject struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	ProjectType string `json:"project_type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Body        string `json:"body"`
	IconURL     string `json:"icon_url"`
	Downloads   int64  `json:"downloads"`
	Followers   int64  `json:"followers"`
}

type modrinthDependency struct {
	VersionID      string `json:"version_id"`
	ProjectID      string `json:"project_id"`
	DependencyType string `json:"dependency_type"`
}

type modrinthVersion struct {
	ID            string               `json:"id"`
	ProjectID     string               `json:"project_id"`
	Name          string               `json:"name"`
	VersionNumber string               `json:"version_number"`
	GameVersions  []string             `json:"game_versions"`
	Loaders       []string             `json:"loaders"`
	Dependencies  []modrinthDependency `json:"dependencies"`
	Files         []struct {
		Primary  bool   `json:"primary"`
		URL      string `json:"url"`
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
	} `json:"files"`
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
		writeJSON(w, http.StatusOK, map[string]any{"disabled": true, "results": []any{}, "nextOffset": options.Offset + options.Limit})
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
		if err := h.createAutoSnapshot(r.Context(), server, "before deleting "+safeBaseName(input.FileName)); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
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
		if err := h.createAutoSnapshot(r.Context(), server, "before deleting selected mods"); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		files, err := deleteSelectedMods(server, input.Mods)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files": files})
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
		if err := h.createAutoSnapshot(r.Context(), server, "before install-modrinth"); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		includeDependencies := input.IncludeDependencies == nil || *input.IncludeDependencies
		files, err := h.installModrinth(r, input.ProjectID, server, input.VersionID, includeDependencies, input.DependencyWarnings)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files": files})
	case "install-modrinth-modpack":
		if err := h.createAutoSnapshot(r.Context(), server, "before installing modpack"); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		files, err := h.installModrinthModpack(r, input.ProjectID, server, input.VersionID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files": files})
	case "install-modrinth-dependencies":
		if err := h.createAutoSnapshot(r.Context(), server, "before installing mod dependencies"); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
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
			if action != "upload" {
				writeError(w, http.StatusBadRequest, "Unsupported mod upload action")
				return
			}
		case "file":
			if action != "upload" {
				writeError(w, http.StatusBadRequest, "Upload action must be sent before the mod jar")
				return
			}
			safeName, err := uniqueModFileName(server, part.FileName())
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if err := h.createAutoSnapshot(r.Context(), server, "before uploading "+safeName); err != nil {
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

func (h apiHandler) searchModrinth(r *http.Request, query string, server store.Server, options modSearchOptions) ([]map[string]any, int, error) {
	version := firstNonEmpty(options.Version, server.MinecraftVersion)
	requestLimit := options.Limit
	if requestLimit > 100 {
		requestLimit = 100
	}
	projectType := firstNonEmpty(options.ProjectType, "mod")
	// Modrinth has a dedicated "plugin" project type. Plugins are also tagged
	// with loader categories like paper, purpur, folia, spigot, bukkit. When
	// searching for plugins, filter by the server's platform as a loader
	// category (defaulting to the server type when no loader is selected).
	facetGroups := [][]string{{"project_type:" + projectType}, {"versions:" + version}}
	if projectType == "plugin" {
		loader := options.Loader
		if loader == "" {
			loader = server.Type
		}
		if loader != "" {
			facetGroups = append(facetGroups, []string{"categories:" + loader})
		}
	} else if options.Loader != "" {
		facetGroups = append(facetGroups, []string{"categories:" + options.Loader})
	}
	if options.Category != "" {
		facetGroups = append(facetGroups, []string{"categories:" + options.Category})
	}
	if projectType == "mod" || projectType == "plugin" {
		switch options.Side {
		case "server":
			facetGroups = append(facetGroups, []string{"server_side:required", "server_side:optional"})
		case "client":
			facetGroups = append(facetGroups, []string{"client_side:required", "client_side:optional"})
		case "both":
			facetGroups = append(facetGroups, []string{"server_side:required", "server_side:optional"}, []string{"client_side:required", "client_side:optional"})
		}
	}
	facets, _ := json.Marshal(facetGroups)
	requestURL := "https://api.modrinth.com/v2/search"
	parsed, _ := url.Parse(requestURL)
	values := parsed.Query()
	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery != "" && trimmedQuery != "." {
		values.Set("query", trimmedQuery)
	}
	values.Set("limit", strconv.Itoa(requestLimit))
	values.Set("offset", strconv.Itoa(options.Offset))
	values.Set("facets", string(facets))
	if index := modrinthSortIndex[options.Sort]; index != "" {
		values.Set("index", index)
	} else if trimmedQuery == "" || trimmedQuery == "." {
		values.Set("index", "downloads")
	} else {
		values.Set("index", "relevance")
	}
	parsed.RawQuery = values.Encode()
	var data struct {
		Hits []map[string]any `json:"hits"`
	}
	if err := fetchJSON(r, parsed.String(), &data); err != nil {
		return nil, 0, err
	}
	return data.Hits, options.Offset + requestLimit, nil
}

func (h apiHandler) compatibleModrinthVersions(r *http.Request, projectID string, server store.Server, options modSearchOptions) ([]modrinthVersion, error) {
	version := firstNonEmpty(options.Version, server.MinecraftVersion)
	loader := options.Loader
	if options.ProjectType == "modpack" {
		loader = ""
	} else if loader == "" && (serverTypeNeedsLoader(server.Type) || serverTypeNeedsPlugins(server.Type)) {
		loader = server.Type
	}
	parsed, _ := url.Parse("https://api.modrinth.com/v2/project/" + url.PathEscape(projectID) + "/version")
	values := parsed.Query()
	values.Set("game_versions", jsonArrayParam(version))
	if loader != "" {
		values.Set("loaders", jsonArrayParam(loader))
	}
	parsed.RawQuery = values.Encode()
	var versions []modrinthVersion
	err := fetchJSON(r, parsed.String(), &versions)
	return versions, err
}

func (h apiHandler) modrinthProjectDetails(r *http.Request, projectID string, server store.Server, options modSearchOptions) (map[string]any, error) {
	project, err := h.getModrinthProject(r, projectID)
	if err != nil {
		return nil, err
	}
	versions, err := h.compatibleModrinthVersions(r, projectID, server, options)
	if err != nil {
		return nil, err
	}
	return map[string]any{"project": project, "versions": versions}, nil
}

func (h apiHandler) getModrinthProject(r *http.Request, projectID string) (modrinthProject, error) {
	var project modrinthProject
	err := fetchJSON(r, "https://api.modrinth.com/v2/project/"+url.PathEscape(projectID), &project)
	return project, err
}

func (h apiHandler) getModrinthVersion(r *http.Request, versionID string) (modrinthVersion, error) {
	var version modrinthVersion
	err := fetchJSON(r, "https://api.modrinth.com/v2/version/"+url.PathEscape(versionID), &version)
	return version, err
}

func (h apiHandler) resolveModrinthInstallTarget(r *http.Request, projectID string, server store.Server, versionID string) (modrinthProject, modrinthVersion, string, string, error) {
	return h.resolveModrinthInstallTargetWith(r, projectID, server, versionID, modSearchOptions{})
}

func (h apiHandler) resolveModrinthInstallTargetWith(r *http.Request, projectID string, server store.Server, versionID string, options modSearchOptions) (modrinthProject, modrinthVersion, string, string, error) {
	project, err := h.getModrinthProject(r, projectID)
	if err != nil {
		return modrinthProject{}, modrinthVersion{}, "", "", err
	}
	var version modrinthVersion
	if versionID != "" {
		version, err = h.getModrinthVersion(r, versionID)
		if err != nil {
			return modrinthProject{}, modrinthVersion{}, "", "", err
		}
		if !modrinthVersionCompatible(version, server) {
			return modrinthProject{}, modrinthVersion{}, "", "", errors.New("Selected Modrinth version is not compatible with this server")
		}
	} else {
		versions, err := h.compatibleModrinthVersions(r, projectID, server, options)
		if err != nil {
			return modrinthProject{}, modrinthVersion{}, "", "", err
		}
		if len(versions) == 0 {
			return modrinthProject{}, modrinthVersion{}, "", "", errors.New("No compatible Modrinth file found")
		}
		version = versions[0]
	}
	for _, file := range version.Files {
		if file.Primary {
			return project, version, file.URL, file.Filename, nil
		}
	}
	if len(version.Files) > 0 {
		return project, version, version.Files[0].URL, version.Files[0].Filename, nil
	}
	return modrinthProject{}, modrinthVersion{}, "", "", errors.New("No compatible Modrinth file found")
}

func modrinthVersionCompatible(version modrinthVersion, server store.Server) bool {
	needsLoader := serverTypeNeedsLoader(server.Type) || serverTypeNeedsPlugins(server.Type)
	return stringSliceContains(version.GameVersions, server.MinecraftVersion) && (!needsLoader || stringSliceContains(version.Loaders, server.Type))
}

func (h apiHandler) modrinthInstallPlan(r *http.Request, projectID string, server store.Server, versionID string) (map[string]any, error) {
	project, version, _, _, err := h.resolveModrinthInstallTarget(r, projectID, server, versionID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"project": map[string]any{
			"projectId":     project.ID,
			"title":         project.Title,
			"slug":          project.Slug,
			"versionId":     version.ID,
			"versionNumber": version.VersionNumber,
		},
		"dependencies": h.requiredModrinthDependencies(r, version),
	}, nil
}

func (h apiHandler) requiredModrinthDependencies(r *http.Request, version modrinthVersion) []modDependencyWarning {
	dependencies := []modDependencyWarning{}
	seen := map[string]bool{}
	for _, dependency := range version.Dependencies {
		if dependency.DependencyType != "required" {
			continue
		}
		projectID := dependency.ProjectID
		versionNumber := ""
		if projectID == "" && dependency.VersionID != "" {
			dependencyVersion, err := h.getModrinthVersion(r, dependency.VersionID)
			if err != nil {
				continue
			}
			projectID = dependencyVersion.ProjectID
			versionNumber = dependencyVersion.VersionNumber
		}
		if projectID == "" || seen[projectID] {
			continue
		}
		seen[projectID] = true
		project, err := h.getModrinthProject(r, projectID)
		if err != nil {
			continue
		}
		dependencies = append(dependencies, modDependencyWarning{ProjectID: project.ID, VersionID: dependency.VersionID, Title: project.Title, Slug: project.Slug, Summary: project.Description, IconURL: project.IconURL, VersionNumber: versionNumber})
	}
	return dependencies
}

func (h apiHandler) modrinthDependencyDetails(r *http.Request, dependencies []modDependencyWarning) ([]modDependencyWarning, error) {
	details := make([]modDependencyWarning, 0, len(dependencies))
	for _, dependency := range dependencies {
		project, err := h.getModrinthProject(r, dependency.ProjectID)
		if err != nil {
			return nil, err
		}
		if dependency.Title == "" {
			dependency.Title = project.Title
		}
		if dependency.Slug == "" {
			dependency.Slug = project.Slug
		}
		if dependency.Summary == "" {
			dependency.Summary = project.Description
		}
		if dependency.IconURL == "" {
			dependency.IconURL = project.IconURL
		}
		details = append(details, dependency)
	}
	return details, nil
}

func (h apiHandler) installModrinth(r *http.Request, projectID string, server store.Server, versionID string, includeDependencies bool, warnings []modDependencyWarning) ([]string, error) {
	return h.installModrinthProject(r, projectID, server, versionID, map[string]bool{}, includeDependencies, warnings)
}

func (h apiHandler) installModrinthModpack(r *http.Request, projectID string, server store.Server, versionID string) ([]string, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, errors.New("Modrinth project id is required")
	}
	options := modSearchOptions{Version: server.MinecraftVersion, ProjectType: "modpack"}
	project, version, downloadURL, _, err := h.resolveModrinthInstallTargetWith(r, projectID, server, versionID, options)
	if err != nil {
		return nil, err
	}
	response, err := fetchResponse(r, downloadURL)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, errors.New("Modpack download failed: " + strconv.Itoa(response.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxArtifactDownloadBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxArtifactDownloadBytes {
		return nil, errors.New("Modpack download is too large")
	}
	return h.applyModrinthModpack(r, server, data, project, version)
}

type modrinthPackIndex struct {
	Files []struct {
		Path      string   `json:"path"`
		Downloads []string `json:"downloads"`
	} `json:"files"`
}

func (h apiHandler) applyModrinthModpack(r *http.Request, server store.Server, data []byte, project modrinthProject, version modrinthVersion) ([]string, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("Modpack file could not be read")
	}
	var index modrinthPackIndex
	indexFound := false
	for _, entry := range reader.File {
		if entry.Name != "modrinth.index.json" {
			continue
		}
		indexFound = true
		file, err := entry.Open()
		if err != nil {
			return nil, err
		}
		decodeErr := json.NewDecoder(file).Decode(&index)
		closeErr := file.Close()
		if decodeErr != nil {
			return nil, errors.New("Modpack index could not be read")
		}
		if closeErr != nil {
			return nil, closeErr
		}
		break
	}
	if !indexFound {
		return nil, errors.New("Modpack does not contain a Modrinth index")
	}
	installed := []string{}
	metadata := modMetadata{
		Source:        "modrinth-modpack",
		ProjectID:     project.ID,
		Slug:          project.Slug,
		Title:         project.Title,
		Summary:       project.Description,
		Description:   project.Body,
		IconURL:       project.IconURL,
		PageURL:       "https://modrinth.com/modpack/" + firstNonEmpty(project.Slug, project.ID),
		VersionID:     version.ID,
		VersionName:   version.Name,
		VersionNumber: version.VersionNumber,
		InstalledAt:   time.Now().UTC().Format(time.RFC3339),
	}
	for _, entry := range reader.File {
		name := filepath.ToSlash(entry.Name)
		prefix := ""
		if strings.HasPrefix(name, "server-overrides/") {
			prefix = "server-overrides/"
		} else if strings.HasPrefix(name, "overrides/") {
			prefix = "overrides/"
		}
		if prefix == "" || entry.FileInfo().IsDir() {
			continue
		}
		rel := strings.TrimPrefix(name, prefix)
		if rel == "" || strings.Contains(rel, "..") {
			continue
		}
		target, err := resolveInside(server.Path, filepath.FromSlash(rel))
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		src, err := entry.Open()
		if err != nil {
			return nil, err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			_ = src.Close()
			return nil, err
		}
		copyErr := copyBoundedDownload(out, src, maxArtifactDownloadBytes)
		closeOutErr := out.Close()
		closeSrcErr := src.Close()
		if copyErr != nil {
			_ = os.Remove(target)
			return nil, copyErr
		}
		if closeOutErr != nil {
			return nil, closeOutErr
		}
		if closeSrcErr != nil {
			return nil, closeSrcErr
		}
		if strings.HasPrefix(filepath.ToSlash(rel), "mods/") && strings.HasSuffix(strings.ToLower(rel), ".jar") {
			if err := saveModMetadata(server, filepath.Base(rel), metadata); err != nil {
				return nil, err
			}
		}
		installed = append(installed, rel)
	}
	for _, file := range index.Files {
		if strings.TrimSpace(file.Path) == "" || len(file.Downloads) == 0 {
			continue
		}
		rel := filepath.FromSlash(file.Path)
		if strings.Contains(filepath.ToSlash(rel), "..") {
			continue
		}
		target, err := resolveInside(server.Path, rel)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		if err := downloadFile(r, file.Downloads[0], target); err != nil {
			return nil, err
		}
		if strings.HasPrefix(filepath.ToSlash(file.Path), "mods/") && strings.HasSuffix(strings.ToLower(file.Path), ".jar") {
			if err := saveModMetadata(server, filepath.Base(file.Path), metadata); err != nil {
				return nil, err
			}
		}
		installed = append(installed, filepath.ToSlash(file.Path))
	}
	return installed, nil
}

func (h apiHandler) installModrinthDependencies(r *http.Request, dependencies []modDependencyWarning, server store.Server) ([]string, error) {
	installed := []string{}
	seen := map[string]bool{}
	for _, dependency := range dependencies {
		files, err := h.installModrinthProject(r, dependency.ProjectID, server, dependency.VersionID, seen, true, nil)
		if err != nil {
			return installed, err
		}
		installed = append(installed, files...)
	}
	return installed, nil
}

func (h apiHandler) installModrinthProject(r *http.Request, projectID string, server store.Server, versionID string, seen map[string]bool, includeDependencies bool, warnings []modDependencyWarning) ([]string, error) {
	if seen[projectID] {
		return []string{}, nil
	}
	seen[projectID] = true
	project, version, downloadURL, fileName, err := h.resolveModrinthInstallTarget(r, projectID, server, versionID)
	if err != nil {
		return nil, err
	}
	installed := []string{}
	if includeDependencies {
		for _, dependency := range version.Dependencies {
			if dependency.DependencyType != "required" {
				continue
			}
			dependencyProjectID := dependency.ProjectID
			dependencyVersionID := ""
			if dependencyProjectID == "" && dependency.VersionID != "" {
				dependencyVersion, err := h.getModrinthVersion(r, dependency.VersionID)
				if err != nil {
					return nil, err
				}
				dependencyProjectID = dependencyVersion.ProjectID
				dependencyVersionID = dependency.VersionID
			}
			if dependencyProjectID == "" {
				continue
			}
			files, err := h.installModrinthProject(r, dependencyProjectID, server, dependencyVersionID, seen, true, nil)
			if err != nil {
				return nil, err
			}
			installed = append(installed, files...)
		}
	}
	installedFile, err := h.downloadToMods(r, downloadURL, fileName, server)
	if err != nil {
		return nil, err
	}
	metadata := modMetadata{
		Source:             "modrinth",
		ProjectID:          project.ID,
		Slug:               project.Slug,
		Title:              project.Title,
		Summary:            project.Description,
		Description:        project.Body,
		IconURL:            project.IconURL,
		PageURL:            "https://modrinth.com/mod/" + firstNonEmpty(project.Slug, project.ID),
		VersionID:          version.ID,
		VersionName:        version.Name,
		VersionNumber:      version.VersionNumber,
		DependencyWarnings: warnings,
		InstalledAt:        time.Now().UTC().Format(time.RFC3339),
	}
	if err := saveModMetadata(server, installedFile, metadata); err != nil {
		return nil, err
	}
	installed = append(installed, installedFile)
	return installed, nil
}

func (h apiHandler) downloadToMods(r *http.Request, requestURL string, fileName string, server store.Server) (string, error) {
	response, err := fetchResponse(r, requestURL)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", errors.New("Mod download failed: " + strconv.Itoa(response.StatusCode))
	}
	safeName, err := uniqueModFileName(server, fileName)
	if err != nil {
		return "", err
	}
	modsPath := filepath.Join(server.Path, modsActiveDir(server))
	if err := os.MkdirAll(modsPath, 0o755); err != nil {
		return "", err
	}
	targetPath := filepath.Join(modsPath, safeName)
	output, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	copyErr := copyBoundedDownload(output, response.Body, maxArtifactDownloadBytes)
	closeErr := output.Close()
	if copyErr != nil {
		_ = os.Remove(targetPath)
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return safeName, nil
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
