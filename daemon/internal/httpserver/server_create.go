package httpserver

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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
	// ProgressID lets the dashboard follow this operation step by step.
	ProgressID string `json:"progressId"`
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
	progress := h.createProgress.begin(input.ProgressID, createSteps(input))
	server, note, err := h.dispatchCreate(withProgress(r, progress), input)
	progress.finish(err)
	return server, note, err
}

func (h apiHandler) dispatchCreate(r *http.Request, input serverCreateInput) (store.Server, string, error) {
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
	progressFrom(r).start("prepare")
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
	progressFrom(r).start("save")
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
	progressFrom(r).start("copy")
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
	progressFrom(r).start("save")
	created, err := h.store.CreateServer(r.Context(), server)
	if err != nil {
		_ = os.RemoveAll(target)
		return store.Server{}, "", err
	}
	progressFrom(r).start("properties")
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
	progressFrom(r).start("check")
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
	progressFrom(r).start("copy")
	if err := copyDirectory(source, target); err != nil {
		_ = os.RemoveAll(target)
		return store.Server{}, "", err
	}
	progressFrom(r).start("detect")
	server, err := h.serverRecordFromInput(r, input, target, name)
	if err != nil {
		_ = os.RemoveAll(target)
		return store.Server{}, "", err
	}
	if server.LaunchJar == "" {
		server.LaunchJar = detectLaunchJar(target, server.Type)
	}
	progressFrom(r).start("save")
	created, err := h.store.CreateServer(r.Context(), server)
	if err != nil {
		_ = os.RemoveAll(target)
		return store.Server{}, "", err
	}
	return created, "Server imported", nil
}

func (h apiHandler) importStagedServer(r *http.Request, input serverCreateInput) (store.Server, string, error) {
	progressFrom(r).start("check")
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
	progressFrom(r).start("copy")
	if err := copyDirectory(stagedPath, target); err != nil {
		_ = os.RemoveAll(target)
		return store.Server{}, "", err
	}
	progressFrom(r).start("detect")
	server, err := h.serverRecordFromInput(r, input, target, name)
	if err != nil {
		_ = os.RemoveAll(target)
		return store.Server{}, "", err
	}
	if server.LaunchJar == "" {
		server.LaunchJar = detectLaunchJar(target, server.Type)
	}
	progressFrom(r).start("save")
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
