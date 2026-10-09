package httpserver

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	javamanager "github.com/W1seGit/Cliff/daemon/internal/java"
	"github.com/W1seGit/Cliff/daemon/internal/store"
)

// upgradeInput is the body of POST /api/servers/{id}/upgrade.
type upgradeInput struct {
	Action           string `json:"action"` // "check" or "apply"
	MinecraftVersion string `json:"minecraftVersion"`
	LoaderVersion    string `json:"loaderVersion"`
	UpdateMods       bool   `json:"updateMods"`
	// DisableIncompatible moves mods that have no file for the new version into
	// the disabled folder so the server can still start.
	DisableIncompatible bool `json:"disableIncompatible"`
	AllowDowngrade      bool `json:"allowDowngrade"`
}

type upgradeReport struct {
	CurrentVersion string          `json:"currentVersion"`
	TargetVersion  string          `json:"targetVersion"`
	TargetLoader   string          `json:"targetLoader,omitempty"`
	Downgrade      bool            `json:"downgrade"`
	JavaMajor      int             `json:"javaMajor"`
	Warnings       []string        `json:"warnings"`
	Mods           []modUpdateInfo `json:"mods"`
	Counts         map[string]int  `json:"counts"`
}

// compareMinecraftVersions orders release versions such as "1.20.4" and
// "26.1". It returns 0 when either one is not a plain release (a snapshot).
func compareMinecraftVersions(a string, b string) int {
	parse := func(value string) ([]int, bool) {
		parts := strings.Split(strings.TrimSpace(value), ".")
		numbers := make([]int, 0, 3)
		for _, part := range parts {
			number, err := strconv.Atoi(part)
			if err != nil {
				return nil, false
			}
			numbers = append(numbers, number)
		}
		return numbers, len(numbers) >= 2
	}
	left, okLeft := parse(a)
	right, okRight := parse(b)
	if !okLeft || !okRight {
		return 0
	}
	for index := 0; index < 3; index++ {
		var l, r int
		if index < len(left) {
			l = left[index]
		}
		if index < len(right) {
			r = right[index]
		}
		if l != r {
			if l < r {
				return -1
			}
			return 1
		}
	}
	return 0
}

func (h apiHandler) upgradeServer(w http.ResponseWriter, r *http.Request) {
	server, ok, err := h.store.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	var input upgradeInput
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid upgrade body")
		return
	}
	input.MinecraftVersion = strings.TrimSpace(input.MinecraftVersion)
	input.LoaderVersion = strings.TrimSpace(input.LoaderVersion)
	if input.MinecraftVersion == "" {
		writeError(w, http.StatusBadRequest, "Choose the Minecraft version to upgrade to")
		return
	}
	if err := h.validateUpgradeTarget(r, server, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch input.Action {
	case "apply":
		h.applyUpgrade(w, r, server, input)
	default:
		report, err := h.upgradeReport(r, server, input)
		if err != nil {
			h.writeModSearchError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, report)
	}
}

// validateUpgradeTarget checks the version (and loader) exist before anything
// is changed, and fills in the loader version for loader servers.
func (h apiHandler) validateUpgradeTarget(r *http.Request, server store.Server, input *upgradeInput) error {
	metadata, err := h.getMinecraftMetadata(r, false)
	if err != nil {
		return err
	}
	if !metadataHasMinecraftVersion(metadata, input.MinecraftVersion) {
		return fmt.Errorf("Minecraft %s is not available in current release metadata", input.MinecraftVersion)
	}
	if !serverTypeNeedsLoader(server.Type) {
		input.LoaderVersion = ""
		return nil
	}
	loaders, err := h.getLoaderVersions(r, server.Type, input.MinecraftVersion, false)
	if err != nil {
		return err
	}
	if input.LoaderVersion == "" {
		// Default to the newest stable loader for the new version.
		for _, loader := range loaders {
			if loader.Stable {
				input.LoaderVersion = loader.Version
				break
			}
		}
		if input.LoaderVersion == "" && len(loaders) > 0 {
			input.LoaderVersion = loaders[0].Version
		}
	}
	if !loaderListContains(loaders, input.LoaderVersion) {
		return fmt.Errorf("%s is not available for Minecraft %s", server.Type, input.MinecraftVersion)
	}
	return nil
}

func (h apiHandler) upgradeReport(r *http.Request, server store.Server, input upgradeInput) (upgradeReport, error) {
	report := upgradeReport{
		CurrentVersion: server.MinecraftVersion,
		TargetVersion:  input.MinecraftVersion,
		TargetLoader:   input.LoaderVersion,
		Downgrade:      compareMinecraftVersions(input.MinecraftVersion, server.MinecraftVersion) < 0,
		JavaMajor:      javamanager.RequiredMajor(input.MinecraftVersion),
		Warnings:       []string{},
		Mods:           []modUpdateInfo{},
		Counts:         map[string]int{},
	}
	if report.Downgrade {
		report.Warnings = append(report.Warnings, "This is a downgrade. Worlds saved by a newer Minecraft version usually cannot be opened by an older one and may be corrupted.")
	}
	if input.MinecraftVersion == server.MinecraftVersion && input.LoaderVersion == server.LoaderVersion {
		report.Warnings = append(report.Warnings, "This is the version the server already uses; applying it reinstalls the server files.")
	}
	if javaPath := strings.TrimSpace(server.JavaPath); strings.HasPrefix(javaPath, "managed:") {
		if pinned, err := strconv.Atoi(strings.TrimPrefix(javaPath, "managed:")); err == nil && pinned < report.JavaMajor {
			report.Warnings = append(report.Warnings, fmt.Sprintf("The server is set to Java %d but %s needs Java %d. Switch the Java setting to automatic.", pinned, input.MinecraftVersion, report.JavaMajor))
		}
	} else if javaPath != "" && javaPath != "auto" && javaPath != "java" {
		report.Warnings = append(report.Warnings, fmt.Sprintf("The server uses a custom Java at %s. Minecraft %s needs Java %d.", javaPath, input.MinecraftVersion, report.JavaMajor))
	}
	if serverTypeNeedsLoader(server.Type) || serverTypeNeedsPlugins(server.Type) {
		mods, err := h.analyzeMods(r, server, input.MinecraftVersion)
		if err != nil {
			return report, err
		}
		report.Mods = mods
		for _, mod := range mods {
			report.Counts[mod.Status]++
		}
		if report.Counts[modStatusIncompatible] > 0 {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%d mods have no file for Minecraft %s and may stop the server from starting.", report.Counts[modStatusIncompatible], input.MinecraftVersion))
		}
		if report.Counts[modStatusUnknown] > 0 {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%d mods were not found on Modrinth, so Cliff cannot tell whether they work with %s.", report.Counts[modStatusUnknown], input.MinecraftVersion))
		}
	}
	return report, nil
}

func (h apiHandler) applyUpgrade(w http.ResponseWriter, r *http.Request, server store.Server, input upgradeInput) {
	if h.process.IsRunning(server.ID) {
		writeError(w, http.StatusConflict, "Stop the server before upgrading it")
		return
	}
	report, err := h.upgradeReport(r, server, input)
	if err != nil {
		h.writeModSearchError(w, err)
		return
	}
	if report.Downgrade && !input.AllowDowngrade {
		writeError(w, http.StatusBadRequest, "This would downgrade the server. Confirm the downgrade to continue.")
		return
	}
	snapshotID, err := h.createBackup(r.Context(), server, "pre-upgrade snapshot ("+server.MinecraftVersion+" to "+input.MinecraftVersion+")")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not take a safety snapshot first: "+err.Error())
		return
	}

	target := server
	target.MinecraftVersion = input.MinecraftVersion
	target.LoaderVersion = input.LoaderVersion
	if err := h.reprovisionServer(r, &target); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("The upgrade failed: %s. The server files may be half changed; restore the snapshot %q from Backups.", err.Error(), snapshotID))
		return
	}

	modResult := map[string][]string{"updated": {}, "disabled": {}, "failed": {}}
	for _, mod := range report.Mods {
		switch {
		case mod.Status == modStatusUpdate && input.UpdateMods:
			if err := h.replaceMod(r, target, mod); err != nil {
				modResult["failed"] = append(modResult["failed"], mod.Title+": "+err.Error())
			} else {
				modResult["updated"] = append(modResult["updated"], mod.Title)
			}
		case mod.Status == modStatusIncompatible && input.DisableIncompatible && mod.Enabled:
			if err := moveMod(target, mod.FileName, false); err != nil {
				modResult["failed"] = append(modResult["failed"], mod.Title+": "+err.Error())
			} else {
				modResult["disabled"] = append(modResult["disabled"], mod.Title)
			}
		}
	}

	// UpdateServer replaces the snapshot and restart settings with what it is
	// given, so it gets the whole record, not just the changed fields.
	saved, err := h.store.UpdateServer(r.Context(), server.ID, target)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.invalidateServerHealth(server.ID)
	h.notifier.notify(notification{
		Event:      notifyServerUpgraded,
		Level:      "success",
		Title:      server.Name + " upgraded to " + input.MinecraftVersion,
		Message:    "From " + server.MinecraftVersion + ". Safety snapshot: " + snapshotID,
		ServerID:   server.ID,
		ServerName: server.Name,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "server": saved, "snapshotId": snapshotID, "mods": modResult, "warnings": report.Warnings})
}

// reprovisionServer downloads the files for the server's (new) version in
// place. Provisioning writes fresh default eula.txt and server.properties
// files, so the existing ones are put back afterwards.
func (h apiHandler) reprovisionServer(r *http.Request, server *store.Server) error {
	keep := map[string][]byte{}
	for _, name := range []string{"eula.txt", "server.properties"} {
		if data, err := os.ReadFile(filepath.Join(server.Path, name)); err == nil {
			keep[name] = data
		}
	}
	_, provisionErr := h.provisionServer(r, server)
	for name, data := range keep {
		if err := os.WriteFile(filepath.Join(server.Path, name), data, 0o644); err != nil && provisionErr == nil {
			provisionErr = err
		}
	}
	return provisionErr
}
