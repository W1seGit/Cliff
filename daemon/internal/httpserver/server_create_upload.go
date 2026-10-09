package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

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
