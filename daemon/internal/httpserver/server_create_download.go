package httpserver

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func (h apiHandler) vanillaServerDownload(r *http.Request, minecraftVersion string) (*struct {
	URL  string `json:"url"`
	SHA1 string `json:"sha1"`
	Size int64  `json:"size"`
}, error) {
	progressFrom(r).start("lookup")
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
	progressFrom(r).start("lookup")
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
	progressFrom(r).start("lookup")
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
	progressFrom(r).start("lookup")
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
	progressFrom(r).start("lookup")
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
	progress := progressFrom(r)
	progress.start("download")
	copyErr := copyBoundedDownload(output, &progressReader{reader: response.Body, progress: progress, total: response.ContentLength}, maxArtifactDownloadBytes)
	closeErr := output.Close()
	if copyErr != nil {
		_ = os.Remove(destination)
		return copyErr
	}
	return closeErr
}

// ensureNotInside refuses a copy whose destination lives inside its source.
// Importing a folder that contains the server storage would otherwise copy
// itself into itself until the disk fills.
func ensureNotInside(source string, target string) error {
	absSource, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(absSource, absTarget)
	if err != nil {
		return nil // different volumes cannot be nested
	}
	if relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return errors.New("That folder contains Cliff's own server storage. Choose the server's folder itself, not a parent folder.")
	}
	return nil
}

func copyDirectory(source string, target string) error {
	if err := ensureNotInside(source, target); err != nil {
		return err
	}
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
		return "auto"
	}
	return value
}
