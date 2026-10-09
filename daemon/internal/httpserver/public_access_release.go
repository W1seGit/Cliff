package httpserver

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func playitReleaseURL() string {
	return playitCLIReleaseURL
}

func playitInstallNeedsReplacement(status playitStatus) bool {
	if status.Version == "system" {
		return false
	}
	return status.Version != playitCLIVersion
}

func fetchPlayitRelease(r *http.Request, releaseURL string) (githubRelease, error) {
	var release githubRelease
	if err := fetchJSON(r, releaseURL, &release); err != nil {
		return githubRelease{}, err
	}
	if release.TagName == "" || len(release.Assets) == 0 {
		return githubRelease{}, errors.New("Playit latest release did not include downloadable assets")
	}
	return release, nil
}

func selectPlayitAsset(assets []githubAssetRecord, goos string, goarch string) (githubAssetRecord, error) {
	targets, err := playitAssetTargets(goos, goarch)
	if err != nil {
		return githubAssetRecord{}, err
	}
	byName := map[string]githubAssetRecord{}
	for _, asset := range assets {
		byName[strings.ToLower(asset.Name)] = asset
	}
	for _, target := range targets {
		if asset, ok := byName[strings.ToLower(target)]; ok && asset.BrowserDownloadURL != "" {
			return asset, nil
		}
	}
	return githubAssetRecord{}, fmt.Errorf("No Playit agent binary is available for %s/%s", goos, goarch)
}

func playitAssetTargets(goos string, goarch string) ([]string, error) {
	switch goos {
	case "windows":
		switch goarch {
		case "amd64":
			return []string{"playit-windows-x86_64-signed.exe", "playit-windows-x86_64.exe"}, nil
		case "386":
			return []string{"playit-windows-x86-signed.exe", "playit-windows-x86.exe"}, nil
		}
	case "linux":
		switch goarch {
		case "amd64":
			return []string{"playit-linux-amd64"}, nil
		case "arm64":
			return []string{"playit-linux-aarch64"}, nil
		case "arm":
			return []string{"playit-linux-armv7"}, nil
		case "386":
			return []string{"playit-linux-i686"}, nil
		}
	case "darwin":
		switch goarch {
		case "amd64":
			return []string{"playit-darwin-amd64", "playit-macos-amd64", "playit-macos-x86_64"}, nil
		case "arm64":
			return []string{"playit-darwin-aarch64", "playit-darwin-arm64", "playit-macos-aarch64", "playit-macos-arm64"}, nil
		}
	}
	return nil, fmt.Errorf("Playit managed install is not supported on %s/%s yet", goos, goarch)
}

func downloadPlayitAsset(r *http.Request, requestURL string, destination string) error {
	response, err := fetchResponse(r, requestURL)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Playit agent download failed: HTTP %d", response.StatusCode)
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	copyErr := copyBoundedDownload(output, response.Body, maxPlayitDownloadBytes)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func (h apiHandler) readPlayitStatus() playitStatus {
	var saved playitStatus
	_ = readJSONFile(h.playitMetadataPath(), &saved)
	path := h.playitAgentPath()
	if saved.Path == "" {
		saved.Path = path
	}
	saved.Installed = fileExists(path)
	if saved.Installed && playitInstallNeedsReplacement(saved) {
		saved.Installed = false
		saved.Error = "Installed Playit agent does not support the claim-link CLI and needs reinstall."
	}
	return saved
}

func (h apiHandler) playitAgentPath() string {
	name := "playit"
	if runtime.GOOS == "windows" {
		name = "playit.exe"
	}
	return filepath.Join(h.config.DataDir, "tools", "playit", name)
}

func (h apiHandler) playitMetadataPath() string {
	return filepath.Join(h.config.DataDir, "tools", "playit", "agent.json")
}
