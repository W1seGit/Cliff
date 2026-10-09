package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

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
