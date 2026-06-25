package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
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

func searchModrinthDatapacks(r *http.Request, server store.Server) ([]map[string]any, error) {
	limit := boundedQueryInt(r, "limit", 20, 1, 100)
	offset := boundedQueryInt(r, "offset", 0, 0, 10000)
	version := firstNonEmpty(strings.TrimSpace(r.URL.Query().Get("version")), server.MinecraftVersion)
	facets, _ := json.Marshal([][]string{
		{"project_type:datapack"},
		{"versions:" + version},
	})
	requestURL, _ := url.Parse("https://api.modrinth.com/v2/search")
	query := requestURL.Query()
	rawQuery := strings.TrimSpace(r.URL.Query().Get("q"))
	if rawQuery != "" && rawQuery != "." {
		query.Set("query", rawQuery)
	}
	query.Set("limit", strconv.Itoa(limit))
	query.Set("offset", strconv.Itoa(offset))
	query.Set("facets", string(facets))
	if index := modrinthSortIndex[strings.TrimSpace(r.URL.Query().Get("sort"))]; index != "" {
		query.Set("index", index)
	} else if rawQuery == "" || rawQuery == "." {
		query.Set("index", "downloads")
	} else {
		query.Set("index", "relevance")
	}
	requestURL.RawQuery = query.Encode()

	var payload struct {
		Hits []map[string]any `json:"hits"`
	}
	if err := fetchJSON(r, requestURL.String(), &payload); err != nil {
		if strings.Contains(err.Error(), "429") {
			return nil, errors.New("Modrinth rate limit reached. Wait a moment before loading more results.")
		}
		return nil, err
	}
	return payload.Hits, nil
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
		if err := h.createAutoSnapshot(r.Context(), server, "before installing Modrinth datapack into "+input.WorldName); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
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

func (h apiHandler) installModrinthDatapack(r *http.Request, server store.Server, worldName string, projectID string, versionID string) ([]string, error) {
	return h.installModrinthDatapackProject(r, server, worldName, projectID, versionID, map[string]bool{})
}

func (h apiHandler) installModrinthDatapackProject(r *http.Request, server store.Server, worldName string, projectID string, versionID string, seen map[string]bool) ([]string, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, errors.New("Modrinth project id is required")
	}
	if seen[projectID] {
		return []string{}, nil
	}
	seen[projectID] = true

	var version modrinthVersion
	if strings.TrimSpace(versionID) != "" {
		selected, err := h.getModrinthVersion(r, versionID)
		if err != nil {
			return nil, err
		}
		if selected.ProjectID != projectID || !stringSliceContains(selected.GameVersions, server.MinecraftVersion) || !stringSliceContains(selected.Loaders, "datapack") {
			return nil, errors.New("Selected Modrinth datapack version is not compatible with this server")
		}
		version = selected
	} else {
		versions, err := h.compatibleModrinthDatapackVersions(r, projectID, server, "")
		if err != nil {
			return nil, err
		}
		if len(versions) == 0 {
			return nil, errors.New("No compatible Modrinth datapack file found")
		}
		version = versions[0]
	}
	fileURL := ""
	fileName := ""
	for _, file := range version.Files {
		if file.Primary && strings.HasSuffix(strings.ToLower(file.Filename), ".zip") {
			fileURL = file.URL
			fileName = file.Filename
			break
		}
	}
	if fileURL == "" {
		for _, file := range version.Files {
			if strings.HasSuffix(strings.ToLower(file.Filename), ".zip") {
				fileURL = file.URL
				fileName = file.Filename
				break
			}
		}
	}
	if fileURL == "" {
		for _, file := range version.Files {
			if file.Primary {
				fileURL = file.URL
				fileName = file.Filename
				break
			}
		}
	}
	if fileURL == "" && len(version.Files) > 0 {
		fileURL = version.Files[0].URL
		fileName = version.Files[0].Filename
	}
	if fileURL == "" || fileName == "" {
		return nil, errors.New("No downloadable Modrinth datapack file found for the selected version")
	}

	installed := []string{}
	for _, dependency := range version.Dependencies {
		if dependency.DependencyType != "required" {
			continue
		}
		dependencyProjectID := dependency.ProjectID
		if dependencyProjectID == "" && dependency.VersionID != "" {
			dependencyVersion, err := h.getModrinthVersion(r, dependency.VersionID)
			if err != nil {
				return nil, err
			}
			dependencyProjectID = dependencyVersion.ProjectID
		}
		if dependencyProjectID == "" {
			continue
		}
		dependencyFiles, err := h.installModrinthDatapackProject(r, server, worldName, dependencyProjectID, dependency.VersionID, seen)
		if err != nil {
			return nil, err
		}
		installed = append(installed, dependencyFiles...)
	}

	safeName, err := uniqueDatapackFileName(server, worldName, fileName)
	if err != nil {
		return nil, err
	}
	safeWorld, err := safePathSegment(worldName, "World name")
	if err != nil {
		return nil, err
	}
	targetDir, err := resolveInside(server.Path, filepath.Join(safeWorld, "datapacks"))
	if err != nil {
		return nil, err
	}
	target, err := resolveInside(targetDir, safeName)
	if err != nil {
		return nil, err
	}
	if err := downloadFile(r, fileURL, target); err != nil {
		return nil, err
	}
	project, _ := h.getModrinthProject(r, projectID)
	if project.ID != "" {
		metadata := modMetadata{
			Source:        "modrinth",
			ProjectID:     project.ID,
			Slug:          project.Slug,
			Title:         project.Title,
			Summary:       project.Description,
			IconURL:       project.IconURL,
			PageURL:       "https://modrinth.com/datapack/" + firstNonEmpty(project.Slug, project.ID),
			VersionID:     version.ID,
			VersionName:   version.Name,
			VersionNumber: version.VersionNumber,
			InstalledAt:   time.Now().UTC().Format(time.RFC3339),
		}
		_ = saveDatapackMetadata(server, worldName, safeName, metadata)
	}
	installed = append(installed, safeName)
	return installed, nil
}

func (h apiHandler) modrinthDatapackProjectDetails(r *http.Request, projectID string, server store.Server, requestedVersion string) (map[string]any, error) {
	project, err := h.getModrinthProject(r, projectID)
	if err != nil {
		return nil, err
	}
	versions, err := h.compatibleModrinthDatapackVersions(r, projectID, server, requestedVersion)
	if err != nil {
		return nil, err
	}
	return map[string]any{"project": project, "versions": versions}, nil
}

func (h apiHandler) compatibleModrinthDatapackVersions(r *http.Request, projectID string, server store.Server, requestedVersion string) ([]modrinthVersion, error) {
	version := firstNonEmpty(strings.TrimSpace(requestedVersion), server.MinecraftVersion)
	parsed, _ := url.Parse("https://api.modrinth.com/v2/project/" + url.PathEscape(projectID) + "/version")
	values := parsed.Query()
	values.Set("game_versions", jsonArrayParam(version))
	values.Set("loaders", jsonArrayParam("datapack"))
	parsed.RawQuery = values.Encode()
	var versions []modrinthVersion
	err := fetchJSON(r, parsed.String(), &versions)
	return versions, err
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

func datapackMetadataPath(server store.Server) string {
	return filepath.Join(server.Path, ".dashboard-datapacks.json")
}

func readDatapackMetadata(server store.Server) map[string]*modMetadata {
	metadata := map[string]*modMetadata{}
	data, err := os.ReadFile(datapackMetadataPath(server))
	if err != nil {
		return metadata
	}
	_ = json.Unmarshal(data, &metadata)
	if metadata == nil {
		metadata = map[string]*modMetadata{}
	}
	return metadata
}

func writeDatapackMetadata(server store.Server, metadata map[string]*modMetadata) error {
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(datapackMetadataPath(server), append(data, '\n'), 0o644)
}

func saveDatapackMetadata(server store.Server, worldName string, fileName string, metadata modMetadata) error {
	index := readDatapackMetadata(server)
	index[worldName+"/"+filepath.Base(fileName)] = &metadata
	return writeDatapackMetadata(server, index)
}

func listDatapacks(path string, server store.Server, worldName string) []datapackInfo {
	entries, err := os.ReadDir(path)
	if err != nil {
		return []datapackInfo{}
	}
	metadataIndex := readDatapackMetadata(server)
	datapacks := []datapackInfo{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		lower := strings.ToLower(entry.Name())
		if !strings.HasSuffix(lower, ".zip") && !strings.HasSuffix(lower, ".zip.disabled") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		datapacks = append(datapacks, datapackInfo{
			Name:      entry.Name(),
			Size:      info.Size(),
			UpdatedAt: info.ModTime().UTC().Format(time.RFC3339),
			Enabled:   !strings.HasSuffix(lower, ".disabled"),
			Metadata:  metadataIndex[worldName+"/"+entry.Name()],
		})
	}
	sort.Slice(datapacks, func(left int, right int) bool {
		return datapacks[left].Name < datapacks[right].Name
	})
	return datapacks
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

func worldArchiveTarget(server store.Server, worldName string) (string, string, string, error) {
	safeWorld, err := safePathSegment(worldName, "World name")
	if err != nil {
		return "", "", "", err
	}
	worldPath, err := resolveInside(server.Path, safeWorld)
	if err != nil {
		return "", "", "", err
	}
	if info, err := os.Stat(worldPath); err != nil || !info.IsDir() {
		return "", "", "", errors.New("World folder not found")
	}
	if !fileExists(filepath.Join(worldPath, "level.dat")) {
		return "", "", "", errors.New("Only Minecraft world folders can be downloaded here")
	}
	return worldPath, safeArchiveName(server.Name) + "-" + safeArchiveName(safeWorld) + ".zip", safeWorld, nil
}

func importWorldZipFromPart(server store.Server, part *multipart.Part, worldName string) error {
	tempFile, err := os.CreateTemp("", "cliff-world-upload-*.zip")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	if _, err := io.Copy(tempFile, part); err != nil {
		_ = tempFile.Close()
		return err
	}
	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
		_ = tempFile.Close()
		return errors.New("World archive is not a valid zip file")
	}
	defer tempFile.Close()
	return importWorldZip(server, tempFile, part.FileName(), worldName)
}

func importWorldZip(server store.Server, file multipart.File, archiveName string, worldName string) error {
	safeArchive, err := safePathSegment(archiveName, "Archive name")
	if err != nil {
		return err
	}
	if !strings.HasSuffix(strings.ToLower(safeArchive), ".zip") {
		return errors.New("World imports must be .zip files")
	}
	reader, err := zipReaderFromMultipart(file, "World archive is not a valid zip file")
	if err != nil {
		return err
	}
	targetName := strings.TrimSpace(worldName)
	if targetName == "" {
		targetName = strings.TrimSuffix(safeArchive, filepath.Ext(safeArchive))
	}
	safeWorld, target, err := ensureAvailableWorldTarget(server, targetName)
	if err != nil {
		return err
	}

	files := []string{}
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		normalized, err := normalizedZipName(entry.Name)
		if err != nil {
			return err
		}
		files = append(files, normalized)
	}
	if len(files) == 0 {
		return errors.New("World archive is empty")
	}
	prefix := ""
	if !containsString(files, "level.dat") {
		for _, name := range files {
			if strings.Count(name, "/") == 1 && strings.HasSuffix(name, "/level.dat") {
				prefix = strings.TrimSuffix(name, "level.dat")
				break
			}
		}
		if prefix == "" {
			return errors.New("World archive must contain level.dat at the root or inside one top-level folder")
		}
	}

	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	for _, entry := range reader.File {
		normalized, err := normalizedZipName(entry.Name)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(normalized, prefix) {
			continue
		}
		relative := strings.TrimPrefix(normalized, prefix)
		if relative == "" {
			continue
		}
		destination, err := resolveInside(target, filepath.FromSlash(relative))
		if err != nil {
			return err
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(destination, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		source, err := entry.Open()
		if err != nil {
			return err
		}
		if err := writeZipEntry(source, destination); err != nil {
			_ = source.Close()
			return err
		}
		_ = source.Close()
	}
	_ = safeWorld
	return nil
}

func writeDatapack(server store.Server, worldName string, fileName string, file io.Reader) error {
	safeWorld, err := safePathSegment(worldName, "World name")
	if err != nil {
		return err
	}
	safeName, err := safePathSegment(fileName, "Datapack name")
	if err != nil {
		return err
	}
	if !strings.HasSuffix(strings.ToLower(safeName), ".zip") {
		return errors.New("Datapacks must be .zip files")
	}
	targetDir, err := resolveInside(server.Path, filepath.Join(safeWorld, "datapacks"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}
	target, err := resolveInside(targetDir, safeName)
	if err != nil {
		return err
	}
	return writeUploadedFile(file, target)
}

func uniqueDatapackFileName(server store.Server, worldName string, fileName string) (string, error) {
	safeWorld, err := safePathSegment(worldName, "World name")
	if err != nil {
		return "", err
	}
	safeName := safeBaseName(fileName)
	if !strings.HasSuffix(strings.ToLower(safeName), ".zip") {
		return "", errors.New("Only .zip datapack files are supported")
	}
	extension := filepath.Ext(safeName)
	base := strings.TrimSuffix(safeName, extension)
	candidate := safeName
	index := 2
	for {
		target, err := resolveInside(server.Path, filepath.Join(safeWorld, "datapacks", candidate))
		if err != nil {
			return "", err
		}
		if !fileExists(target) {
			return candidate, nil
		}
		candidate = base + "-" + strconv.Itoa(index) + extension
		index++
	}
}

func datapackDownloadTarget(server store.Server, worldName string, fileName string) (string, string, error) {
	target, safeName, err := datapackPath(server, worldName, fileName)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() {
		return "", "", errors.New("Datapack not found")
	}
	return target, removeDisabledSuffix(safeName), nil
}

func deleteDatapack(server store.Server, worldName string, fileName string) error {
	target, _, err := datapackPath(server, worldName, fileName)
	if err != nil {
		return err
	}
	if _, err := os.Stat(target); err != nil {
		return errors.New("Datapack not found")
	}
	return os.Remove(target)
}

func deleteSelectedDatapacks(server store.Server, worldName string, fileNames []string) error {
	for _, fileName := range fileNames {
		target, _, err := datapackPath(server, worldName, fileName)
		if err != nil {
			return err
		}
		if fileExists(target) {
			if err := os.Remove(target); err != nil {
				return err
			}
		}
	}
	return nil
}

func setDatapackEnabled(server store.Server, worldName string, fileName string, enabled bool) error {
	source, safeName, err := datapackPath(server, worldName, fileName)
	if err != nil {
		return err
	}
	if !fileExists(source) {
		return errors.New("Datapack not found")
	}
	lowerName := strings.ToLower(safeName)
	nextName := safeName
	if enabled {
		nextName = removeDisabledSuffix(safeName)
	} else if !strings.HasSuffix(lowerName, ".disabled") {
		nextName = safeName + ".disabled"
	}
	if nextName == safeName {
		return nil
	}
	datapackDir := filepath.Dir(source)
	destination, err := resolveInside(datapackDir, nextName)
	if err != nil {
		return err
	}
	if fileExists(destination) {
		return errors.New("A datapack with the target enabled state already exists")
	}
	return os.Rename(source, destination)
}

func setSelectedDatapacksEnabled(server store.Server, worldName string, fileNames []string, enabled bool) error {
	for _, fileName := range fileNames {
		target, safeName, err := datapackPath(server, worldName, fileName)
		if err != nil {
			return err
		}
		lowerName := strings.ToLower(safeName)
		if enabled && !strings.HasSuffix(lowerName, ".disabled") {
			continue
		}
		if !enabled && strings.HasSuffix(lowerName, ".disabled") {
			continue
		}
		if !fileExists(target) {
			continue
		}
		if err := setDatapackEnabled(server, worldName, safeName, enabled); err != nil {
			return err
		}
	}
	return nil
}

func datapackPath(server store.Server, worldName string, fileName string) (string, string, error) {
	safeWorld, err := safePathSegment(worldName, "World name")
	if err != nil {
		return "", "", err
	}
	safeName, err := safePathSegment(fileName, "Datapack name")
	if err != nil {
		return "", "", err
	}
	lowerName := strings.ToLower(safeName)
	if !strings.HasSuffix(lowerName, ".zip") && !strings.HasSuffix(lowerName, ".zip.disabled") {
		return "", "", errors.New("Only datapack zip files are supported")
	}
	datapackDir, err := resolveInside(server.Path, filepath.Join(safeWorld, "datapacks"))
	if err != nil {
		return "", "", err
	}
	target, err := resolveInside(datapackDir, safeName)
	return target, safeName, err
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

func safePathSegment(value string, label string) (string, error) {
	segment := strings.TrimSpace(value)
	if segment == "" || segment == "." || segment == ".." || filepath.Base(segment) != segment {
		return "", errors.New(label + " is invalid")
	}
	return segment, nil
}

func normalizedZipName(entryName string) (string, error) {
	normalized := strings.TrimLeft(strings.ReplaceAll(entryName, "\\", "/"), "/")
	if normalized == "" || strings.Contains(normalized, "\x00") || strings.HasPrefix(normalized, "/") {
		return "", errors.New("World archive contains an invalid path")
	}
	for _, part := range strings.Split(normalized, "/") {
		if part == ".." {
			return "", errors.New("World archive contains an unsafe path")
		}
	}
	return normalized, nil
}

func writeZipEntry(source io.Reader, target string) error {
	output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer output.Close()
	_, err = io.Copy(output, source)
	return err
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
