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
	"sort"
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
	Token             string   `json:"token,omitempty"`
	Name              string   `json:"name"`
	Path              string   `json:"path"`
	Type              string   `json:"type"`
	MinecraftVersion  string   `json:"minecraftVersion"`
	LoaderVersion     string   `json:"loaderVersion"`
	Port              int      `json:"port"`
	ActiveWorld       string   `json:"activeWorld"`
	LaunchJar         string   `json:"launchJar"`
	AlreadyRegistered bool     `json:"alreadyRegistered"`
	Mods              int      `json:"mods"`
	DisabledMods      int      `json:"disabledMods"`
	Warnings          []string `json:"warnings,omitempty"`
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

const maxImportFieldBytes int64 = 16 << 20

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
	upload, err := readServerImportUpload(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer upload.RemoveAll()

	mode := strings.TrimSpace(upload.Value("mode"))
	if mode != "detect-zip" && mode != "detect-folder" && mode != "import-zip" && mode != "import-folder" {
		writeError(w, http.StatusBadRequest, "Unsupported server upload action")
		return
	}
	detectMode := mode == "detect-zip" || mode == "detect-folder"
	zipMode := mode == "detect-zip" || mode == "import-zip"
	fallbackName := "Imported server"
	if zipMode {
		if upload.ZipPath == "" {
			writeError(w, http.StatusBadRequest, "Server ZIP is required")
			return
		}
		fallbackName = fileDisplayName(upload.ZipName)
	} else {
		paths, _ := upload.Paths()
		if len(paths) > 0 {
			parts, err := relativeUploadParts(paths[0])
			if err == nil && len(parts) > 0 {
				fallbackName = parts[0]
			}
		}
	}
	name := displayName(upload.Value("name"), fallbackName)
	serverPath := ""
	if detectMode {
		token, err := newImportToken()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Import session could not be created")
			return
		}
		serverPath = h.importSessionPath(token)
		if err := upload.Write(zipMode, serverPath); err != nil {
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
	if err := upload.Write(zipMode, serverPath); err != nil {
		_ = os.RemoveAll(serverPath)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	input := serverCreateInput{
		Name:             name,
		Type:             upload.Value("type"),
		MinecraftVersion: upload.Value("minecraftVersion"),
		LoaderVersion:    upload.Value("loaderVersion"),
		JavaPath:         upload.Value("javaPath"),
		MinMemoryMB:      atoiDefault(upload.Value("minMemoryMb"), 0),
		MaxMemoryMB:      atoiDefault(upload.Value("maxMemoryMb"), 0),
		Port:             atoiDefault(upload.Value("port"), 0),
		LaunchJar:        upload.Value("launchJar"),
		ExtraArgs:        upload.Value("extraArgs"),
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

type stagedImportUpload struct {
	Root        string
	Values      map[string]string
	ZipPath     string
	ZipName     string
	FolderFiles []stagedImportFile
}

type stagedImportFile struct {
	Path string
	Name string
}

func readServerImportUpload(r *http.Request) (*stagedImportUpload, error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, errors.New("Server upload could not be read")
	}
	root, err := os.MkdirTemp("", "cliff-server-import-*")
	if err != nil {
		return nil, errors.New("Server upload could not be staged")
	}
	upload := &stagedImportUpload{
		Root:   root,
		Values: map[string]string{},
	}
	fail := func(err error) (*stagedImportUpload, error) {
		_ = os.RemoveAll(root)
		return nil, err
	}
	fileIndex := 0
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fail(errors.New("Server upload could not be read"))
		}
		formName := part.FormName()
		fileName := part.FileName()
		if formName == "" {
			_ = part.Close()
			continue
		}
		if fileName == "" {
			limited := io.LimitReader(part, maxImportFieldBytes+1)
			data, readErr := io.ReadAll(limited)
			closeErr := part.Close()
			if readErr != nil || closeErr != nil {
				return fail(errors.New("Server upload fields could not be read"))
			}
			if int64(len(data)) > maxImportFieldBytes {
				return fail(errors.New("Server upload metadata is too large"))
			}
			upload.Values[formName] = string(data)
			continue
		}
		stagedPath := filepath.Join(root, strconv.Itoa(fileIndex)+".part")
		fileIndex++
		output, err := os.OpenFile(stagedPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			_ = part.Close()
			return fail(errors.New("Server upload could not be staged"))
		}
		_, copyErr := io.Copy(output, part)
		closeErr := output.Close()
		partCloseErr := part.Close()
		if copyErr != nil || closeErr != nil || partCloseErr != nil {
			return fail(errors.New("Server upload could not be staged"))
		}
		if formName == "file" {
			upload.ZipPath = stagedPath
			upload.ZipName = fileName
			continue
		}
		if formName == "files" {
			upload.FolderFiles = append(upload.FolderFiles, stagedImportFile{Path: stagedPath, Name: fileName})
		}
	}
	return upload, nil
}

func (u *stagedImportUpload) RemoveAll() {
	if u != nil && u.Root != "" {
		_ = os.RemoveAll(u.Root)
	}
}

func (u *stagedImportUpload) Value(name string) string {
	if u == nil {
		return ""
	}
	return u.Values[name]
}

func (u *stagedImportUpload) Paths() ([]string, error) {
	if u == nil {
		return []string{}, nil
	}
	value := strings.TrimSpace(u.Values["paths"])
	if value == "" {
		return []string{}, nil
	}
	paths := []string{}
	if err := json.Unmarshal([]byte(value), &paths); err != nil {
		return nil, errors.New("Uploaded folder paths could not be read")
	}
	return paths, nil
}

func (u *stagedImportUpload) Write(zipMode bool, targetRoot string) error {
	if zipMode {
		if u.ZipPath == "" {
			return errors.New("Server ZIP is required")
		}
		return extractStagedServerZip(u.ZipPath, u.ZipName, targetRoot)
	}
	paths, err := u.Paths()
	if err != nil {
		return err
	}
	return writeStagedFolder(u.FolderFiles, paths, targetRoot)
}

func (h apiHandler) inspectServerFolder(r *http.Request, serverPath string, fallbackName string, token string) (importDetection, error) {
	scan := scanImportedServer(serverPath)
	serverType := scan.ServerType
	minecraftVersion, loaderVersion := scan.MinecraftVersion, scan.LoaderVersion
	if minecraftVersion == "" {
		if metadata, err := h.getMinecraftMetadata(r, false); err == nil {
			minecraftVersion = metadata.Latest.Release
			if serverTypeNeedsLoader(serverType) {
				scan.Warnings = append(scan.Warnings, "Minecraft version could not be read from the imported server. Review the selected version before importing.")
			}
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
		LaunchJar:         scan.LaunchTarget,
		AlreadyRegistered: registered,
		Mods:              mods,
		DisabledMods:      disabledMods,
		Warnings:          scan.Warnings,
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

func extractStagedServerZip(path string, filename string, targetRoot string) error {
	if !strings.HasSuffix(strings.ToLower(filename), ".zip") {
		return errors.New("Upload a .zip server archive")
	}
	file, err := os.Open(path)
	if err != nil {
		return errors.New("Server ZIP could not be read")
	}
	defer file.Close()
	archive, err := zipReaderFromMultipart(file, "Server ZIP could not be read")
	if err != nil {
		return err
	}
	return extractServerZipArchive(archive, targetRoot)
}

func extractServerZipArchive(archive *zip.Reader, targetRoot string) error {
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

func writeStagedFolder(files []stagedImportFile, relativePaths []string, targetRoot string) error {
	if len(files) == 0 {
		return errors.New("Choose a server folder to import")
	}
	entries := make([]struct {
		file  stagedImportFile
		parts []string
	}, 0, len(files))
	roots := map[string]bool{}
	for index, file := range files {
		relativePath := file.Name
		if index < len(relativePaths) && relativePaths[index] != "" {
			relativePath = relativePaths[index]
		}
		parts, err := relativeUploadParts(relativePath)
		if err != nil {
			return err
		}
		entries = append(entries, struct {
			file  stagedImportFile
			parts []string
		}{file: file, parts: parts})
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
		input, err := os.Open(entry.file.Path)
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

type importedServerScan struct {
	ServerType       string
	MinecraftVersion string
	LoaderVersion    string
	LaunchTarget     string
	Warnings         []string
}

type importJarCandidate struct {
	Name             string
	Lower            string
	MainClass        string
	ServerType       string
	MinecraftVersion string
	LoaderVersion    string
	Score            int
	Installer        bool
}

func detectServerTypeFromPath(serverPath string) string {
	return scanImportedServer(serverPath).ServerType
}

func detectMinecraftProfileFromPath(serverPath string, serverType string) (string, string) {
	scan := scanImportedServer(serverPath)
	if scan.ServerType != serverType {
		return "", ""
	}
	return scan.MinecraftVersion, scan.LoaderVersion
}

func detectLaunchJar(serverPath string, serverType string) string {
	scan := scanImportedServer(serverPath)
	if scan.ServerType != serverType && serverType != "" {
		if scan.ServerType == "vanilla" {
			return ""
		}
	}
	return scan.LaunchTarget
}

func scanImportedServer(serverPath string) importedServerScan {
	scan := importedServerScan{ServerType: "vanilla"}
	evidence := map[string]int{"vanilla": 1}
	topJars, _ := topLevelJars(serverPath)

	_, scriptJar, scriptArgFile := detectLaunchScript(serverPath)
	if scriptJar != "" {
		evidenceFromName(filepath.Base(scriptJar), evidence, &scan)
	}
	if scriptArgFile != "" {
		evidenceFromArgFilePath(scriptArgFile, evidence, &scan)
	}

	if mc, loader := detectForgeProfileFromLibraries(serverPath); mc != "" || loader != "" {
		evidence["forge"] += 90
		scan.MinecraftVersion = firstNonEmpty(scan.MinecraftVersion, mc)
		scan.LoaderVersion = firstNonEmpty(scan.LoaderVersion, loader)
	}
	if mc, loader := detectNeoForgeProfileFromLibraries(serverPath); mc != "" || loader != "" {
		evidence["neoforge"] += 95
		scan.MinecraftVersion = firstNonEmpty(scan.MinecraftVersion, mc)
		scan.LoaderVersion = firstNonEmpty(scan.LoaderVersion, loader)
	}
	if mc, loader := detectFabricProfileFromLibraries(serverPath); mc != "" || loader != "" {
		evidence["fabric"] += 95
		scan.MinecraftVersion = firstNonEmpty(scan.MinecraftVersion, mc)
		scan.LoaderVersion = firstNonEmpty(scan.LoaderVersion, loader)
	}

	candidates := []importJarCandidate{}
	for _, jar := range topJars {
		candidate := inspectImportJar(serverPath, jar)
		candidates = append(candidates, candidate)
		evidenceFromJar(candidate, evidence, &scan)
	}

	scan.ServerType = strongestServerType(evidence)
	if scan.MinecraftVersion == "" || (serverTypeNeedsLoader(scan.ServerType) && scan.LoaderVersion == "") {
		if mc, loader := detectMinecraftProfileFromJars(candidates, scan.ServerType); mc != "" || loader != "" {
			scan.MinecraftVersion = firstNonEmpty(scan.MinecraftVersion, mc)
			scan.LoaderVersion = firstNonEmpty(scan.LoaderVersion, loader)
		}
	}

	if scan.LaunchTarget == "" {
		if target, warning := bestJarLaunchTarget(candidates, scan.ServerType); target != "" || warning != "" {
			scan.LaunchTarget = target
			if warning != "" {
				scan.Warnings = append(scan.Warnings, warning)
			}
		}
	}
	if scan.LaunchTarget == "" && serverTypeNeedsLoader(scan.ServerType) {
		scan.Warnings = append(scan.Warnings, "No launchable server jar was detected. Choose the generated server launcher before importing.")
	}
	return scan
}

func topLevelJars(serverPath string) ([]string, error) {
	entries, err := os.ReadDir(serverPath)
	if err != nil {
		return nil, err
	}
	jars := []string{}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".jar") {
			jars = append(jars, entry.Name())
		}
	}
	sort.Strings(jars)
	return jars, nil
}

func detectLaunchScript(serverPath string) (string, string, string) {
	names := []string{"run.sh", "start.sh", "start.command", "server.sh", "run.bat", "start.bat", "server.bat"}
	for _, name := range names {
		path := filepath.Join(serverPath, name)
		if !fileExists(path) {
			continue
		}
		text := readSmallText(path, 64*1024)
		jar := extractJarFromLaunchText(text)
		argFile := extractArgFileFromLaunchText(text)
		return name, jar, argFile
	}
	return "", "", ""
}

func readSmallText(path string, limit int64) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	data, _ := io.ReadAll(io.LimitReader(file, limit))
	return string(data)
}

func extractJarFromLaunchText(text string) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)-jar\s+"([^"]+\.jar)"`),
		regexp.MustCompile(`(?i)-jar\s+'([^']+\.jar)'`),
		regexp.MustCompile(`(?i)-jar\s+([^\s]+\.jar)`),
	}
	for _, pattern := range patterns {
		if match := pattern.FindStringSubmatch(text); len(match) == 2 {
			return strings.Trim(match[1], `"'`)
		}
	}
	return ""
}

func extractArgFileFromLaunchText(text string) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`@"([^"]+\.txt)"`),
		regexp.MustCompile(`@'([^']+\.txt)'`),
		regexp.MustCompile(`@([^\s]+\.txt)`),
	}
	for _, pattern := range patterns {
		if match := pattern.FindStringSubmatch(text); len(match) == 2 {
			return strings.Trim(match[1], `"'`)
		}
	}
	return ""
}

func evidenceFromName(name string, evidence map[string]int, scan *importedServerScan) {
	lower := strings.ToLower(filepath.ToSlash(name))
	switch {
	case strings.Contains(lower, "neoforge"):
		evidence["neoforge"] += 40
	case strings.Contains(lower, "forge"):
		evidence["forge"] += 35
	case strings.Contains(lower, "fabric"):
		evidence["fabric"] += 40
	case strings.Contains(lower, "paper"):
		evidence["paper"] += 35
	case strings.Contains(lower, "purpur"):
		evidence["purpur"] += 35
	case strings.Contains(lower, "folia"):
		evidence["folia"] += 35
	}
	if scan.MinecraftVersion == "" {
		if match := regexp.MustCompile(`(\d+\.\d+(?:\.\d+)?)`).FindStringSubmatch(lower); len(match) == 2 {
			scan.MinecraftVersion = match[1]
		}
	}
}

func evidenceFromArgFilePath(path string, evidence map[string]int, scan *importedServerScan) {
	normalized := strings.ToLower(filepath.ToSlash(path))
	if match := regexp.MustCompile(`net/minecraftforge/forge/(\d+\.\d+(?:\.\d+)?)-([0-9][a-z0-9.+-]*)/`).FindStringSubmatch(normalized); len(match) == 3 {
		evidence["forge"] += 90
		scan.MinecraftVersion = firstNonEmpty(scan.MinecraftVersion, match[1])
		scan.LoaderVersion = firstNonEmpty(scan.LoaderVersion, match[2])
	}
	if match := regexp.MustCompile(`net/neoforged/neoforge/([0-9][a-z0-9.+-]*)/`).FindStringSubmatch(normalized); len(match) == 2 {
		evidence["neoforge"] += 90
		loader := match[1]
		scan.LoaderVersion = firstNonEmpty(scan.LoaderVersion, loader)
		scan.MinecraftVersion = firstNonEmpty(scan.MinecraftVersion, minecraftVersionFromNeoForgeLoader(loader))
	}
}

func detectForgeProfileFromLibraries(serverPath string) (string, string) {
	return detectProfileFromArgFiles(serverPath, regexp.MustCompile(`(?i)libraries/net/minecraftforge/forge/(\d+\.\d+(?:\.\d+)?)-([0-9][a-z0-9.+-]*)/(?:unix|win)_args\.txt$`))
}

func detectNeoForgeProfileFromLibraries(serverPath string) (string, string) {
	mc, loader := detectProfileFromArgFiles(serverPath, regexp.MustCompile(`(?i)libraries/net/neoforged/neoforge/([0-9][a-z0-9.+-]*)/(?:unix|win)_args\.txt$`))
	if loader == "" && mc != "" {
		loader = mc
		mc = minecraftVersionFromNeoForgeLoader(loader)
	}
	return mc, loader
}

func detectProfileFromArgFiles(serverPath string, pattern *regexp.Regexp) (string, string) {
	minecraftVersion, loaderVersion := "", ""
	_ = filepath.WalkDir(filepath.Join(serverPath, "libraries"), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(serverPath, path)
		if relErr != nil {
			return nil
		}
		normalized := filepath.ToSlash(rel)
		match := pattern.FindStringSubmatch(normalized)
		if len(match) == 3 {
			minecraftVersion, loaderVersion = match[1], match[2]
			return filepath.SkipAll
		}
		if len(match) == 2 {
			minecraftVersion = match[1]
			return filepath.SkipAll
		}
		return nil
	})
	return minecraftVersion, loaderVersion
}

func detectFabricProfileFromLibraries(serverPath string) (string, string) {
	minecraftVersion, loaderVersion := "", ""
	_ = filepath.WalkDir(filepath.Join(serverPath, "libraries"), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(serverPath, path)
		if relErr != nil {
			return nil
		}
		normalized := strings.ToLower(filepath.ToSlash(rel))
		if loaderVersion == "" {
			if match := regexp.MustCompile(`libraries/net/fabricmc/fabric-loader/([0-9][a-z0-9.+-]*)/`).FindStringSubmatch(normalized); len(match) == 2 {
				loaderVersion = match[1]
			}
		}
		if minecraftVersion == "" {
			if match := regexp.MustCompile(`libraries/net/minecraft/server/(\d+\.\d+(?:\.\d+)?)/`).FindStringSubmatch(normalized); len(match) == 2 {
				minecraftVersion = match[1]
			}
		}
		if minecraftVersion != "" && loaderVersion != "" {
			return filepath.SkipAll
		}
		return nil
	})
	if minecraftVersion == "" {
		minecraftVersion = detectMinecraftVersionFromVersionDirs(serverPath)
	}
	return minecraftVersion, loaderVersion
}

func detectMinecraftVersionFromVersionDirs(serverPath string) string {
	entries, err := os.ReadDir(filepath.Join(serverPath, "versions"))
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			name := entry.Name()
			if regexp.MustCompile(`^\d+\.\d+(?:\.\d+)?$`).MatchString(name) {
				return name
			}
		}
	}
	return ""
}

func inspectImportJar(serverPath string, name string) importJarCandidate {
	candidate := importJarCandidate{Name: name, Lower: strings.ToLower(name)}
	candidate.Installer = isInstallerJar(candidate.Lower)
	candidate.MainClass = readJarMainClass(filepath.Join(serverPath, name))
	lowerMain := strings.ToLower(candidate.MainClass)
	evidenceFromJarName(&candidate)
	if strings.Contains(lowerMain, "installer") {
		candidate.Installer = true
		candidate.Score -= 120
	}
	switch {
	case strings.Contains(lowerMain, "fabric") && strings.Contains(lowerMain, "server"):
		candidate.ServerType = "fabric"
		candidate.Score += 110
	case strings.Contains(lowerMain, "net.minecraft.server"):
		candidate.Score += 90
	case strings.Contains(lowerMain, "paper"):
		candidate.ServerType = "paper"
		candidate.Score += 80
	case strings.Contains(lowerMain, "purpur"):
		candidate.ServerType = "purpur"
		candidate.Score += 80
	case strings.Contains(lowerMain, "folia"):
		candidate.ServerType = "folia"
		candidate.Score += 80
	}
	return candidate
}

func evidenceFromJarName(candidate *importJarCandidate) {
	lower := candidate.Lower
	if lower == "fabric-server-launch.jar" {
		candidate.ServerType = "fabric"
		candidate.Score += 150
	}
	switch {
	case strings.Contains(lower, "neoforge"):
		candidate.ServerType = "neoforge"
		candidate.Score += 35
	case strings.Contains(lower, "forge"):
		candidate.ServerType = "forge"
		candidate.Score += 35
	case strings.Contains(lower, "fabric"):
		candidate.ServerType = "fabric"
		candidate.Score += 40
	case strings.Contains(lower, "paper"):
		candidate.ServerType = "paper"
		candidate.Score += 45
	case strings.Contains(lower, "purpur"):
		candidate.ServerType = "purpur"
		candidate.Score += 45
	case strings.Contains(lower, "folia"):
		candidate.ServerType = "folia"
		candidate.Score += 45
	case strings.Contains(lower, "server"):
		candidate.Score += 25
	}
	if candidate.Installer {
		candidate.Score -= 120
	}
}

func readJarMainClass(path string) string {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return ""
	}
	defer reader.Close()
	for _, file := range reader.File {
		if strings.EqualFold(file.Name, "META-INF/MANIFEST.MF") {
			if file.UncompressedSize64 > 128*1024 {
				return ""
			}
			rc, err := file.Open()
			if err != nil {
				return ""
			}
			data, _ := io.ReadAll(io.LimitReader(rc, 128*1024))
			_ = rc.Close()
			return manifestValue(string(data), "Main-Class")
		}
	}
	return ""
}

func manifestValue(manifest string, key string) string {
	lines := strings.Split(strings.ReplaceAll(manifest, "\r\n", "\n"), "\n")
	prefix := strings.ToLower(key) + ":"
	for index, line := range lines {
		if strings.HasPrefix(strings.ToLower(line), prefix) {
			value := strings.TrimSpace(line[len(prefix):])
			for next := index + 1; next < len(lines); next++ {
				if !strings.HasPrefix(lines[next], " ") {
					break
				}
				value += strings.TrimSpace(lines[next])
			}
			return value
		}
	}
	return ""
}

func evidenceFromJar(candidate importJarCandidate, evidence map[string]int, scan *importedServerScan) {
	if candidate.ServerType != "" {
		evidence[candidate.ServerType] += maxInt(candidate.Score, 1)
	}
	evidenceFromName(candidate.Name, evidence, scan)
	if candidate.MinecraftVersion != "" {
		scan.MinecraftVersion = firstNonEmpty(scan.MinecraftVersion, candidate.MinecraftVersion)
	}
	if candidate.LoaderVersion != "" {
		scan.LoaderVersion = firstNonEmpty(scan.LoaderVersion, candidate.LoaderVersion)
	}
}

func strongestServerType(evidence map[string]int) string {
	bestType, bestScore := "vanilla", evidence["vanilla"]
	preference := map[string]int{"fabric": 6, "neoforge": 5, "forge": 4, "paper": 3, "purpur": 2, "folia": 1, "vanilla": 0}
	for serverType, score := range evidence {
		if score > bestScore || (score == bestScore && preference[serverType] > preference[bestType]) {
			bestType, bestScore = serverType, score
		}
	}
	return bestType
}

func detectMinecraftProfileFromJars(candidates []importJarCandidate, serverType string) (string, string) {
	for _, candidate := range candidates {
		lower := candidate.Lower
		if serverType == "fabric" && strings.Contains(lower, "fabric") {
			pattern := regexp.MustCompile(`(?:mc|minecraft)[-_.]?(\d+\.\d+(?:\.\d+)?)[-_.].*loader[-_.]?([0-9][a-z0-9.+-]*)`)
			if match := pattern.FindStringSubmatch(lower); len(match) == 3 {
				return match[1], strings.TrimSuffix(strings.TrimSuffix(regexp.MustCompile(`[-_.]?launcher.*$`).ReplaceAllString(match[2], ""), ".jar"), "-installer")
			}
		}
		if serverType == "forge" && strings.Contains(lower, "forge") {
			pattern := regexp.MustCompile(`forge-(\d+\.\d+(?:\.\d+)?)-([0-9][a-z0-9.+-]*)`)
			if match := pattern.FindStringSubmatch(lower); len(match) == 3 {
				return match[1], cleanJarVersion(match[2])
			}
		}
		if serverType == "neoforge" && strings.Contains(lower, "neoforge") {
			pattern := regexp.MustCompile(`neoforge-([0-9][a-z0-9.+-]*)`)
			if match := pattern.FindStringSubmatch(lower); len(match) == 2 {
				loader := cleanJarVersion(match[1])
				return minecraftVersionFromNeoForgeLoader(loader), loader
			}
		}
	}
	versionPattern := regexp.MustCompile(`(\d+\.\d+(?:\.\d+)?)`)
	for _, candidate := range candidates {
		if regexp.MustCompile(`(?:minecraft[_-]?server|server)[_.-]\d+\.\d+`).MatchString(candidate.Lower) {
			if match := versionPattern.FindStringSubmatch(candidate.Lower); len(match) == 2 {
				return match[1], ""
			}
		}
	}
	return "", ""
}

func bestJarLaunchTarget(candidates []importJarCandidate, serverType string) (string, string) {
	if len(candidates) == 0 {
		return "", ""
	}
	best := importJarCandidate{Score: -1000}
	installerCount := 0
	for _, candidate := range candidates {
		if candidate.Installer {
			installerCount++
			continue
		}
		score := candidate.Score
		if candidate.ServerType == serverType {
			score += 30
		}
		if strings.Contains(candidate.Lower, "server") {
			score += 20
		}
		if score > best.Score {
			best = candidate
			best.Score = score
		}
	}
	if best.Name != "" && best.Score >= 20 {
		return best.Name, ""
	}
	if installerCount > 0 {
		return "", "Installer jar found but not launchable. Choose a start script or generated server launcher before importing."
	}
	return "", ""
}

func cleanJarVersion(value string) string {
	value = strings.TrimSuffix(value, "-installer.jar")
	value = strings.TrimSuffix(value, ".jar")
	return value
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
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

// isInstallerJar reports whether a jar filename (lowercased) looks like a
// mod-loader installer rather than a server jar. These should not be used
// as launch targets with nogui.
func isInstallerJar(lower string) bool {
	return strings.Contains(lower, "installer") || strings.Contains(lower, "installer.jar")
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
