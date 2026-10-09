package httpserver

import (
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func (h apiHandler) playitAgentStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.currentPlayitStatus(r))
}

func (h apiHandler) installPlayitAgent(w http.ResponseWriter, r *http.Request) {
	status := h.readPlayitStatus()
	if status.Installed && !playitInstallNeedsReplacement(status) {
		writeJSON(w, http.StatusOK, h.enrichPlayitStatus(r, status))
		return
	}
	// On macOS there is no prebuilt binary release, so we build from source.
	// The build is async (cargo compile takes minutes); kick it off and
	// return the current status immediately so the dashboard can poll.
	if isMacOSPlayitBuildSupported() {
		if h.playitBuild == nil {
			writeError(w, http.StatusInternalServerError, "Playit build manager is unavailable")
			return
		}
		agentPath := h.playitAgentPath()
		metadataPath := h.playitMetadataPath()
		onComplete := func(success bool) {
			if !success {
				return
			}
			if _, err := os.Stat(agentPath); err != nil {
				return
			}
			status := playitStatus{
				Installed: true,
				Path:      agentPath,
				Version:   playitCLIVersion,
				Asset:     "built-from-source",
			}
			_ = writeJSONFile(metadataPath, status)
		}
		if err := h.playitBuild.startBuild(h.playitBuildScriptDir(), filepath.Dir(agentPath), onComplete); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, h.currentPlayitStatus(r))
		return
	}
	installed, err := h.ensurePlayitAgent(r)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.enrichPlayitStatus(r, installed))
}

func (h apiHandler) checkPlayitDeps(w http.ResponseWriter, r *http.Request) {
	if !isMacOSPlayitBuildSupported() {
		writeError(w, http.StatusBadRequest, "Dependency checks are only available on macOS")
		return
	}
	if h.playitBuild == nil {
		writeError(w, http.StatusInternalServerError, "Playit build manager is unavailable")
		return
	}
	h.playitBuild.checkPlayitDeps()
	writeJSON(w, http.StatusOK, h.currentPlayitStatus(r))
}

func (h apiHandler) installPlayitDeps(w http.ResponseWriter, r *http.Request) {
	if !isMacOSPlayitBuildSupported() {
		writeError(w, http.StatusBadRequest, "Dependency install is only available on macOS")
		return
	}
	if h.playitBuild == nil {
		writeError(w, http.StatusInternalServerError, "Playit build manager is unavailable")
		return
	}
	if err := h.playitBuild.startDepsInstall(h.playitBuildScriptDir()); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.currentPlayitStatus(r))
}

func (h apiHandler) startPlayitAgent(w http.ResponseWriter, r *http.Request) {
	status := h.readPlayitStatus()
	if !status.Installed {
		writeError(w, http.StatusBadRequest, "Install the Playit agent first")
		return
	}
	if h.playit == nil {
		writeError(w, http.StatusInternalServerError, "Playit agent manager is unavailable")
		return
	}
	if err := h.playit.start(status.Path); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.enrichPlayitStatus(r, h.playit.mergeStatus(status)))
}

func (h apiHandler) stopPlayitAgent(w http.ResponseWriter, r *http.Request) {
	status := h.readPlayitStatus()
	if !status.Installed {
		writeError(w, http.StatusBadRequest, "Install the Playit agent first")
		return
	}
	if h.playit == nil {
		writeError(w, http.StatusInternalServerError, "Playit agent manager is unavailable")
		return
	}
	if err := h.playit.stop(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.enrichPlayitStatus(r, h.playit.mergeStatus(status)))
}

func (h apiHandler) resetPlayitAgent(w http.ResponseWriter, r *http.Request) {
	status := h.readPlayitStatus()
	if !status.Installed {
		writeError(w, http.StatusBadRequest, "Install the Playit agent first")
		return
	}
	if h.playit == nil {
		writeError(w, http.StatusInternalServerError, "Playit agent manager is unavailable")
		return
	}
	if err := h.playit.reset(status.Path); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.enrichPlayitStatus(r, h.playit.mergeStatus(status)))
}

func (h apiHandler) uninstallPlayitAgent(w http.ResponseWriter, r *http.Request) {
	status := h.readPlayitStatus()
	if !status.Installed {
		writeError(w, http.StatusBadRequest, "Install the Playit agent first")
		return
	}
	if h.playit == nil {
		writeError(w, http.StatusInternalServerError, "Playit agent manager is unavailable")
		return
	}
	if err := h.playit.reset(status.Path); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := os.Remove(status.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := os.Remove(h.playitMetadataPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	nextStatus := h.readPlayitStatus()
	writeJSON(w, http.StatusOK, h.enrichPlayitStatus(r, h.playit.mergeStatus(nextStatus)))
}

func (h apiHandler) currentPlayitStatus(r *http.Request) playitStatus {
	status := h.readPlayitStatus()
	if h.playit != nil {
		status = h.playit.mergeStatus(status)
	}
	if h.playitBuild != nil {
		status = h.playitBuild.mergeDepsState(status)
	}
	return h.enrichPlayitStatus(r, status)
}

func (h apiHandler) enrichPlayitStatus(r *http.Request, status playitStatus) playitStatus {
	tunnels, err := h.detectPlayitTunnels(r, status)
	if err != nil && status.Error == "" {
		status.Error = "Playit tunnel lookup failed: " + err.Error()
	}
	if len(tunnels) > 0 {
		status.Tunnels = tunnels
	}
	return status
}

func (h apiHandler) ensurePlayitAgent(r *http.Request) (playitStatus, error) {
	release, err := fetchPlayitRelease(r, playitReleaseURL())
	if err != nil {
		if systemStatus, systemErr := h.useSystemPlayitAgent(); systemErr == nil {
			return systemStatus, nil
		}
		return playitStatus{}, err
	}
	asset, err := selectPlayitAsset(release.Assets, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		if systemStatus, systemErr := h.useSystemPlayitAgent(); systemErr == nil {
			return systemStatus, nil
		}
		return playitStatus{}, err
	}
	destination := h.playitAgentPath()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return playitStatus{}, err
	}
	tempPath := destination + ".download"
	if err := downloadPlayitAsset(r, asset.BrowserDownloadURL, tempPath); err != nil {
		_ = os.Remove(tempPath)
		return playitStatus{}, err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tempPath, 0o755); err != nil {
			_ = os.Remove(tempPath)
			return playitStatus{}, err
		}
	}
	if err := os.Rename(tempPath, destination); err != nil {
		_ = os.Remove(destination)
		if renameErr := os.Rename(tempPath, destination); renameErr != nil {
			_ = os.Remove(tempPath)
			return playitStatus{}, err
		}
	}
	status := playitStatus{
		Installed: true,
		Path:      destination,
		Version:   strings.TrimPrefix(release.TagName, "v"),
		Asset:     asset.Name,
	}
	_ = writeJSONFile(h.playitMetadataPath(), status)
	return status, nil
}

func (h apiHandler) useSystemPlayitAgent() (playitStatus, error) {
	for _, name := range []string{"playit-cli", "playit"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		status := playitStatus{
			Installed: true,
			Path:      path,
			Version:   "system",
			Asset:     "system:" + name,
		}
		_ = writeJSONFile(h.playitMetadataPath(), status)
		return status, nil
	}
	if runtime.GOOS == "darwin" {
		return playitStatus{}, errors.New("Playit does not publish a macOS binary release. Use the Install Playit Agent button to build it from source, or install the 'playit' command on PATH manually")
	}
	return playitStatus{}, errors.New("No compatible Playit agent binary was found for this platform")
}
