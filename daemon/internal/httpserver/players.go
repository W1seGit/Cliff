package httpserver

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

type playerAccess struct {
	Ops           []operatorEntry     `json:"ops"`
	Whitelist     []namedAccessEntry  `json:"whitelist"`
	BannedPlayers []bannedPlayerEntry `json:"bannedPlayers"`
	BannedIPs     []bannedIPEntry     `json:"bannedIps"`
}

type operatorEntry struct {
	UUID                string `json:"uuid"`
	Name                string `json:"name"`
	Level               int    `json:"level"`
	BypassesPlayerLimit bool   `json:"bypassesPlayerLimit"`
}

type namedAccessEntry struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

type bannedPlayerEntry struct {
	UUID    string `json:"uuid"`
	Name    string `json:"name"`
	Created string `json:"created"`
	Source  string `json:"source"`
	Expires string `json:"expires"`
	Reason  string `json:"reason"`
}

type bannedIPEntry struct {
	IP      string `json:"ip"`
	Created string `json:"created"`
	Source  string `json:"source"`
	Expires string `json:"expires"`
	Reason  string `json:"reason"`
}

type playerSession struct {
	Name         string `json:"name"`
	IP           string `json:"ip"`
	LastJoinedAt string `json:"lastJoinedAt"`
}

var (
	loginLogPattern = regexp.MustCompile(`^\[(\d{2}):(\d{2}):(\d{2})\] \[[^\]]+/INFO\]: ([^\[\n]+)\[/([\d.]+):\d+\] logged in`)
	usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{3,16}$`)
)

const (
	maxPlayerSessionLogFiles = 24
	maxPlayerSessionLogBytes = 2 * 1024 * 1024
)

func (h apiHandler) players(w http.ResponseWriter, r *http.Request) {
	server, ok, err := h.store.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	if lookup := strings.TrimSpace(r.URL.Query().Get("lookup")); lookup != "" {
		h.lookupPlayer(w, r, lookup)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access":   readPlayerAccess(server.Path),
		"sessions": readPlayerSessions(server),
	})
}

func (h apiHandler) updatePlayers(w http.ResponseWriter, r *http.Request) {
	server, ok, err := h.store.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	var input struct {
		Kind    string           `json:"kind"`
		Action  string           `json:"action"`
		Entry   map[string]any   `json:"entry"`
		Entries []map[string]any `json:"entries"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid players body")
		return
	}
	if !validAccessKind(input.Kind) {
		writeError(w, http.StatusBadRequest, "Invalid access list")
		return
	}
	if input.Action == "remove-selected" {
		removed, err := removeSelectedAccessEntries(server.Path, input.Kind, input.Entries)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"access": readPlayerAccess(server.Path), "sessions": readPlayerSessions(server), "removed": removed})
		return
	}
	if input.Action == "add-selected" {
		for _, entry := range input.Entries {
			if err := updateAccessEntry(server.Path, input.Kind, "add", entry); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"access": readPlayerAccess(server.Path), "sessions": readPlayerSessions(server)})
		return
	}
	if input.Action != "add" && input.Action != "remove" {
		writeError(w, http.StatusBadRequest, "Invalid access action")
		return
	}
	if err := updateAccessEntry(server.Path, input.Kind, input.Action, input.Entry); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"access": readPlayerAccess(server.Path), "sessions": readPlayerSessions(server)})
}

func (h apiHandler) lookupPlayer(w http.ResponseWriter, r *http.Request, username string) {
	if !usernamePattern.MatchString(username) {
		writeError(w, http.StatusBadRequest, "Minecraft usernames must be 3-16 letters, numbers, or underscores")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	response, err := fetchResponse(r.WithContext(ctx), "https://api.mojang.com/users/profiles/minecraft/"+username)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent || response.StatusCode == http.StatusNotFound {
		writeError(w, http.StatusNotFound, "Minecraft player was not found")
		return
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		writeError(w, http.StatusBadGateway, "Mojang profile lookup failed")
		return
	}
	var profile struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(response.Body).Decode(&profile); err != nil || profile.ID == "" || profile.Name == "" {
		writeError(w, http.StatusBadGateway, "Mojang profile lookup returned incomplete data")
		return
	}
	uuid, err := dashedUUID(profile.ID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": profile.Name, "uuid": uuid})
}

func readPlayerAccess(serverPath string) playerAccess {
	return playerAccess{
		Ops:           readJSONArray[operatorEntry](filepath.Join(serverPath, "ops.json")),
		Whitelist:     readJSONArray[namedAccessEntry](filepath.Join(serverPath, "whitelist.json")),
		BannedPlayers: readJSONArray[bannedPlayerEntry](filepath.Join(serverPath, "banned-players.json")),
		BannedIPs:     readJSONArray[bannedIPEntry](filepath.Join(serverPath, "banned-ips.json")),
	}
}

func readPlayerSessions(server store.Server) []playerSession {
	logsPath := filepath.Join(server.Path, "logs")
	entries, err := os.ReadDir(logsPath)
	if err != nil {
		return []playerSession{}
	}
	type logEntry struct {
		name    string
		path    string
		modTime time.Time
	}
	logs := []logEntry{}
	for _, entry := range entries {
		if entry.IsDir() || !isSessionLogFile(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		logs = append(logs, logEntry{name: entry.Name(), path: filepath.Join(logsPath, entry.Name()), modTime: info.ModTime()})
	}
	sort.Slice(logs, func(left int, right int) bool {
		return logs[left].modTime.After(logs[right].modTime)
	})
	if len(logs) > maxPlayerSessionLogFiles {
		logs = logs[:maxPlayerSessionLogFiles]
	}
	latest := map[string]playerSession{}
	for _, entry := range logs {
		text, ok := readLogText(entry.path, entry.name)
		if !ok {
			continue
		}
		for _, session := range parseSessions(text, entry.modTime) {
			existing, found := latest[session.Name]
			if !found || session.LastJoinedAt > existing.LastJoinedAt {
				latest[session.Name] = session
			}
		}
	}
	sessions := []playerSession{}
	for _, session := range latest {
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(left int, right int) bool {
		return sessions[left].Name < sessions[right].Name
	})
	return sessions
}

func isSessionLogFile(name string) bool {
	return name == "latest.log" || strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".log.gz")
}

func parseSessions(text string, fileDate time.Time) []playerSession {
	sessions := []playerSession{}
	for _, line := range strings.Split(text, "\n") {
		match := loginLogPattern.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if match == nil {
			continue
		}
		date := time.Date(fileDate.Year(), fileDate.Month(), fileDate.Day(), atoiDefault(match[1], 0), atoiDefault(match[2], 0), atoiDefault(match[3], 0), 0, fileDate.Location()).UTC()
		sessions = append(sessions, playerSession{Name: strings.TrimSpace(match[4]), IP: match[5], LastJoinedAt: date.Format(time.RFC3339)})
	}
	return sessions
}

func readLogText(path string, name string) (string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()
	if strings.HasSuffix(name, ".gz") {
		reader, err := gzip.NewReader(file)
		if err != nil {
			return "", false
		}
		defer reader.Close()
		bytes, err := io.ReadAll(io.LimitReader(reader, maxPlayerSessionLogBytes))
		return string(bytes), err == nil
	}
	if name == "latest.log" || strings.HasSuffix(name, ".log") {
		bytes, err := io.ReadAll(io.LimitReader(file, maxPlayerSessionLogBytes))
		return string(bytes), err == nil
	}
	return "", false
}

func updateAccessEntry(serverPath string, kind string, action string, input map[string]any) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if kind == "ops" {
		current := readJSONArray[operatorEntry](accessFilePath(serverPath, kind))
		name := strings.TrimSpace(anyString(input["name"]))
		if name == "" {
			return errors.New("Player name is required")
		}
		next := []operatorEntry{}
		for _, entry := range current {
			if entry.Name != name {
				next = append(next, entry)
			}
		}
		if action == "add" {
			next = append(next, operatorEntry{UUID: anyString(input["uuid"]), Name: name, Level: anyIntDefault(input["level"], 4), BypassesPlayerLimit: anyBool(input["bypassesPlayerLimit"])})
		}
		return writeJSONArray(accessFilePath(serverPath, kind), next)
	}
	if kind == "whitelist" {
		current := readJSONArray[namedAccessEntry](accessFilePath(serverPath, kind))
		name := strings.TrimSpace(anyString(input["name"]))
		if name == "" {
			return errors.New("Player name is required")
		}
		next := []namedAccessEntry{}
		for _, entry := range current {
			if entry.Name != name {
				next = append(next, entry)
			}
		}
		if action == "add" {
			next = append(next, namedAccessEntry{UUID: anyString(input["uuid"]), Name: name})
		}
		return writeJSONArray(accessFilePath(serverPath, kind), next)
	}
	if kind == "bannedPlayers" {
		current := readJSONArray[bannedPlayerEntry](accessFilePath(serverPath, kind))
		name := strings.TrimSpace(anyString(input["name"]))
		if name == "" {
			return errors.New("Player name is required")
		}
		next := []bannedPlayerEntry{}
		for _, entry := range current {
			if entry.Name != name {
				next = append(next, entry)
			}
		}
		if action == "add" {
			next = append(next, bannedPlayerEntry{UUID: anyString(input["uuid"]), Name: name, Created: stringDefault(input["created"], now), Source: stringDefault(input["source"], "Cliff"), Expires: stringDefault(input["expires"], "forever"), Reason: stringDefault(input["reason"], "Banned by an operator.")})
		}
		return writeJSONArray(accessFilePath(serverPath, kind), next)
	}
	current := readJSONArray[bannedIPEntry](accessFilePath(serverPath, kind))
	ip := strings.TrimSpace(anyString(input["ip"]))
	if ip == "" {
		return errors.New("IP address is required")
	}
	next := []bannedIPEntry{}
	for _, entry := range current {
		if entry.IP != ip {
			next = append(next, entry)
		}
	}
	if action == "add" {
		next = append(next, bannedIPEntry{IP: ip, Created: stringDefault(input["created"], now), Source: stringDefault(input["source"], "Cliff"), Expires: stringDefault(input["expires"], "forever"), Reason: stringDefault(input["reason"], "Banned by an operator.")})
	}
	return writeJSONArray(accessFilePath(serverPath, kind), next)
}

func removeSelectedAccessEntries(serverPath string, kind string, entries []map[string]any) ([]string, error) {
	if len(entries) == 0 {
		return []string{}, nil
	}
	values := map[string]bool{}
	for _, entry := range entries {
		key := "name"
		if kind == "bannedIps" {
			key = "ip"
		}
		value := strings.TrimSpace(anyString(entry[key]))
		if value != "" {
			values[value] = true
		}
	}
	removed := []string{}
	for value := range values {
		removed = append(removed, value)
	}
	sort.Strings(removed)
	if kind == "ops" {
		current := readJSONArray[operatorEntry](accessFilePath(serverPath, kind))
		next := []operatorEntry{}
		for _, entry := range current {
			if !values[entry.Name] {
				next = append(next, entry)
			}
		}
		return removed, writeJSONArray(accessFilePath(serverPath, kind), next)
	}
	if kind == "whitelist" {
		current := readJSONArray[namedAccessEntry](accessFilePath(serverPath, kind))
		next := []namedAccessEntry{}
		for _, entry := range current {
			if !values[entry.Name] {
				next = append(next, entry)
			}
		}
		return removed, writeJSONArray(accessFilePath(serverPath, kind), next)
	}
	if kind == "bannedPlayers" {
		current := readJSONArray[bannedPlayerEntry](accessFilePath(serverPath, kind))
		next := []bannedPlayerEntry{}
		for _, entry := range current {
			if !values[entry.Name] {
				next = append(next, entry)
			}
		}
		return removed, writeJSONArray(accessFilePath(serverPath, kind), next)
	}
	current := readJSONArray[bannedIPEntry](accessFilePath(serverPath, kind))
	next := []bannedIPEntry{}
	for _, entry := range current {
		if !values[entry.IP] {
			next = append(next, entry)
		}
	}
	return removed, writeJSONArray(accessFilePath(serverPath, kind), next)
}

func accessFilePath(serverPath string, kind string) string {
	switch kind {
	case "ops":
		return filepath.Join(serverPath, "ops.json")
	case "whitelist":
		return filepath.Join(serverPath, "whitelist.json")
	case "bannedPlayers":
		return filepath.Join(serverPath, "banned-players.json")
	default:
		return filepath.Join(serverPath, "banned-ips.json")
	}
}

func validAccessKind(kind string) bool {
	return kind == "ops" || kind == "whitelist" || kind == "bannedPlayers" || kind == "bannedIps"
}

func readJSONArray[T any](path string) []T {
	data, err := os.ReadFile(path)
	if err != nil {
		return []T{}
	}
	var value []T
	if err := json.Unmarshal(data, &value); err != nil {
		return []T{}
	}
	return value
}

func writeJSONArray(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func dashedUUID(value string) (string, error) {
	clean := strings.ToLower(strings.ReplaceAll(value, "-", ""))
	if len(clean) != 32 {
		return "", errors.New("Mojang returned an invalid UUID")
	}
	for _, char := range clean {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return "", errors.New("Mojang returned an invalid UUID")
		}
	}
	return clean[:8] + "-" + clean[8:12] + "-" + clean[12:16] + "-" + clean[16:20] + "-" + clean[20:], nil
}

func atoiDefault(value string, fallback int) int {
	parsed := 0
	for _, char := range value {
		if char < '0' || char > '9' {
			return fallback
		}
		parsed = parsed*10 + int(char-'0')
	}
	return parsed
}

func anyString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return strings.TrimSpace(propertyString(value))
}

func anyIntDefault(value any, fallback int) int {
	if value == nil {
		return fallback
	}
	if number, ok := anyToInt(value); ok {
		return number
	}
	return fallback
}

func anyBool(value any) bool {
	result, _ := value.(bool)
	return result
}

func stringDefault(value any, fallback string) string {
	text := strings.TrimSpace(anyString(value))
	if text == "" {
		return fallback
	}
	return text
}
