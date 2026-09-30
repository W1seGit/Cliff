package httpserver

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

// Uploads are classified by what is inside the archive, not by its extension:
// a .zip can be a datapack, a bundle of mods, or a world save, and a .jar can
// be a Fabric/Forge/NeoForge mod or a Bukkit/Paper plugin.

type uploadKind string

const (
	uploadKindMod        uploadKind = "mod"
	uploadKindPlugin     uploadKind = "plugin"
	uploadKindDatapack   uploadKind = "datapack"
	uploadKindBundle     uploadKind = "bundle"
	uploadKindWorld      uploadKind = "world"
	uploadKindResources  uploadKind = "resourcepack"
	uploadKindUnknownJar uploadKind = "jar"
	uploadKindUnknown    uploadKind = "unknown"
)

type uploadResult struct {
	Name    string     `json:"name"`
	Kind    uploadKind `json:"kind"`
	Status  string     `json:"status"` // added, skipped
	Message string     `json:"message,omitempty"`
	Source  string     `json:"source,omitempty"` // archive a file was extracted from
}

type archiveInfo struct {
	Kind    uploadKind
	Loaders []string // fabric, quilt, forge, neoforge, bukkit
	Jars    []string // archive entries for a bundle
}

const maxBundleJars = 500

func normalizeEntryName(name string) string {
	return strings.TrimPrefix(strings.ReplaceAll(name, "\\", "/"), "./")
}

// classifyArchive looks inside a .jar or .zip and works out what it is.
func classifyArchive(archivePath string, fileName string) (archiveInfo, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return archiveInfo{Kind: uploadKindUnknown}, fmt.Errorf("%s is not a valid zip or jar archive", fileName)
	}
	defer reader.Close()

	names := map[string]bool{}
	var jars []string
	for _, entry := range reader.File {
		name := normalizeEntryName(entry.Name)
		if name == "" || entry.FileInfo().IsDir() {
			continue
		}
		names[name] = true
		dir, base := path.Split(name)
		if strings.HasSuffix(strings.ToLower(base), ".jar") {
			// Jars at the top level, or one folder deep under mods/ or plugins/.
			lowerDir := strings.ToLower(strings.TrimSuffix(dir, "/"))
			if dir == "" || lowerDir == "mods" || lowerDir == "plugins" {
				jars = append(jars, entry.Name)
			}
		}
	}

	// One optional wrapper folder ("MyPack/pack.mcmeta") is common in downloads.
	has := func(rel string) bool {
		if names[rel] {
			return true
		}
		for name := range names {
			if strings.Count(name, "/") == strings.Count(rel, "/")+1 && strings.HasSuffix(name, "/"+rel) {
				return true
			}
		}
		return false
	}
	hasPrefixed := func(prefix string) bool {
		for name := range names {
			if strings.HasPrefix(name, prefix) || strings.Contains(name, "/"+prefix) {
				return true
			}
		}
		return false
	}

	var loaders []string
	if names["fabric.mod.json"] {
		loaders = append(loaders, "fabric")
	}
	if names["quilt.mod.json"] {
		loaders = append(loaders, "quilt")
	}
	if names["META-INF/neoforge.mods.toml"] {
		loaders = append(loaders, "neoforge")
	}
	if names["META-INF/mods.toml"] {
		loaders = append(loaders, "forge", "neoforge")
	}
	if names["mcmod.info"] {
		loaders = append(loaders, "forge")
	}
	if len(loaders) > 0 {
		return archiveInfo{Kind: uploadKindMod, Loaders: loaders}, nil
	}
	if names["plugin.yml"] || names["paper-plugin.yml"] || names["bungee.yml"] {
		return archiveInfo{Kind: uploadKindPlugin, Loaders: []string{"bukkit"}}, nil
	}
	if has("pack.mcmeta") {
		if hasPrefixed("data/") {
			return archiveInfo{Kind: uploadKindDatapack}, nil
		}
		if hasPrefixed("assets/") {
			return archiveInfo{Kind: uploadKindResources}, nil
		}
		return archiveInfo{Kind: uploadKindDatapack}, nil
	}
	if has("level.dat") {
		return archiveInfo{Kind: uploadKindWorld}, nil
	}
	if len(jars) > 0 && !strings.HasSuffix(strings.ToLower(fileName), ".jar") {
		return archiveInfo{Kind: uploadKindBundle, Jars: jars}, nil
	}
	if strings.HasSuffix(strings.ToLower(fileName), ".jar") {
		return archiveInfo{Kind: uploadKindUnknownJar}, nil
	}
	return archiveInfo{Kind: uploadKindUnknown}, nil
}

// serverAcceptsMod reports whether a mod built for the given loaders runs on
// this server type, and why not when it does not.
func serverAcceptsMod(serverType string, loaders []string) (bool, string) {
	if serverTypeNeedsPlugins(serverType) {
		return false, fmt.Sprintf("This is a mod, but %s servers load plugins, not mods.", serverType)
	}
	if serverType == "vanilla" {
		return false, "Vanilla servers do not load mods."
	}
	for _, loader := range loaders {
		if loader == serverType || (loader == "quilt" && serverType == "fabric") {
			return true, ""
		}
	}
	return false, fmt.Sprintf("This mod is built for %s, but this server runs %s.", strings.Join(loaders, "/"), serverType)
}

func serverAcceptsPlugin(serverType string) (bool, string) {
	if serverTypeNeedsPlugins(serverType) {
		return true, ""
	}
	return false, fmt.Sprintf("This is a plugin, but %s servers load mods, not plugins.", serverType)
}

type uploadSession struct {
	h           apiHandler
	ctx         context.Context
	server      store.Server
	worldName   string
}

func (u *uploadSession) activeWorld() string {
	if strings.TrimSpace(u.worldName) != "" {
		return u.worldName
	}
	return stringProperty(readPropertiesRaw(filepath.Join(u.server.Path, "server.properties")), "level-name", "world")
}

// handlePart saves one uploaded multipart file and returns one result per
// thing it contained (a bundle produces several).
func (u *uploadSession) handlePart(part *multipart.Part) []uploadResult {
	name := safeBaseName(part.FileName())
	if name == "" {
		return []uploadResult{{Name: "(unnamed)", Kind: uploadKindUnknown, Status: "skipped", Message: "The upload had no file name."}}
	}
	temp, err := os.CreateTemp("", "cliff-upload-*")
	if err != nil {
		return []uploadResult{{Name: name, Kind: uploadKindUnknown, Status: "skipped", Message: err.Error()}}
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := io.Copy(temp, part); err != nil {
		temp.Close()
		return []uploadResult{{Name: name, Kind: uploadKindUnknown, Status: "skipped", Message: "Upload was interrupted: " + err.Error()}}
	}
	if err := temp.Close(); err != nil {
		return []uploadResult{{Name: name, Kind: uploadKindUnknown, Status: "skipped", Message: err.Error()}}
	}
	return u.place(tempPath, name, "")
}

func (u *uploadSession) place(tempPath string, name string, source string) []uploadResult {
	lower := strings.ToLower(name)
	if !strings.HasSuffix(lower, ".jar") && !strings.HasSuffix(lower, ".zip") {
		return []uploadResult{{Name: name, Kind: uploadKindUnknown, Status: "skipped", Source: source, Message: "Only .jar and .zip files are supported."}}
	}
	info, err := classifyArchive(tempPath, name)
	if err != nil {
		return []uploadResult{{Name: name, Kind: uploadKindUnknown, Status: "skipped", Source: source, Message: err.Error()}}
	}
	skip := func(message string) []uploadResult {
		return []uploadResult{{Name: name, Kind: info.Kind, Status: "skipped", Source: source, Message: message}}
	}

	switch info.Kind {
	case uploadKindMod:
		if ok, why := serverAcceptsMod(u.server.Type, info.Loaders); !ok {
			return skip(why)
		}
		return u.installJar(tempPath, name, info.Kind, source)
	case uploadKindPlugin:
		if ok, why := serverAcceptsPlugin(u.server.Type); !ok {
			return skip(why)
		}
		return u.installJar(tempPath, name, info.Kind, source)
	case uploadKindUnknownJar:
		// A jar with no recognizable descriptor: trust the server type.
		if u.server.Type == "vanilla" {
			return skip("Vanilla servers do not load mods or plugins.")
		}
		kind := uploadKindMod
		if serverTypeNeedsPlugins(u.server.Type) {
			kind = uploadKindPlugin
		}
		return u.installJar(tempPath, name, kind, source)
	case uploadKindDatapack:
		if !strings.HasSuffix(lower, ".zip") {
			return skip("Datapacks must be .zip files.")
		}
		file, err := os.Open(tempPath)
		if err != nil {
			return skip(err.Error())
		}
		defer file.Close()
		world := u.activeWorld()
		if err := writeDatapack(u.server, world, name, file); err != nil {
			return skip(err.Error())
		}
		return []uploadResult{{Name: name, Kind: uploadKindDatapack, Status: "added", Source: source, Message: "Added to world " + world}}
	case uploadKindBundle:
		return u.installBundle(tempPath, name, info.Jars)
	case uploadKindWorld:
		return skip("This looks like a world save. Use Worlds, then Import world.")
	case uploadKindResources:
		return skip("This is a resource pack. Servers do not load resource packs from here.")
	default:
		return skip("Could not tell what this is: it has no mod, plugin or datapack descriptor.")
	}
}

func (u *uploadSession) installJar(tempPath string, name string, kind uploadKind, source string) []uploadResult {
	safeName, err := uniqueModFileName(u.server, name)
	if err != nil {
		return []uploadResult{{Name: name, Kind: kind, Status: "skipped", Source: source, Message: err.Error()}}
	}
	target := filepath.Join(u.server.Path, modsActiveDir(u.server), safeName)
	file, err := os.Open(tempPath)
	if err != nil {
		return []uploadResult{{Name: name, Kind: kind, Status: "skipped", Source: source, Message: err.Error()}}
	}
	defer file.Close()
	if err := writeUploadedFile(file, target); err != nil {
		return []uploadResult{{Name: name, Kind: kind, Status: "skipped", Source: source, Message: err.Error()}}
	}
	message := ""
	if safeName != name {
		message = "Renamed to " + safeName + " because a file with that name already exists."
	}
	return []uploadResult{{Name: safeName, Kind: kind, Status: "added", Source: source, Message: message}}
}

// installBundle extracts the jars from a zip of mods (or plugins) and places
// each one through the same checks as a directly uploaded jar.
func (u *uploadSession) installBundle(zipPath string, zipName string, entries []string) []uploadResult {
	if len(entries) > maxBundleJars {
		return []uploadResult{{Name: zipName, Kind: uploadKindBundle, Status: "skipped", Message: fmt.Sprintf("This archive has more than %d jars.", maxBundleJars)}}
	}
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return []uploadResult{{Name: zipName, Kind: uploadKindBundle, Status: "skipped", Message: err.Error()}}
	}
	defer reader.Close()

	wanted := map[string]bool{}
	for _, entry := range entries {
		wanted[entry] = true
	}
	var results []uploadResult
	for _, entry := range reader.File {
		if !wanted[entry.Name] {
			continue
		}
		jarName := safeBaseName(path.Base(normalizeEntryName(entry.Name)))
		if jarName == "" {
			continue
		}
		extracted, err := extractZipEntry(entry)
		if err != nil {
			results = append(results, uploadResult{Name: jarName, Kind: uploadKindUnknownJar, Status: "skipped", Source: zipName, Message: err.Error()})
			continue
		}
		results = append(results, u.place(extracted, jarName, zipName)...)
		os.Remove(extracted)
	}
	if len(results) == 0 {
		results = append(results, uploadResult{Name: zipName, Kind: uploadKindBundle, Status: "skipped", Message: "No usable jars were found in this archive."})
	}
	return results
}

func extractZipEntry(entry *zip.File) (string, error) {
	source, err := entry.Open()
	if err != nil {
		return "", err
	}
	defer source.Close()
	temp, err := os.CreateTemp("", "cliff-extract-*")
	if err != nil {
		return "", err
	}
	// Cap what a single entry may expand to, to blunt zip bombs.
	const maxEntryBytes = 512 << 20
	written, err := io.Copy(temp, io.LimitReader(source, maxEntryBytes+1))
	closeErr := temp.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil && written > maxEntryBytes {
		err = errors.New("archive entry is too large")
	}
	if err != nil {
		os.Remove(temp.Name())
		return "", err
	}
	return temp.Name(), nil
}
