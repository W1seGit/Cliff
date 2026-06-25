package httpserver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

const (
	playitLatestReleaseURL     = "https://api.github.com/repos/playit-cloud/playit-agent/releases/latest"
	playitWindowsCLIReleaseURL = "https://api.github.com/repos/playit-cloud/playit-agent/releases/tags/v0.17.1"
	maxPlayitDownloadBytes     = 64 * 1024 * 1024
)

type playitStatus struct {
	Installed bool                 `json:"installed"`
	Path      string               `json:"path"`
	Version   string               `json:"version"`
	Asset     string               `json:"asset"`
	Running   bool                 `json:"running"`
	PID       int                  `json:"pid"`
	ClaimURL  string               `json:"claimUrl"`
	StartedAt string               `json:"startedAt"`
	Logs      []string             `json:"logs"`
	Error     string               `json:"error"`
	Claiming  bool                 `json:"claiming"`
	Tunnels   []playitTunnelStatus `json:"tunnels"`
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

func (h apiHandler) playitAgentStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.currentPlayitStatus(r))
}

func (h apiHandler) installPlayitAgent(w http.ResponseWriter, r *http.Request) {
	status := h.readPlayitStatus()
	if status.Installed && !playitInstallNeedsReplacement(status) {
		writeJSON(w, http.StatusOK, h.enrichPlayitStatus(r, status))
		return
	}
	installed, err := h.ensurePlayitAgent(r)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.enrichPlayitStatus(r, installed))
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
		return playitStatus{}, err
	}
	asset, err := selectPlayitAsset(release.Assets, runtime.GOOS, runtime.GOARCH)
	if err != nil {
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

func playitReleaseURL() string {
	if runtime.GOOS == "windows" {
		return playitWindowsCLIReleaseURL
	}
	return playitLatestReleaseURL
}

func playitInstallNeedsReplacement(status playitStatus) bool {
	return runtime.GOOS == "windows" && status.Version != "0.17.1"
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

func (h apiHandler) detectPlayitTunnels(r *http.Request, status playitStatus) ([]playitTunnelStatus, error) {
	if !status.Installed || status.Path == "" {
		return nil, nil
	}
	secret, err := readPlayitManagedSecret(status.Path)
	if err != nil {
		return nil, nil
	}
	requestBody := bytes.NewBufferString(`{"tunnel_id":null,"agent_id":null}`)
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "https://api.playit.gg/tunnels/list", requestBody)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Agent-Key "+secret)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Cliff/PlayitTunnelDetection")

	client := &http.Client{Timeout: 8 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024))
	if err != nil {
		return nil, err
	}
	return parsePlayitTunnelList(body)
}

func readPlayitManagedSecret(agentPath string) (string, error) {
	raw, err := os.ReadFile(playitManagedSecretPath(agentPath))
	if err != nil {
		return "", err
	}
	secret := parsePlayitManagedSecret(string(raw))
	if secret == "" {
		return "", errors.New("Playit secret file is empty")
	}
	return secret, nil
}

func parsePlayitManagedSecret(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "secret_key") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}
			return strings.Trim(strings.TrimSpace(parts[1]), `"`)
		}
	}
	return strings.TrimSpace(raw)
}

func parsePlayitTunnelList(body []byte) ([]playitTunnelStatus, error) {
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Tunnels []struct {
				Name       *string `json:"name"`
				TunnelType string  `json:"tunnel_type"`
				Active     bool    `json:"active"`
				Alloc      struct {
					Status string `json:"status"`
					Data   struct {
						IPHostname     string  `json:"ip_hostname"`
						AssignedDomain string  `json:"assigned_domain"`
						AssignedSRV    *string `json:"assigned_srv"`
						PortStart      int     `json:"port_start"`
					} `json:"data"`
				} `json:"alloc"`
				Origin struct {
					Type string `json:"type"`
					Data struct {
						LocalIP   string `json:"local_ip"`
						LocalPort *int   `json:"local_port"`
					} `json:"data"`
				} `json:"origin"`
			} `json:"tunnels"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if payload.Status != "success" {
		return nil, fmt.Errorf("Playit API status %q", payload.Status)
	}
	tunnels := make([]playitTunnelStatus, 0, len(payload.Data.Tunnels))
	for _, tunnel := range payload.Data.Tunnels {
		if tunnel.Origin.Type != "agent" || tunnel.Alloc.Status != "allocated" {
			continue
		}
		address := playitPublicAddress(tunnel.Alloc.Data.AssignedSRV, tunnel.Alloc.Data.AssignedDomain, tunnel.Alloc.Data.IPHostname, tunnel.Alloc.Data.PortStart)
		if address == "" {
			continue
		}
		localPort := 0
		if tunnel.Origin.Data.LocalPort != nil {
			localPort = *tunnel.Origin.Data.LocalPort
		}
		name := ""
		if tunnel.Name != nil {
			name = *tunnel.Name
		}
		tunnels = append(tunnels, playitTunnelStatus{
			Name:          name,
			TunnelType:    tunnel.TunnelType,
			PublicAddress: address,
			LocalIP:       tunnel.Origin.Data.LocalIP,
			LocalPort:     localPort,
			Active:        tunnel.Active,
		})
	}
	return tunnels, nil
}

func playitPublicAddress(assignedSRV *string, assignedDomain string, ipHostname string, portStart int) string {
	if assignedSRV != nil && strings.TrimSpace(*assignedSRV) != "" {
		return strings.TrimSpace(*assignedSRV)
	}
	host := strings.TrimSpace(assignedDomain)
	if host == "" {
		host = strings.TrimSpace(ipHostname)
	}
	if host == "" {
		return ""
	}
	if portStart > 0 {
		return fmt.Sprintf("%s:%d", host, portStart)
	}
	return host
}

const maxPlayitLogLines = 200

var playitClaimURLPattern = regexp.MustCompile(`https?://playit\.gg/claim/[A-Za-z0-9_-]+/?`)
var playitSecretPattern = regexp.MustCompile(`\b[A-Fa-f0-9]{32,}\b`)

type playitAgentManager struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	logs      []string
	claimURL  string
	claimCode string
	claiming  bool
	startedAt time.Time
	lastError string
}

func newPlayitAgentManager() *playitAgentManager {
	return &playitAgentManager{}
}

func (m *playitAgentManager) start(path string) error {
	if runtime.GOOS == "windows" {
		return m.prepareWindowsClaim(path)
	}
	m.mu.Lock()
	if m.cmd != nil && m.cmd.Process != nil {
		m.mu.Unlock()
		return nil
	}
	m.claimURL = ""
	m.lastError = ""
	m.logs = nil
	cmd := exec.Command(path)
	cmd.Dir = filepath.Dir(path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		m.mu.Unlock()
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		m.mu.Unlock()
		return err
	}
	m.cmd = cmd
	m.startedAt = time.Now().UTC()
	m.mu.Unlock()

	if err := cmd.Start(); err != nil {
		m.mu.Lock()
		m.cmd = nil
		m.startedAt = time.Time{}
		m.lastError = err.Error()
		m.mu.Unlock()
		return err
	}

	go m.scan(stdout)
	go m.scan(stderr)
	go m.wait(cmd)
	return nil
}

func (m *playitAgentManager) stop() error {
	m.mu.Lock()
	cmd := m.cmd
	m.cmd = nil
	m.claiming = false
	m.startedAt = time.Time{}
	m.lastError = ""
	m.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			m.pushLog("Playit agent stop failed: " + err.Error())
			return err
		}
		m.pushLog("Playit agent stopped.")
	}
	return nil
}

func (m *playitAgentManager) reset(path string) error {
	m.mu.Lock()
	cmd := m.cmd
	m.cmd = nil
	m.claimURL = ""
	m.claimCode = ""
	m.claiming = false
	m.startedAt = time.Time{}
	m.lastError = ""
	m.logs = nil
	m.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			m.pushLog("Playit agent stop failed: " + err.Error())
			return err
		}
	}
	if err := os.Remove(playitManagedSecretPath(path)); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.pushLog("Playit secret reset failed: " + err.Error())
		return err
	}
	m.pushLog("Playit setup reset. Start the agent to get a fresh claim link.")
	return nil
}

func (m *playitAgentManager) prepareWindowsClaim(path string) error {
	m.mu.Lock()
	if m.cmd != nil && m.cmd.Process != nil {
		m.mu.Unlock()
		return nil
	}
	m.claimURL = ""
	m.claimCode = ""
	m.claiming = false
	m.lastError = ""
	m.logs = nil
	m.startedAt = time.Now().UTC()
	m.mu.Unlock()

	return m.startWindowsAgent(path)
}

func (m *playitAgentManager) exchangeWindowsClaimAndStart(path string, code string) {
	m.pushLog("Waiting for Playit account approval")
	output, err := runPlayitCommand(path, "claim", "exchange", "--wait", "0", code)
	if err != nil {
		m.mu.Lock()
		m.claiming = false
		m.lastError = err.Error()
		m.mu.Unlock()
		m.pushLog("Playit claim exchange failed: " + err.Error())
		if strings.TrimSpace(output) != "" {
			m.pushLog("Playit claim exchange returned output; see Playit and try again.")
		}
		return
	}
	secret := parsePlayitSecret(output)
	if secret == "" {
		m.mu.Lock()
		m.claiming = false
		m.lastError = "Playit approved the agent but did not return a usable secret."
		m.mu.Unlock()
		m.pushLog("Playit approved the agent but did not return a usable secret.")
		return
	}
	secretPath, err := playitSecretPath(path)
	if err != nil {
		m.mu.Lock()
		m.claiming = false
		m.lastError = err.Error()
		m.mu.Unlock()
		m.pushLog("Playit secret path could not be found: " + err.Error())
		return
	}
	if err := writePlayitSecret(secretPath, secret); err != nil {
		m.mu.Lock()
		m.claiming = false
		m.lastError = err.Error()
		m.mu.Unlock()
		m.pushLog("Playit secret could not be saved: " + err.Error())
		return
	}
	m.mu.Lock()
	m.claiming = false
	m.mu.Unlock()
	m.pushLog("Playit account approved")
	if err := m.startWindowsAgent(path); err != nil {
		m.pushLog("Playit agent could not start: " + err.Error())
	}
}

func (m *playitAgentManager) startWindowsAgent(path string) error {
	m.mu.Lock()
	if m.cmd != nil && m.cmd.Process != nil {
		m.mu.Unlock()
		return nil
	}
	secretPath := playitManagedSecretPath(path)
	if err := os.MkdirAll(filepath.Dir(secretPath), 0o755); err != nil {
		m.mu.Unlock()
		return err
	}
	cmd := exec.Command(path, "--secret_path", secretPath, "-s", "start")
	cmd.Dir = filepath.Dir(path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		m.mu.Unlock()
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		m.mu.Unlock()
		return err
	}
	m.cmd = cmd
	m.startedAt = time.Now().UTC()
	m.lastError = ""
	m.mu.Unlock()

	if err := cmd.Start(); err != nil {
		m.mu.Lock()
		if m.cmd == cmd {
			m.cmd = nil
		}
		m.lastError = err.Error()
		m.mu.Unlock()
		return err
	}
	go m.scan(stdout)
	go m.scan(stderr)
	go m.wait(cmd)
	return nil
}

func (m *playitAgentManager) mergeStatus(status playitStatus) playitStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	status.Running = m.cmd != nil && m.cmd.Process != nil
	if status.Running {
		status.PID = m.cmd.Process.Pid
	}
	status.ClaimURL = m.claimURL
	status.Claiming = m.claiming
	if !m.startedAt.IsZero() {
		status.StartedAt = m.startedAt.Format(time.RFC3339)
	}
	status.Logs = append([]string(nil), m.logs...)
	status.Error = m.lastError
	return status
}

func (m *playitAgentManager) scan(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		m.pushLog(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		m.pushLog("Playit log stream failed: " + err.Error())
	}
}

func (m *playitAgentManager) wait(cmd *exec.Cmd) {
	err := cmd.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == cmd {
		m.cmd = nil
		if err != nil {
			m.lastError = err.Error()
			m.appendLogLocked("Playit agent exited: " + err.Error())
		} else {
			m.appendLogLocked("Playit agent exited")
		}
	}
}

func (m *playitAgentManager) pushLog(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if match := playitClaimURLPattern.FindString(line); match != "" {
		m.claimURL = match
	}
	if strings.Contains(line, "CodeNotFound") || strings.Contains(line, "CodeExpired") || strings.Contains(line, "UserRejected") {
		m.lastError = "Playit accepted the claim page action, but the local agent could not finish claiming. Reset Playit setup and try a fresh claim link."
	}
	m.appendLogLocked(line)
}

func (m *playitAgentManager) appendLogLocked(line string) {
	m.logs = append(m.logs, line)
	if len(m.logs) > maxPlayitLogLines {
		m.logs = m.logs[len(m.logs)-maxPlayitLogLines:]
	}
}

func runPlayitCommand(path string, args ...string) (string, error) {
	cmd := exec.Command(path, args...)
	cmd.Dir = filepath.Dir(path)
	output, err := cmd.CombinedOutput()
	return stripANSI(string(output)), err
}

func parsePlayitClaimCode(output string) string {
	matches := regexp.MustCompile(`\b[A-Fa-f0-9]{10}\b`).FindAllString(output, -1)
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1]
}

func parsePlayitSecret(output string) string {
	matches := playitSecretPattern.FindAllString(stripANSI(output), -1)
	if len(matches) == 0 {
		return ""
	}
	return strings.ToLower(matches[len(matches)-1])
}

func playitSecretPath(path string) (string, error) {
	output, err := runPlayitCommand(path, "secret-path")
	if err != nil {
		return "", err
	}
	secretPath := strings.TrimSpace(output)
	if secretPath == "" {
		return "", errors.New("Playit did not return a secret path")
	}
	return filepath.Clean(secretPath), nil
}

func playitManagedSecretPath(path string) string {
	return filepath.Join(filepath.Dir(path), "playit.toml")
}

func writePlayitSecret(secretPath string, secret string) error {
	if secret == "" {
		return errors.New("Playit secret is empty")
	}
	if err := os.MkdirAll(filepath.Dir(secretPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(secretPath, []byte(secret+"\n"), 0o600)
}

func stripANSI(input string) string {
	csiPattern := regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	oscPattern := regexp.MustCompile(`\x1b\][^\x07]*(\x07|\x1b\\)`)
	input = oscPattern.ReplaceAllString(input, "")
	return csiPattern.ReplaceAllString(input, "")
}
