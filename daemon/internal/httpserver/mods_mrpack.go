package httpserver

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

const (
	maxMrpackBytes = 512 << 20
	// maxMrpackOverrideBytes caps the config files copied into an exported pack.
	maxMrpackOverrideBytes = 64 << 20
)

// mrpackDownloadHosts are the hosts the .mrpack format allows downloads from.
// An imported pack naming any other host is refused, so a pack cannot make the
// daemon fetch addresses on the local network.
var mrpackDownloadHosts = map[string]bool{
	"cdn.modrinth.com":          true,
	"github.com":                true,
	"raw.githubusercontent.com": true,
	"gitlab.com":                true,
}

// mrpackDependencyKeys maps a server type to its key in a pack's dependencies.
var mrpackDependencyKeys = map[string]string{
	"fabric":   "fabric-loader",
	"forge":    "forge",
	"neoforge": "neoforge",
}

type mrpackManifest struct {
	FormatVersion int               `json:"formatVersion"`
	Game          string            `json:"game"`
	VersionID     string            `json:"versionId"`
	Name          string            `json:"name"`
	Summary       string            `json:"summary,omitempty"`
	Files         []mrpackFile      `json:"files"`
	Dependencies  map[string]string `json:"dependencies"`
}

type mrpackFile struct {
	Path      string            `json:"path"`
	Hashes    map[string]string `json:"hashes"`
	Env       *mrpackEnv        `json:"env,omitempty"`
	Downloads []string          `json:"downloads"`
	FileSize  int64             `json:"fileSize"`
}

type mrpackEnv struct {
	Client string `json:"client,omitempty"`
	Server string `json:"server,omitempty"`
}

func readMrpackManifest(data []byte) (mrpackManifest, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return mrpackManifest{}, errors.New("That file is not a valid .mrpack modpack")
	}
	for _, entry := range reader.File {
		if entry.Name != "modrinth.index.json" {
			continue
		}
		file, err := entry.Open()
		if err != nil {
			return mrpackManifest{}, err
		}
		defer file.Close()
		var manifest mrpackManifest
		if err := json.NewDecoder(io.LimitReader(file, 8<<20)).Decode(&manifest); err != nil {
			return mrpackManifest{}, errors.New("The modpack index could not be read")
		}
		return manifest, nil
	}
	return mrpackManifest{}, errors.New("That file has no modrinth.index.json, so it is not a Modrinth modpack")
}

// checkMrpackForServer refuses a pack built for another Minecraft version or
// loader than the server runs, and any download host outside the allow list.
func checkMrpackForServer(manifest mrpackManifest, server store.Server) error {
	if manifest.Game != "" && manifest.Game != "minecraft" {
		return errors.New("This modpack is not for Minecraft")
	}
	if want := manifest.Dependencies["minecraft"]; want != "" && want != server.MinecraftVersion {
		return fmt.Errorf("This modpack is for Minecraft %s, but this server runs %s", want, server.MinecraftVersion)
	}
	if key, needsLoader := mrpackDependencyKeys[server.Type]; needsLoader {
		if manifest.Dependencies[key] == "" {
			return fmt.Errorf("This modpack does not support %s servers", server.Type)
		}
	} else {
		for _, key := range mrpackDependencyKeys {
			if manifest.Dependencies[key] != "" {
				return fmt.Errorf("This modpack needs a mod loader, but this is a %s server", server.Type)
			}
		}
	}
	for _, file := range manifest.Files {
		if len(file.Downloads) == 0 {
			continue
		}
		parsed, err := url.Parse(file.Downloads[0])
		if err != nil || parsed.Scheme != "https" || !mrpackDownloadHosts[strings.ToLower(parsed.Hostname())] {
			return fmt.Errorf("The modpack downloads %q from a host Cliff does not allow", file.Path)
		}
	}
	return nil
}

// importMrpack installs an uploaded .mrpack file into the server.
func (h apiHandler) importMrpack(w http.ResponseWriter, r *http.Request) {
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
	if h.process.IsRunning(server.ID) {
		writeError(w, http.StatusConflict, "Stop the server before installing a modpack")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMrpackBytes)
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "Choose a .mrpack file to upload")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "The modpack upload was too large or interrupted")
		return
	}
	manifest, err := readMrpackManifest(data)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := checkMrpackForServer(manifest, server); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := h.createBackup(r.Context(), server, "pre-modpack snapshot"); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not take a safety snapshot first: "+err.Error())
		return
	}
	title := firstNonEmpty(manifest.Name, "Imported modpack")
	files, err := h.applyModrinthModpack(r, server, data,
		modrinthProject{Title: title, Description: manifest.Summary},
		modrinthVersion{VersionNumber: manifest.VersionID, Name: manifest.VersionID})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": title, "files": files})
}

// exportMrpack writes the server's mods as a .mrpack. Mods Modrinth knows are
// listed by download link; the rest, and the config folder, are bundled.
func (h apiHandler) exportMrpack(w http.ResponseWriter, r *http.Request, server store.Server) {
	mods := listServerMods(server)
	hashes := make([]string, 0, len(mods))
	hashByName := map[string]string{}
	for _, mod := range mods {
		if !mod.Enabled {
			continue
		}
		if hash, err := sha1File(mod.Path); err == nil {
			hashByName[mod.FileName] = hash
			hashes = append(hashes, hash)
		}
	}
	known := map[string]modrinthFileVersion{}
	if len(hashes) > 0 {
		if err := modrinthPostJSON(r, "https://api.modrinth.com/v2/version_files", map[string]any{"hashes": hashes, "algorithm": "sha1"}, &known); err != nil {
			// Offline or rate limited: everything is bundled instead.
			known = map[string]modrinthFileVersion{}
		}
	}

	dir := modsActiveDir(server)
	manifest := mrpackManifest{
		FormatVersion: 1,
		Game:          "minecraft",
		VersionID:     time.Now().UTC().Format("2006.01.02"),
		Name:          server.Name,
		Summary:       "Exported from Cliff",
		Files:         []mrpackFile{},
		Dependencies:  map[string]string{"minecraft": server.MinecraftVersion},
	}
	if key, ok := mrpackDependencyKeys[server.Type]; ok && server.LoaderVersion != "" {
		manifest.Dependencies[key] = server.LoaderVersion
	}

	bundled := []string{} // jar names Modrinth does not host
	for _, mod := range mods {
		if !mod.Enabled {
			continue
		}
		version, isKnown := known[hashByName[mod.FileName]]
		downloadURL, sha512Hash := "", ""
		if isKnown {
			for _, versionFile := range version.Files {
				if strings.EqualFold(versionFile.Hashes["sha1"], hashByName[mod.FileName]) {
					downloadURL, sha512Hash = versionFile.URL, versionFile.Hashes["sha512"]
				}
			}
		}
		if downloadURL == "" || sha512Hash == "" {
			bundled = append(bundled, mod.FileName)
			continue
		}
		manifest.Files = append(manifest.Files, mrpackFile{
			Path:      dir + "/" + mod.FileName,
			Hashes:    map[string]string{"sha1": hashByName[mod.FileName], "sha512": sha512Hash},
			Downloads: []string{downloadURL},
			FileSize:  mod.Size,
		})
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })

	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	index, _ := json.MarshalIndent(manifest, "", "  ")
	if err := writeZipBytes(archive, "modrinth.index.json", index); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, name := range bundled {
		if err := addFileToZip(archive, "overrides/"+dir+"/"+name, filepath.Join(server.Path, dir, name)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := addConfigToZip(archive, server.Path); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := archive.Close(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/x-modrinth-modpack+zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+serverSlug(server.Name)+`.mrpack"`)
	w.Header().Set("Content-Length", fmt.Sprint(buffer.Len()))
	_, _ = w.Write(buffer.Bytes())
}

func writeZipBytes(archive *zip.Writer, name string, data []byte) error {
	entry, err := archive.Create(name)
	if err != nil {
		return err
	}
	_, err = entry.Write(data)
	return err
}

func addFileToZip(archive *zip.Writer, name string, path string) error {
	source, err := os.Open(path)
	if err != nil {
		return err
	}
	defer source.Close()
	entry, err := archive.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(entry, source)
	return err
}

// addConfigToZip copies the server's config folder into the pack overrides,
// stopping before the pack grows past maxMrpackOverrideBytes.
func addConfigToZip(archive *zip.Writer, serverPath string) error {
	root := filepath.Join(serverPath, "config")
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		if total > maxMrpackOverrideBytes {
			return fs.SkipAll
		}
		rel, err := filepath.Rel(serverPath, path)
		if err != nil {
			return err
		}
		return addFileToZip(archive, "overrides/"+filepath.ToSlash(rel), path)
	})
	return err
}

// verifyPackFile checks a downloaded pack file against the hashes the pack lists.
func verifyPackFile(path string, hashes map[string]string) error {
	wantSHA1, wantSHA512 := strings.ToLower(hashes["sha1"]), strings.ToLower(hashes["sha512"])
	if wantSHA1 == "" && wantSHA512 == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if wantSHA512 != "" {
		sum := sha512.Sum512(data)
		if hex.EncodeToString(sum[:]) != wantSHA512 {
			return errors.New("a downloaded file did not match the checksum in the modpack")
		}
	}
	if wantSHA1 != "" {
		sum := sha1.Sum(data)
		if hex.EncodeToString(sum[:]) != wantSHA1 {
			return errors.New("a downloaded file did not match the checksum in the modpack")
		}
	}
	return nil
}
