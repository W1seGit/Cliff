package httpserver

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

type serverCreateInput struct {
	Mode             string `json:"mode"`
	SourceServerID   string `json:"sourceServerId"`
	Token            string `json:"token"`
	Path             string `json:"path"`
	Name             string `json:"name"`
	Type             string `json:"type"`
	MinecraftVersion string `json:"minecraftVersion"`
	LoaderVersion    string `json:"loaderVersion"`
	JavaPath         string `json:"javaPath"`
	MinMemoryMB      int    `json:"minMemoryMb"`
	MaxMemoryMB      int    `json:"maxMemoryMb"`
	Port             int    `json:"port"`
	LaunchJar        string `json:"launchJar"`
	ExtraArgs        string `json:"extraArgs"`
}

type importDetection struct {
	Token             string `json:"token,omitempty"`
	Name              string `json:"name"`
	Path              string `json:"path"`
	Type              string `json:"type"`
	MinecraftVersion  string `json:"minecraftVersion"`
	LoaderVersion     string `json:"loaderVersion"`
	Port              int    `json:"port"`
	ActiveWorld       string `json:"activeWorld"`
	LaunchJar         string `json:"launchJar"`
	AlreadyRegistered bool   `json:"alreadyRegistered"`
	Mods              int    `json:"mods"`
	DisabledMods      int    `json:"disabledMods"`
}

type versionDetails struct {
	Downloads struct {
		Server *struct {
			URL  string `json:"url"`
			SHA1 string `json:"sha1"`
			Size int64  `json:"size"`
		} `json:"server"`
	} `json:"downloads"`
}

type fabricInstaller struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
}

type paperBuild struct {
	ID        int    `json:"id"`
	Channel   string `json:"channel"`
	Downloads map[string]struct {
		URL string `json:"url"`
	} `json:"downloads"`
}

type purpurVersionInfo struct {
	Version string `json:"version"`
	Builds  struct {
		Latest string   `json:"latest"`
		All    []string `json:"all"`
	} `json:"builds"`
}

var serverSlugPattern = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)
var importTokenPattern = regexp.MustCompile(`^imp_[a-f0-9]{16}$`)

const maxImportMultipartMemoryBytes int64 = 8 << 20

func (h apiHandler) createServer(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
		h.uploadServerImport(w, r)
		return
	}
	var input serverCreateInput
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid server body")
		return
	}
	server, note, err := h.createServerFromInput(r, input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server": server, "note": note})
}

func (h apiHandler) createServerFromInput(r *http.Request, input serverCreateInput) (store.Server, string, error) {
	mode := strings.TrimSpace(input.Mode)
	if mode == "" {
		mode = "create"
	}
	switch mode {
	case "clone":
		return h.cloneServer(r, input)
	case "import":
		return h.importServerPath(r, input)
	case "import-staged":
		return h.importStagedServer(r, input)
	case "create":
		return h.createManagedServer(r, input)
	default:
		return store.Server{}, "", errors.New("Unsupported server action")
	}
}

func (h apiHandler) uploadServerImport(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxImportMultipartMemoryBytes); err != nil {
		writeError(w, http.StatusBadRequest, "Server upload could not be read")
		return
	}
	mode := strings.TrimSpace(r.FormValue("mode"))
	if mode != "detect-zip" && mode != "detect-folder" && mode != "import-zip" && mode != "import-folder" {
		writeError(w, http.StatusBadRequest, "Unsupported server upload action")
		return
	}
	detectMode := mode == "detect-zip" || mode == "detect-folder"
	zipMode := mode == "detect-zip" || mode == "import-zip"
	fallbackName := "Imported server"
	if zipMode {
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "Server ZIP is required")
			return
		}
		_ = file.Close()
		fallbackName = fileDisplayName(header.Filename)
	} else {
		paths, _ := formPaths(r.MultipartForm)
		if len(paths) > 0 {
			parts, err := relativeUploadParts(paths[0])
			if err == nil && len(parts) > 0 {
				fallbackName = parts[0]
			}
		}
	}
	name := displayName(r.FormValue("name"), fallbackName)
	serverPath := ""
	if detectMode {
		token, err := newImportToken()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Import session could not be created")
			return
		}
		serverPath = h.importSessionPath(token)
		defer r.MultipartForm.RemoveAll()
		if err := h.writeImportUpload(r, zipMode, serverPath); err != nil {
			_ = os.RemoveAll(serverPath)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		detection, err := h.inspectServerFolder(r, serverPath, name, token)
		if err != nil {
			_ = os.RemoveAll(serverPath)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"detection": detection})
		return
	}

	settings, err := h.store.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	serverPath, err = availableManagedPath(r, h, settings.ServerRoot, name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer r.MultipartForm.RemoveAll()
	if err := h.writeImportUpload(r, zipMode, serverPath); err != nil {
		_ = os.RemoveAll(serverPath)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	input := serverCreateInput{
		Name:             name,
		Type:             r.FormValue("type"),
		MinecraftVersion: r.FormValue("minecraftVersion"),
		LoaderVersion:    r.FormValue("loaderVersion"),
		JavaPath:         r.FormValue("javaPath"),
		MinMemoryMB:      atoiDefault(r.FormValue("minMemoryMb"), 0),
		MaxMemoryMB:      atoiDefault(r.FormValue("maxMemoryMb"), 0),
		Port:             atoiDefault(r.FormValue("port"), 0),
		LaunchJar:        r.FormValue("launchJar"),
		ExtraArgs:        r.FormValue("extraArgs"),
	}
	server, err := h.serverRecordFromInput(r, input, serverPath, name)
	if err != nil {
		_ = os.RemoveAll(serverPath)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if server.LaunchJar == "" {
		server.LaunchJar = detectLaunchJar(serverPath, server.Type)
	}
	created, err := h.store.CreateServer(r.Context(), server)
	if err != nil {
		_ = os.RemoveAll(serverPath)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server": created})
}

func (h apiHandler) createManagedServer(r *http.Request, input serverCreateInput) (store.Server, string, error) {
	settings, err := h.store.Settings(r.Context())
	if err != nil {
		return store.Server{}, "", err
	}
	name := displayName(input.Name, "New server")
	target, err := availableManagedPath(r, h, settings.ServerRoot, name)
	if err != nil {
		return store.Server{}, "", err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return store.Server{}, "", err
	}
	server, err := h.serverRecordFromInput(r, input, target, name)
	if err != nil {
		_ = os.RemoveAll(target)
		return store.Server{}, "", err
	}
	note, err := h.provisionServer(r, &server)
	if err != nil {
		_ = os.RemoveAll(target)
		return store.Server{}, "", err
	}
	created, err := h.store.CreateServer(r.Context(), server)
	if err != nil {
		_ = os.RemoveAll(target)
		return store.Server{}, "", err
	}
	return created, note, nil
}

func (h apiHandler) cloneServer(r *http.Request, input serverCreateInput) (store.Server, string, error) {
	source, ok, err := h.store.GetServer(r.Context(), input.SourceServerID)
	if err != nil {
		return store.Server{}, "", err
	}
	if !ok {
		return store.Server{}, "", errors.New("Source server not found")
	}
	if h.process.IsRunning(source.ID) {
		return store.Server{}, "", errors.New("Stop this server before cloning it")
	}
	if info, err := os.Stat(source.Path); err != nil || !info.IsDir() {
		return store.Server{}, "", errors.New("Source server folder not found")
	}
	settings, err := h.store.Settings(r.Context())
	if err != nil {
		return store.Server{}, "", err
	}
	name := displayName(input.Name, source.Name+" Copy")
	target, err := availableManagedPath(r, h, settings.ServerRoot, name)
	if err != nil {
		return store.Server{}, "", err
	}
	if err := copyDirectory(source.Path, target); err != nil {
		_ = os.RemoveAll(target)
		return store.Server{}, "", err
	}
	port, err := portValue(input.Port, source.Port+1)
	if err != nil {
		_ = os.RemoveAll(target)
		return store.Server{}, "", err
	}
	server := source
	server.ID = ""
	server.Name = name
	server.Path = target
	server.Port = port
	server.CreatedAt = ""
	server.UpdatedAt = ""
	created, err := h.store.CreateServer(r.Context(), server)
	if err != nil {
		_ = os.RemoveAll(target)
		return store.Server{}, "", err
	}
	properties := readServerPropertiesPayload(created)
	if err := writeServerPropertiesFile(created, map[string]any{
		"motd":               properties.Editable.MOTD,
		"levelName":          properties.Editable.LevelName,
		"levelSeed":          properties.Editable.LevelSeed,
		"gamemode":           properties.Editable.Gamemode,
		"difficulty":         properties.Editable.Difficulty,
		"maxPlayers":         properties.Editable.MaxPlayers,
		"serverPort":         port,
		"viewDistance":       properties.Editable.ViewDistance,
		"simulationDistance": properties.Editable.SimulationDistance,
		"onlineMode":         properties.Editable.OnlineMode,
		"whiteList":          properties.Editable.WhiteList,
		"pvp":                properties.Editable.PVP,
		"enableCommandBlock": properties.Editable.EnableCommandBlock,
		"allowFlight":        properties.Editable.AllowFlight,
	}, nil); err != nil {
		return created, "Server cloned, but server.properties could not be updated", nil
	}
	return created, "Server cloned", nil
}

func (h apiHandler) importServerPath(r *http.Request, input serverCreateInput) (store.Server, string, error) {
	source, err := filepath.Abs(strings.TrimSpace(input.Path))
	if err != nil || source == "" {
		return store.Server{}, "", errors.New("Server path is required")
	}
	if info, err := os.Stat(source); err != nil || !info.IsDir() {
		return store.Server{}, "", errors.New("Server path must be a folder")
	}
	settings, err := h.store.Settings(r.Context())
	if err != nil {
		return store.Server{}, "", err
	}
	name := displayName(input.Name, filepath.Base(source))
	target, err := availableManagedPath(r, h, settings.ServerRoot, name)
	if err != nil {
		return store.Server{}, "", err
	}
	if err := copyDirectory(source, target); err != nil {
		_ = os.RemoveAll(target)
		return store.Server{}, "", err
	}
	server, err := h.serverRecordFromInput(r, input, target, name)
	if err != nil {
		_ = os.RemoveAll(target)
		return store.Server{}, "", err
	}
	if server.LaunchJar == "" {
		server.LaunchJar = detectLaunchJar(target, server.Type)
	}
	created, err := h.store.CreateServer(r.Context(), server)
	if err != nil {
		_ = os.RemoveAll(target)
		return store.Server{}, "", err
	}
	return created, "Server imported", nil
}

func (h apiHandler) importStagedServer(r *http.Request, input serverCreateInput) (store.Server, string, error) {
	token := strings.TrimSpace(input.Token)
	if !importTokenPattern.MatchString(token) {
		return store.Server{}, "", errors.New("Invalid import session")
	}
	stagedPath := h.importSessionPath(token)
	if info, err := os.Stat(stagedPath); err != nil || !info.IsDir() {
		return store.Server{}, "", errors.New("Import session was not found")
	}
	settings, err := h.store.Settings(r.Context())
	if err != nil {
		return store.Server{}, "", err
	}
	detection, err := h.inspectServerFolder(r, stagedPath, displayName(input.Name, "Imported server"), "")
	if err != nil {
		return store.Server{}, "", err
	}
	name := displayName(input.Name, detection.Name)
	target, err := availableManagedPath(r, h, settings.ServerRoot, name)
	if err != nil {
		return store.Server{}, "", err
	}
	if err := copyDirectory(stagedPath, target); err != nil {
		_ = os.RemoveAll(target)
		return store.Server{}, "", err
	}
	server, err := h.serverRecordFromInput(r, input, target, name)
	if err != nil {
		_ = os.RemoveAll(target)
		return store.Server{}, "", err
	}
	if server.LaunchJar == "" {
		server.LaunchJar = detectLaunchJar(target, server.Type)
	}
	created, err := h.store.CreateServer(r.Context(), server)
	if err != nil {
		_ = os.RemoveAll(target)
		return store.Server{}, "", err
	}
	_ = os.RemoveAll(stagedPath)
	return created, "Server imported", nil
}

func (h apiHandler) serverRecordFromInput(r *http.Request, input serverCreateInput, serverPath string, fallbackName string) (store.Server, error) {
	metadata, err := h.getMinecraftMetadata(r, false)
	if err != nil {
		return store.Server{}, err
	}
	serverType := strings.TrimSpace(input.Type)
	if serverType == "" {
		serverType = "fabric"
	}
	if !validServerType(serverType) {
		return store.Server{}, errors.New("Invalid server type")
	}
	minecraftVersion := strings.TrimSpace(input.MinecraftVersion)
	if minecraftVersion == "" {
		minecraftVersion = metadata.Latest.Release
	}
	if !metadataHasMinecraftVersion(metadata, minecraftVersion) {
		return store.Server{}, errors.New("Minecraft " + minecraftVersion + " is not available in current release metadata")
	}
	// Validate that the Minecraft version is supported by the chosen server type
	supportedVersions, expVersions, err := h.getSupportedVersions(r, serverType, false)
	if err == nil && len(supportedVersions) > 0 {
		found := false
		for _, v := range supportedVersions {
			if v == minecraftVersion {
				found = true
				break
			}
		}
		if !found {
			for _, v := range expVersions {
				if v == minecraftVersion {
					found = true
					break
				}
			}
		}
		if !found {
			return store.Server{}, errors.New(serverType + " does not support Minecraft " + minecraftVersion)
		}
	}
	loaderVersion := strings.TrimSpace(input.LoaderVersion)
	if !serverTypeNeedsLoader(serverType) {
		loaderVersion = ""
	} else {
		if loaderVersion == "" {
			return store.Server{}, errors.New("Loader version is required for this server type")
		}
		loaders, err := h.getLoaderVersions(r, serverType, minecraftVersion, false)
		if err != nil {
			return store.Server{}, err
		}
		if !loaderListContains(loaders, loaderVersion) {
			return store.Server{}, errors.New(serverType + " loader " + loaderVersion + " is not available for Minecraft " + minecraftVersion)
		}
	}
	minMemory := input.MinMemoryMB
	if minMemory == 0 {
		minMemory = 2048
	}
	maxMemory := input.MaxMemoryMB
	if maxMemory == 0 {
		maxMemory = 4096
	}
	if minMemory < 512 {
		return store.Server{}, errors.New("Min memory must be at least 512 MB")
	}
	if maxMemory < minMemory {
		return store.Server{}, errors.New("Max memory must be greater than or equal to min memory")
	}
	port, err := portValue(input.Port, 25565)
	if err != nil {
		return store.Server{}, err
	}
	return store.Server{
		Name:             displayName(input.Name, fallbackName),
		Path:             serverPath,
		Type:             serverType,
		MinecraftVersion: minecraftVersion,
		LoaderVersion:    loaderVersion,
		JavaPath:         javaPathValue(input.JavaPath),
		MinMemoryMB:      minMemory,
		MaxMemoryMB:      maxMemory,
		Port:             port,
		LaunchJar:        strings.TrimSpace(input.LaunchJar),
		ExtraArgs:        strings.TrimSpace(input.ExtraArgs),
		SnapshotsEnabled: true,
	}, nil
}

func (h apiHandler) writeImportUpload(r *http.Request, zipMode bool, targetRoot string) error {
	if zipMode {
		file, header, err := r.FormFile("file")
		if err != nil {
			return errors.New("Server ZIP is required")
		}
		defer file.Close()
		return extractServerZip(file, header, targetRoot)
	}
	if r.MultipartForm == nil {
		return errors.New("Server folder is required")
	}
	files := r.MultipartForm.File["files"]
	paths, err := formPaths(r.MultipartForm)
	if err != nil {
		return err
	}
	return writeUploadedFolder(files, paths, targetRoot)
}

func (h apiHandler) inspectServerFolder(r *http.Request, serverPath string, fallbackName string, token string) (importDetection, error) {
	serverType := detectServerTypeFromPath(serverPath)
	minecraftVersion, loaderVersion := detectMinecraftProfileFromPath(serverPath, serverType)
	if minecraftVersion == "" {
		if metadata, err := h.getMinecraftMetadata(r, false); err == nil {
			minecraftVersion = metadata.Latest.Release
		}
	}
	raw := readPropertiesRaw(filepath.Join(serverPath, "server.properties"))
	port, _ := strconv.Atoi(strings.TrimSpace(raw["server-port"]))
	if port == 0 {
		port = 25565
	}
	activeWorld := strings.TrimSpace(raw["level-name"])
	if activeWorld == "" {
		activeWorld = "world"
	}
	mods, disabledMods := countImportedMods(serverPath)
	registered, err := h.pathAlreadyRegistered(r, serverPath)
	if err != nil {
		return importDetection{}, err
	}
	return importDetection{
		Token:             token,
		Name:              displayName(fallbackName, filepath.Base(serverPath)),
		Path:              serverPath,
		Type:              serverType,
		MinecraftVersion:  minecraftVersion,
		LoaderVersion:     loaderVersion,
		Port:              port,
		ActiveWorld:       activeWorld,
		LaunchJar:         detectLaunchJar(serverPath, serverType),
		AlreadyRegistered: registered,
		Mods:              mods,
		DisabledMods:      disabledMods,
	}, nil
}

func (h apiHandler) pathAlreadyRegistered(r *http.Request, serverPath string) (bool, error) {
	servers, err := h.store.ListServers(r.Context())
	if err != nil {
		return false, err
	}
	target, _ := filepath.Abs(serverPath)
	target = filepath.Clean(target)
	for _, server := range servers {
		registered, _ := filepath.Abs(server.Path)
		if filepath.Clean(registered) == target {
			return true, nil
		}
	}
	return false, nil
}

func extractServerZip(file multipart.File, header *multipart.FileHeader, targetRoot string) error {
	if header == nil || !strings.HasSuffix(strings.ToLower(header.Filename), ".zip") {
		return errors.New("Upload a .zip server archive")
	}
	archive, err := zipReaderFromMultipart(file, "Server ZIP could not be read")
	if err != nil {
		return err
	}
	stripPrefix, err := archiveStripPrefix(archive.File)
	if err != nil {
		return err
	}
	filesWritten := 0
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		return err
	}
	for _, entry := range archive.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		parts, err := normalizedArchiveParts(entry.Name)
		if err != nil {
			return err
		}
		if len(parts) == 0 || parts[0] == "__MACOSX" {
			continue
		}
		if stripPrefix != "" && parts[0] == stripPrefix {
			parts = parts[1:]
		}
		if len(parts) == 0 {
			continue
		}
		target := filepath.Join(append([]string{targetRoot}, parts...)...)
		if err := assertInsidePath(targetRoot, target); err != nil {
			return err
		}
		reader, err := entry.Open()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			_ = reader.Close()
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, entry.Mode())
		if err != nil {
			_ = reader.Close()
			return err
		}
		_, copyErr := io.Copy(output, reader)
		closeErr := output.Close()
		readCloseErr := reader.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if readCloseErr != nil {
			return readCloseErr
		}
		filesWritten++
	}
	if filesWritten == 0 {
		return errors.New("Server ZIP does not contain server files")
	}
	return nil
}

func zipReaderFromMultipart(file multipart.File, message string) (*zip.Reader, error) {
	size, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, errors.New(message)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, errors.New(message)
	}
	archive, err := zip.NewReader(file, size)
	if err != nil {
		return nil, errors.New(message)
	}
	return archive, nil
}

func archiveStripPrefix(files []*zip.File) (string, error) {
	roots := map[string]bool{}
	hasRootFile := false
	fileCount := 0
	for _, entry := range files {
		if entry.FileInfo().IsDir() {
			continue
		}
		parts, err := normalizedArchiveParts(entry.Name)
		if err != nil {
			return "", err
		}
		if len(parts) == 0 || parts[0] == "__MACOSX" {
			continue
		}
		fileCount++
		if len(parts) == 1 {
			hasRootFile = true
		}
		roots[parts[0]] = true
	}
	if fileCount == 0 {
		return "", errors.New("Server ZIP is empty")
	}
	if !hasRootFile && len(roots) == 1 {
		for root := range roots {
			return root, nil
		}
	}
	return "", nil
}

func writeUploadedFolder(files []*multipart.FileHeader, relativePaths []string, targetRoot string) error {
	if len(files) == 0 {
		return errors.New("Choose a server folder to import")
	}
	entries := make([]struct {
		header *multipart.FileHeader
		parts  []string
	}, 0, len(files))
	roots := map[string]bool{}
	for index, header := range files {
		relativePath := header.Filename
		if index < len(relativePaths) && relativePaths[index] != "" {
			relativePath = relativePaths[index]
		}
		parts, err := relativeUploadParts(relativePath)
		if err != nil {
			return err
		}
		entries = append(entries, struct {
			header *multipart.FileHeader
			parts  []string
		}{header: header, parts: parts})
		roots[parts[0]] = true
	}
	stripPrefix := ""
	if len(roots) == 1 {
		for root := range roots {
			stripPrefix = root
		}
	}
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		parts := entry.parts
		if stripPrefix != "" && parts[0] == stripPrefix {
			parts = parts[1:]
		}
		if len(parts) == 0 {
			continue
		}
		target := filepath.Join(append([]string{targetRoot}, parts...)...)
		if err := assertInsidePath(targetRoot, target); err != nil {
			return err
		}
		input, err := entry.header.Open()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			_ = input.Close()
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		inputCloseErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if inputCloseErr != nil {
			return inputCloseErr
		}
	}
	return nil
}

func normalizedArchiveParts(value string) ([]string, error) {
	return safeRelativeParts(value, "Archive")
}

func relativeUploadParts(value string) ([]string, error) {
	return safeRelativeParts(value, "Uploaded folder")
}

func safeRelativeParts(value string, label string) ([]string, error) {
	normalized := strings.TrimLeft(strings.ReplaceAll(value, "\\", "/"), "/")
	if normalized == "" || strings.Contains(normalized, "\x00") {
		return nil, errors.New(label + " contains an invalid path")
	}
	parts := []string{}
	for _, part := range strings.Split(normalized, "/") {
		if part == "" {
			continue
		}
		if part == "." || part == ".." {
			return nil, errors.New(label + " contains an unsafe path")
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return nil, errors.New(label + " contains an invalid path")
	}
	return parts, nil
}

func assertInsidePath(rootPath string, targetPath string) error {
	root, err := filepath.Abs(rootPath)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(targetPath)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if relative == "." || (!strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && relative != ".." && !filepath.IsAbs(relative)) {
		return nil
	}
	return errors.New("Archive contains an unsafe path")
}

func formPaths(form *multipart.Form) ([]string, error) {
	if form == nil {
		return []string{}, nil
	}
	values := form.Value["paths"]
	if len(values) == 0 || strings.TrimSpace(values[0]) == "" {
		return []string{}, nil
	}
	paths := []string{}
	if err := json.Unmarshal([]byte(values[0]), &paths); err != nil {
		return nil, errors.New("Uploaded folder paths could not be read")
	}
	return paths, nil
}

func detectServerTypeFromPath(serverPath string) string {
	entries, err := os.ReadDir(serverPath)
	if err != nil {
		return "vanilla"
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, strings.ToLower(entry.Name()))
	}
	for _, name := range names {
		if strings.Contains(name, "fabric") {
			return "fabric"
		}
	}
	for _, name := range names {
		if strings.Contains(name, "paper") {
			return "paper"
		}
	}
	for _, name := range names {
		if strings.Contains(name, "neoforge") {
			return "neoforge"
		}
	}
	for _, name := range names {
		if strings.Contains(name, "forge") {
			return "forge"
		}
	}
	return "vanilla"
}

func detectMinecraftProfileFromPath(serverPath string, serverType string) (string, string) {
	entries, err := os.ReadDir(serverPath)
	if err != nil {
		return "", ""
	}
	lowerFiles := []string{}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".jar") {
			lowerFiles = append(lowerFiles, strings.ToLower(entry.Name()))
		}
	}
	if serverType == "fabric" {
		pattern := regexp.MustCompile(`(?:mc|minecraft)[-_.]?(\d+\.\d+(?:\.\d+)?)[-_.].*loader[-_.]?([0-9][a-z0-9.+-]*)`)
		for _, file := range lowerFiles {
			if strings.Contains(file, "fabric") {
				if match := pattern.FindStringSubmatch(file); len(match) == 3 {
					return match[1], strings.TrimSuffix(strings.TrimSuffix(regexp.MustCompile(`[-_.]?launcher.*$`).ReplaceAllString(match[2], ""), ".jar"), "-installer")
				}
			}
		}
	}
	if serverType == "forge" {
		pattern := regexp.MustCompile(`forge-(\d+\.\d+(?:\.\d+)?)-([0-9][a-z0-9.+-]*)`)
		for _, file := range lowerFiles {
			if strings.Contains(file, "forge") {
				if match := pattern.FindStringSubmatch(file); len(match) == 3 {
					return match[1], cleanJarVersion(match[2])
				}
			}
		}
	}
	if serverType == "neoforge" {
		pattern := regexp.MustCompile(`neoforge-([0-9][a-z0-9.+-]*)`)
		for _, file := range lowerFiles {
			if strings.Contains(file, "neoforge") {
				if match := pattern.FindStringSubmatch(file); len(match) == 2 {
					loader := cleanJarVersion(match[1])
					return minecraftVersionFromNeoForgeLoader(loader), loader
				}
			}
		}
	}
	versionPattern := regexp.MustCompile(`(\d+\.\d+(?:\.\d+)?)`)
	for _, file := range lowerFiles {
		if regexp.MustCompile(`(?:minecraft[_-]?server|server)[_.-]\d+\.\d+`).MatchString(file) {
			if match := versionPattern.FindStringSubmatch(file); len(match) == 2 {
				return match[1], ""
			}
		}
	}
	return "", ""
}

func cleanJarVersion(value string) string {
	value = strings.TrimSuffix(value, "-installer.jar")
	value = strings.TrimSuffix(value, ".jar")
	return value
}

func minecraftVersionFromNeoForgeLoader(loaderVersion string) string {
	parts := strings.Split(loaderVersion, ".")
	if len(parts) < 2 {
		return ""
	}
	if parts[0] == "20" || parts[0] == "21" {
		return "1." + parts[0] + "." + parts[1]
	}
	if len(parts) >= 3 {
		return parts[0] + "." + parts[1] + "." + parts[2]
	}
	return parts[0] + "." + parts[1]
}

func countImportedMods(serverPath string) (int, int) {
	countJars := func(path string) int {
		entries, err := os.ReadDir(path)
		if err != nil {
			return 0
		}
		count := 0
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".jar") {
				count++
			}
		}
		return count
	}
	return countJars(filepath.Join(serverPath, "mods")), countJars(filepath.Join(serverPath, ".dashboard-disabled-mods"))
}

func (h apiHandler) importSessionPath(token string) string {
	return filepath.Join(h.config.DataDir, "imports", token)
}

func newImportToken() (string, error) {
	data := make([]byte, 8)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return "imp_" + hex.EncodeToString(data), nil
}

func fileDisplayName(fileName string) string {
	name := strings.TrimSuffix(filepath.Base(fileName), filepath.Ext(fileName))
	if name == "" {
		return "Imported server"
	}
	return name
}

func (h apiHandler) provisionServer(r *http.Request, server *store.Server) (string, error) {
	if server.Type == "vanilla" {
		download, err := h.vanillaServerDownload(r, server.MinecraftVersion)
		if err != nil {
			return "", err
		}
		if err := downloadFile(r, download.URL, filepath.Join(server.Path, "server.jar")); err != nil {
			return "", err
		}
		server.LaunchJar = "server.jar"
		if err := writeDefaultServerFiles(*server); err != nil {
			return "", err
		}
		return "Vanilla server jar downloaded", nil
	}
	if server.Type == "paper" {
		downloadURL, err := h.paperServerDownload(r, server.MinecraftVersion)
		if err != nil {
			return "", err
		}
		if err := downloadFile(r, downloadURL, filepath.Join(server.Path, "server.jar")); err != nil {
			return "", err
		}
		server.LaunchJar = "server.jar"
		if err := writeDefaultServerFiles(*server); err != nil {
			return "", err
		}
		_ = os.MkdirAll(filepath.Join(server.Path, "plugins"), 0o755)
		return "Paper server jar downloaded", nil
	}
	if server.Type == "purpur" {
		downloadURL, err := h.purpurServerDownload(r, server.MinecraftVersion)
		if err != nil {
			return "", err
		}
		if err := downloadFile(r, downloadURL, filepath.Join(server.Path, "server.jar")); err != nil {
			return "", err
		}
		server.LaunchJar = "server.jar"
		if err := writeDefaultServerFiles(*server); err != nil {
			return "", err
		}
		_ = os.MkdirAll(filepath.Join(server.Path, "plugins"), 0o755)
		return "Purpur server jar downloaded", nil
	}
	if server.Type == "folia" {
		downloadURL, err := h.foliaServerDownload(r, server.MinecraftVersion)
		if err != nil {
			return "", err
		}
		if err := downloadFile(r, downloadURL, filepath.Join(server.Path, "server.jar")); err != nil {
			return "", err
		}
		server.LaunchJar = "server.jar"
		if err := writeDefaultServerFiles(*server); err != nil {
			return "", err
		}
		_ = os.MkdirAll(filepath.Join(server.Path, "plugins"), 0o755)
		return "Folia server jar downloaded", nil
	}
	if server.Type == "fabric" {
		installer, err := h.latestFabricInstaller(r)
		if err != nil {
			return "", err
		}
		requestURL := "https://meta.fabricmc.net/v2/versions/loader/" + url.PathEscape(server.MinecraftVersion) + "/" + url.PathEscape(server.LoaderVersion) + "/" + url.PathEscape(installer) + "/server/jar"
		if err := downloadFile(r, requestURL, filepath.Join(server.Path, "fabric-server-launch.jar")); err != nil {
			return "", err
		}
		server.LaunchJar = "fabric-server-launch.jar"
		if err := writeDefaultServerFiles(*server); err != nil {
			return "", err
		}
		_ = os.MkdirAll(filepath.Join(server.Path, "mods"), 0o755)
		return "Fabric server launcher downloaded", nil
	}
	if server.Type == "forge" {
		fullVersion := server.MinecraftVersion + "-" + server.LoaderVersion
		installerName := "forge-" + fullVersion + "-installer.jar"
		requestURL := "https://maven.minecraftforge.net/net/minecraftforge/forge/" + url.PathEscape(fullVersion) + "/" + installerName
		if err := downloadFile(r, requestURL, filepath.Join(server.Path, installerName)); err != nil {
			return "", err
		}
		server.LaunchJar = installerName
		_ = writeDefaultServerFiles(*server)
		_ = os.MkdirAll(filepath.Join(server.Path, "mods"), 0o755)
		return "Forge installer downloaded. Run the installer from Files before first launch.", nil
	}
	if server.Type == "neoforge" {
		installerName := "neoforge-" + server.LoaderVersion + "-installer.jar"
		requestURL := "https://maven.neoforged.net/releases/net/neoforged/neoforge/" + url.PathEscape(server.LoaderVersion) + "/" + installerName
		if err := downloadFile(r, requestURL, filepath.Join(server.Path, installerName)); err != nil {
			return "", err
		}
		server.LaunchJar = installerName
		_ = writeDefaultServerFiles(*server)
		_ = os.MkdirAll(filepath.Join(server.Path, "mods"), 0o755)
		return "NeoForge installer downloaded. Run the installer from Files before first launch.", nil
	}
	return "", nil
}

func (h apiHandler) vanillaServerDownload(r *http.Request, minecraftVersion string) (*struct {
	URL  string `json:"url"`
	SHA1 string `json:"sha1"`
	Size int64  `json:"size"`
}, error) {
	metadata, err := h.getMinecraftMetadata(r, false)
	if err != nil {
		return nil, err
	}
	for _, version := range metadata.MinecraftVersions {
		if version.ID == minecraftVersion {
			var details versionDetails
			if err := fetchJSON(r, version.URL, &details); err != nil {
				return nil, err
			}
			if details.Downloads.Server == nil || details.Downloads.Server.URL == "" {
				return nil, errors.New("Minecraft " + minecraftVersion + " does not provide a server jar")
			}
			return details.Downloads.Server, nil
		}
	}
	return nil, errors.New("Minecraft " + minecraftVersion + " was not found in Mojang metadata")
}

func (h apiHandler) paperServerDownload(r *http.Request, minecraftVersion string) (string, error) {
	var builds []paperBuild
	requestURL := "https://fill.papermc.io/v3/projects/paper/versions/" + url.PathEscape(minecraftVersion) + "/builds"
	if err := fetchJSON(r, requestURL, &builds); err != nil {
		return "", err
	}
	// Prefer STABLE builds, fall back to ALPHA/experimental builds
	for _, build := range builds {
		if build.Channel == "STABLE" {
			if download := build.Downloads["server:default"]; download.URL != "" {
				return download.URL, nil
			}
		}
	}
	for _, build := range builds {
		if download := build.Downloads["server:default"]; download.URL != "" {
			return download.URL, nil
		}
	}
	return "", errors.New("Paper does not provide a server build for Minecraft " + minecraftVersion)
}

func (h apiHandler) purpurServerDownload(r *http.Request, minecraftVersion string) (string, error) {
	var info purpurVersionInfo
	requestURL := "https://api.purpurmc.org/v2/purpur/" + url.PathEscape(minecraftVersion)
	if err := fetchJSON(r, requestURL, &info); err != nil {
		return "", err
	}
	if info.Builds.Latest == "" && len(info.Builds.All) == 0 {
		return "", errors.New("Purpur does not provide a build for Minecraft " + minecraftVersion)
	}
	// Use the latest build
	latest := info.Builds.Latest
	if latest == "" && len(info.Builds.All) > 0 {
		latest = info.Builds.All[len(info.Builds.All)-1]
	}
	return "https://api.purpurmc.org/v2/purpur/" + url.PathEscape(minecraftVersion) + "/" + url.PathEscape(latest) + "/download", nil
}

func (h apiHandler) foliaServerDownload(r *http.Request, minecraftVersion string) (string, error) {
	var builds []paperBuild
	requestURL := "https://fill.papermc.io/v3/projects/folia/versions/" + url.PathEscape(minecraftVersion) + "/builds"
	if err := fetchJSON(r, requestURL, &builds); err != nil {
		return "", err
	}
	// Prefer STABLE builds, fall back to ALPHA/experimental builds
	for _, build := range builds {
		if build.Channel == "STABLE" {
			if download := build.Downloads["server:default"]; download.URL != "" {
				return download.URL, nil
			}
		}
	}
	for _, build := range builds {
		if download := build.Downloads["server:default"]; download.URL != "" {
			return download.URL, nil
		}
	}
	return "", errors.New("Folia does not provide a server build for Minecraft " + minecraftVersion)
}

func (h apiHandler) latestFabricInstaller(r *http.Request) (string, error) {
	var installers []fabricInstaller
	if err := fetchJSON(r, "https://meta.fabricmc.net/v2/versions/installer", &installers); err != nil {
		return "", err
	}
	for _, installer := range installers {
		if installer.Stable {
			return installer.Version, nil
		}
	}
	if len(installers) == 0 {
		return "", errors.New("No Fabric installer version is available")
	}
	return installers[0].Version, nil
}

func availableManagedPath(r *http.Request, h apiHandler, root string, name string) (string, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	servers, err := h.store.ListServers(r.Context())
	if err != nil {
		return "", err
	}
	registered := map[string]bool{}
	for _, server := range servers {
		registered[filepath.Clean(server.Path)] = true
	}
	slug := serverSlug(name)
	for index := 0; index < 100; index++ {
		candidate := filepath.Join(root, slug)
		if index > 0 {
			candidate = filepath.Join(root, slug+"-"+strconv.Itoa(index+1))
		}
		candidate, _ = filepath.Abs(candidate)
		if registered[filepath.Clean(candidate)] {
			continue
		}
		entries, err := os.ReadDir(candidate)
		if os.IsNotExist(err) || (err == nil && len(entries) == 0) {
			return candidate, nil
		}
	}
	return "", errors.New("Could not find an available folder name for this server")
}

func writeDefaultServerFiles(server store.Server) error {
	if err := os.MkdirAll(server.Path, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(server.Path, "eula.txt"), []byte("eula=false\n"), 0o644); err != nil {
		return err
	}
	return writeServerPropertiesFile(server, map[string]any{
		"motd":               "A Minecraft Server",
		"levelName":          "world",
		"gamemode":           "survival",
		"difficulty":         "easy",
		"maxPlayers":         20,
		"serverPort":         server.Port,
		"viewDistance":       10,
		"simulationDistance": 10,
		"onlineMode":         true,
		"whiteList":          false,
		"pvp":                true,
		"enableCommandBlock": false,
		"allowFlight":        false,
	}, nil)
}

func downloadFile(r *http.Request, requestURL string, destination string) error {
	response, err := fetchResponse(r, requestURL)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("Download failed: " + strconv.Itoa(response.StatusCode))
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	copyErr := copyBoundedDownload(output, response.Body, maxArtifactDownloadBytes)
	closeErr := output.Close()
	if copyErr != nil {
		_ = os.Remove(destination)
		return copyErr
	}
	return closeErr
}

func copyDirectory(source string, target string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return os.MkdirAll(target, 0o755)
		}
		destination := filepath.Join(target, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(destination, info.Mode())
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer output.Close()
		_, err = io.Copy(output, input)
		return err
	})
}

func detectLaunchJar(serverPath string, serverType string) string {
	script := "run.sh"
	if os.PathSeparator == '\\' {
		script = "run.bat"
	}
	if fileExists(filepath.Join(serverPath, script)) {
		return script
	}
	entries, err := os.ReadDir(serverPath)
	if err != nil {
		return ""
	}
	jars := []string{}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".jar") {
			jars = append(jars, entry.Name())
		}
	}
	for _, jar := range jars {
		lower := strings.ToLower(jar)
		if strings.Contains(lower, serverType) && !strings.HasSuffix(lower, "-installer.jar") {
			return jar
		}
	}
	for _, jar := range jars {
		lower := strings.ToLower(jar)
		if strings.Contains(lower, "server") && !strings.HasSuffix(lower, "-installer.jar") {
			return jar
		}
	}
	if len(jars) > 0 {
		return jars[0]
	}
	return ""
}

func metadataHasMinecraftVersion(metadata minecraftMetadata, minecraftVersion string) bool {
	for _, version := range metadata.MinecraftVersions {
		if version.ID == minecraftVersion {
			return true
		}
	}
	return false
}

func loaderListContains(loaders []loaderOption, loaderVersion string) bool {
	for _, loader := range loaders {
		if loader.Version == loaderVersion {
			return true
		}
	}
	return false
}

func serverSlug(name string) string {
	slug := strings.Trim(serverSlugPattern.ReplaceAllString(strings.TrimSpace(name), "-"), "-")
	if len(slug) > 80 {
		slug = slug[:80]
	}
	if slug == "" {
		return "new-server"
	}
	return slug
}

func displayName(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return fallback
}

func portValue(value int, fallback int) (int, error) {
	if value == 0 {
		value = fallback
	}
	if value < 1 || value > 65535 {
		return 0, errors.New("Server port must be between 1 and 65535")
	}
	return value, nil
}

func javaPathValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "java"
	}
	return value
}
