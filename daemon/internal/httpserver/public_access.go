package httpserver

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

const (
	playitCLIReleaseURL    = "https://api.github.com/repos/playit-cloud/playit-agent/releases/tags/v0.17.1"
	playitCLIVersion       = "0.17.1"
	maxPlayitDownloadBytes = 64 * 1024 * 1024
)

type playitStatus struct {
	Installed   bool                 `json:"installed"`
	Path        string               `json:"path"`
	Version     string               `json:"version"`
	Asset       string               `json:"asset"`
	Running     bool                 `json:"running"`
	PID         int                  `json:"pid"`
	ClaimURL    string               `json:"claimUrl"`
	StartedAt   string               `json:"startedAt"`
	Logs        []string             `json:"logs"`
	Error       string               `json:"error"`
	Claiming    bool                 `json:"claiming"`
	Tunnels     []playitTunnelStatus `json:"tunnels"`
	Platform    string               `json:"platform"`
	Deps        []playitDepStatus    `json:"deps"`
	DepsChecked bool                 `json:"depsChecked"`
	DepsInstall *playitJobState      `json:"depsInstall"`
	Build       *playitJobState      `json:"build"`
}

type playitTunnelStatus struct {
	Name          string `json:"name"`
	TunnelType    string `json:"tunnelType"`
	PublicAddress string `json:"publicAddress"`
	LocalIP       string `json:"localIp"`
	LocalPort     int    `json:"localPort"`
	Active        bool   `json:"active"`
}

type publicAccessConfigResponse struct {
	Config *store.PublicAccess `json:"config"`
}

func (h apiHandler) serverPublicAccess(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("id")
	if _, ok, err := h.store.GetServer(r.Context(), serverID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	config, ok, err := h.store.PublicAccess(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeJSON(w, http.StatusOK, publicAccessConfigResponse{})
		return
	}
	writeJSON(w, http.StatusOK, publicAccessConfigResponse{Config: &config})
}

func (h apiHandler) saveServerPublicAccess(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("id")
	server, ok, err := h.store.GetServer(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	var input store.PublicAccess
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid public access body")
		return
	}
	input.ServerID = serverID
	input.Provider = "Playit"
	if input.LocalHost == "" {
		input.LocalHost = "localhost"
	}
	input.LocalPort = server.Port
	if input.PublicAddress != "" && !validPublicJoinAddress(input.PublicAddress) {
		writeError(w, http.StatusBadRequest, "Enter the public Playit join address")
		return
	}
	config, err := h.store.SavePublicAccess(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, publicAccessConfigResponse{Config: &config})
}

func (h apiHandler) deleteServerPublicAccess(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("id")
	if _, ok, err := h.store.GetServer(r.Context(), serverID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	if err := h.store.DeletePublicAccess(r.Context(), serverID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

var publicJoinAddressPattern = regexp.MustCompile(`^[A-Za-z0-9.-]+\.[A-Za-z]{2,}(?::\d{1,5})?$`)

func validPublicJoinAddress(value string) bool {
	return publicJoinAddressPattern.MatchString(strings.TrimSpace(value))
}

type githubRelease struct {
	TagName string              `json:"tag_name"`
	Assets  []githubAssetRecord `json:"assets"`
}

type githubAssetRecord struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}
