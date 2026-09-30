package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// serverRef is a Minecraft server as the daemon reports it.
type serverRef struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Lifecycle string `json:"lifecycle"`
}

// callDaemon makes an authenticated request to the daemon's local-only
// endpoints, using the token file the daemon wrote at startup.
func callDaemon(method string, port int, dataDir string, path string, body any, out any, timeout time.Duration) error {
	token, err := os.ReadFile(tokenFilePath(dataDir))
	if err != nil || len(bytes.TrimSpace(token)) == 0 {
		return fmt.Errorf("the daemon's local token is not available")
	}
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(raw)
	}
	request, err := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), payload)
	if err != nil {
		return err
	}
	request.Header.Set("X-Cliff-Token", strings.TrimSpace(string(token)))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := (&http.Client{Timeout: timeout}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("the daemon answered HTTP %d", response.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(out)
}

// fetchRunningServers asks the daemon which servers an update would interrupt.
// A daemon that does not know the question (an older version) reports none.
func fetchRunningServers(port int, dataDir string) []serverRef {
	var payload struct {
		Servers []serverRef `json:"servers"`
	}
	if err := callDaemon(http.MethodGet, port, dataDir, "/api/internal/running-servers", nil, &payload, 5*time.Second); err != nil {
		return nil
	}
	return payload.Servers
}

// resumeServers starts the given servers again and reports which came back and
// which did not, each with the reason.
func resumeServers(port int, dataDir string, ids []string) (restarted []string, notRestarted []string) {
	if len(ids) == 0 {
		return nil, nil
	}
	var payload struct {
		Results []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"results"`
	}
	err := callDaemon(http.MethodPost, port, dataDir, "/api/internal/resume-servers", map[string]any{"serverIds": ids}, &payload, 3*time.Minute)
	if err != nil {
		for _, id := range ids {
			notRestarted = append(notRestarted, fmt.Sprintf("%s (%s)", id, err))
		}
		return nil, notRestarted
	}
	for _, result := range payload.Results {
		name := result.Name
		if name == "" {
			name = result.ID
		}
		if result.Status == "started" {
			restarted = append(restarted, name)
			continue
		}
		reason := result.Message
		if reason == "" {
			reason = result.Status
		}
		notRestarted = append(notRestarted, fmt.Sprintf("%s (%s)", name, reason))
	}
	return restarted, notRestarted
}

func serverNames(servers []serverRef) string {
	names := make([]string, 0, len(servers))
	for _, server := range servers {
		names = append(names, fmt.Sprintf("'%s'", server.Name))
	}
	return strings.Join(names, ", ")
}

func serverIDs(servers []serverRef) []string {
	ids := make([]string, 0, len(servers))
	for _, server := range servers {
		ids = append(ids, server.ID)
	}
	return ids
}

// resumeSummary words the outcome of starting servers again for the update result.
func resumeSummary(restarted []string, notRestarted []string) string {
	parts := []string{}
	if len(restarted) > 0 {
		parts = append(parts, "Started again: "+strings.Join(restarted, ", ")+".")
	}
	if len(notRestarted) > 0 {
		parts = append(parts, "Could not start again: "+strings.Join(notRestarted, "; ")+". Start it from the dashboard.")
	}
	return strings.Join(parts, " ")
}
