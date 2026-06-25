package httpserver

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

type serverPropertiesPayload struct {
	Raw          map[string]string        `json:"raw"`
	EULAAccepted bool                     `json:"eulaAccepted"`
	Editable     serverPropertiesEditable `json:"editable"`
}

type serverPropertiesEditable struct {
	MOTD               string `json:"motd"`
	LevelName          string `json:"levelName"`
	LevelSeed          string `json:"levelSeed"`
	Gamemode           string `json:"gamemode"`
	Difficulty         string `json:"difficulty"`
	MaxPlayers         int    `json:"maxPlayers"`
	ServerPort         int    `json:"serverPort"`
	ViewDistance       int    `json:"viewDistance"`
	SimulationDistance int    `json:"simulationDistance"`
	OnlineMode         bool   `json:"onlineMode"`
	WhiteList          bool   `json:"whiteList"`
	PVP                bool   `json:"pvp"`
	EnableCommandBlock bool   `json:"enableCommandBlock"`
	AllowFlight        bool   `json:"allowFlight"`
}

func (h apiHandler) serverProperties(w http.ResponseWriter, r *http.Request) {
	server, ok, err := h.store.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	propertiesPath := filepath.Join(server.Path, "server.properties")
	if r.URL.Query().Get("download") == "1" {
		text, err := os.ReadFile(propertiesPath)
		if err != nil {
			writeError(w, http.StatusBadRequest, "server.properties does not exist")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+safeArchiveName(server.Name)+`-server.properties"`)
		_, _ = w.Write(text)
		return
	}
	writeJSON(w, http.StatusOK, readServerPropertiesPayload(server))
}

func (h apiHandler) updateServerProperties(w http.ResponseWriter, r *http.Request) {
	server, ok, err := h.store.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	var input map[string]any
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid properties body")
		return
	}
	editableInput := input
	if value, ok := input["editable"].(map[string]any); ok {
		editableInput = value
	}
	rawInput := map[string]any(nil)
	if value, ok := input["raw"].(map[string]any); ok {
		rawInput = value
	}
	if err := validateServerProperties(editableInput); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRawServerProperties(rawInput); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := writeServerPropertiesFile(server, editableInput, rawInput); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if value, ok := input["eulaAccepted"].(bool); ok {
		if err := os.WriteFile(filepath.Join(server.Path, "eula.txt"), []byte("eula="+strconv.FormatBool(value)+"\n"), 0o644); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	payload := readServerPropertiesPayload(server)
	if err := h.store.UpdateServerPort(r.Context(), server.ID, payload.Editable.ServerPort); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.invalidateServerHealth(server.ID)
	writeJSON(w, http.StatusOK, payload)
}

func readServerPropertiesPayload(server store.Server) serverPropertiesPayload {
	raw := readPropertiesRaw(filepath.Join(server.Path, "server.properties"))
	return serverPropertiesPayload{
		Raw:          raw,
		EULAAccepted: readEULAAccepted(filepath.Join(server.Path, "eula.txt")),
		Editable: serverPropertiesEditable{
			MOTD:               stringProperty(raw, "motd", "A Minecraft Server"),
			LevelName:          stringProperty(raw, "level-name", "world"),
			LevelSeed:          stringProperty(raw, "level-seed", ""),
			Gamemode:           stringProperty(raw, "gamemode", "survival"),
			Difficulty:         stringProperty(raw, "difficulty", "easy"),
			MaxPlayers:         intMapProperty(raw, "max-players", 20),
			ServerPort:         intMapProperty(raw, "server-port", 25565),
			ViewDistance:       intMapProperty(raw, "view-distance", 10),
			SimulationDistance: intMapProperty(raw, "simulation-distance", 10),
			OnlineMode:         boolMapProperty(raw, "online-mode", true),
			WhiteList:          boolMapProperty(raw, "white-list", false),
			PVP:                boolMapProperty(raw, "pvp", true),
			EnableCommandBlock: boolMapProperty(raw, "enable-command-block", false),
			AllowFlight:        boolMapProperty(raw, "allow-flight", false),
		},
	}
}

func readPropertiesRaw(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	raw := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		raw[key] = value
	}
	return raw
}

func writeServerPropertiesFile(server store.Server, input map[string]any, rawInput map[string]any) error {
	current := readPropertiesRaw(filepath.Join(server.Path, "server.properties"))
	for key, value := range rawInput {
		current[key] = propertyString(value)
	}
	keyMap := map[string]string{
		"motd":               "motd",
		"levelName":          "level-name",
		"levelSeed":          "level-seed",
		"gamemode":           "gamemode",
		"difficulty":         "difficulty",
		"maxPlayers":         "max-players",
		"serverPort":         "server-port",
		"viewDistance":       "view-distance",
		"simulationDistance": "simulation-distance",
		"onlineMode":         "online-mode",
		"whiteList":          "white-list",
		"pvp":                "pvp",
		"enableCommandBlock": "enable-command-block",
		"allowFlight":        "allow-flight",
	}
	for field, property := range keyMap {
		if value, ok := input[field]; ok {
			current[property] = propertyString(value)
		}
	}

	preferredOrder := []string{"motd", "level-name", "level-seed", "gamemode", "difficulty", "max-players", "server-port", "view-distance", "simulation-distance", "online-mode", "white-list", "pvp", "enable-command-block", "allow-flight"}
	seen := map[string]bool{}
	lines := []string{"#Minecraft server properties managed by Cliff"}
	for _, key := range preferredOrder {
		if value, ok := current[key]; ok {
			lines = append(lines, key+"="+value)
			seen[key] = true
		}
	}
	keys := []string{}
	for key := range current {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		lines = append(lines, key+"="+current[key])
	}
	if err := os.MkdirAll(server.Path, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(server.Path, "server.properties"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func validateServerProperties(input map[string]any) error {
	if err := validateIntegerRange(input["maxPlayers"], "Max players", 1, 1000); err != nil {
		return err
	}
	if err := validateIntegerRange(input["serverPort"], "Server port", 1, 65535); err != nil {
		return err
	}
	if err := validateIntegerRange(input["viewDistance"], "View distance", 2, 32); err != nil {
		return err
	}
	if err := validateIntegerRange(input["simulationDistance"], "Simulation distance", 2, 32); err != nil {
		return err
	}
	if value, ok := input["levelName"]; ok && strings.TrimSpace(propertyString(value)) == "" {
		return httpError("World folder is required")
	}
	return nil
}

func validateRawServerProperties(input map[string]any) error {
	for key, value := range input {
		if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "\r\n=") {
			return httpError("Raw property keys cannot be blank or contain line breaks or equals signs")
		}
		if strings.ContainsAny(propertyString(value), "\r\n") {
			return httpError("Raw property values cannot contain line breaks")
		}
	}
	return nil
}

func validateIntegerRange(value any, label string, min int, max int) error {
	if value == nil {
		return nil
	}
	number, ok := anyToInt(value)
	if !ok || number < min || number > max {
		return httpError(label + " must be between " + strconv.Itoa(min) + " and " + strconv.Itoa(max))
	}
	return nil
}

type httpError string

func (e httpError) Error() string {
	return string(e)
}

func stringProperty(raw map[string]string, key string, fallback string) string {
	if value, ok := raw[key]; ok {
		return value
	}
	return fallback
}

func intMapProperty(raw map[string]string, key string, fallback int) int {
	value, err := strconv.Atoi(raw[key])
	if err != nil {
		return fallback
	}
	return value
}

func boolMapProperty(raw map[string]string, key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(raw[key]))
	if value == "true" {
		return true
	}
	if value == "false" {
		return false
	}
	return fallback
}

func anyToInt(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		if typed != float64(int(typed)) {
			return 0, false
		}
		return int(typed), true
	case int:
		return typed, true
	default:
		return 0, false
	}
}

func propertyString(value any) string {
	switch typed := value.(type) {
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		if typed == float64(int(typed)) {
			return strconv.Itoa(int(typed))
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case string:
		return typed
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}
