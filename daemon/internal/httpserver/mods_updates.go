package httpserver

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

// Mod statuses reported by analyzeMods.
const (
	modStatusCurrent      = "current"      // already the newest file for the target version
	modStatusUpdate       = "update"       // a newer file exists for the target version
	modStatusIncompatible = "incompatible" // known to Modrinth, but nothing exists for the target version
	modStatusUnknown      = "unknown"      // not found on Modrinth (custom or other source)
)

type modrinthFileVersion struct {
	ID            string `json:"id"`
	ProjectID     string `json:"project_id"`
	Name          string `json:"name"`
	VersionNumber string `json:"version_number"`
	Files         []struct {
		Primary  bool              `json:"primary"`
		URL      string            `json:"url"`
		Filename string            `json:"filename"`
		Hashes   map[string]string `json:"hashes"`
	} `json:"files"`
}

// primaryFile picks the file to install from a version.
func (v modrinthFileVersion) primaryFile() (url string, filename string, ok bool) {
	for _, file := range v.Files {
		if file.Primary {
			return file.URL, file.Filename, true
		}
	}
	if len(v.Files) > 0 {
		return v.Files[0].URL, v.Files[0].Filename, true
	}
	return "", "", false
}

func (v modrinthFileVersion) hasHash(hash string) bool {
	for _, file := range v.Files {
		if strings.EqualFold(file.Hashes["sha1"], hash) {
			return true
		}
	}
	return false
}

// modUpdateInfo is what the dashboard shows for one installed mod or plugin.
type modUpdateInfo struct {
	FileName            string `json:"fileName"`
	Enabled             bool   `json:"enabled"`
	Status              string `json:"status"`
	Title               string `json:"title"`
	IconURL             string `json:"iconUrl,omitempty"`
	ProjectID           string `json:"projectId,omitempty"`
	CurrentVersion      string `json:"currentVersion,omitempty"`
	LatestVersionID     string `json:"latestVersionId,omitempty"`
	LatestVersionNumber string `json:"latestVersionNumber,omitempty"`
	LatestFileName      string `json:"latestFileName,omitempty"`

	downloadURL string
	project     modrinthProject
}

// modrinthLoadersFor lists the Modrinth loader tags whose files can run on a
// server type. Plugin servers accept the plugins of the platforms they are
// built from.
func modrinthLoadersFor(serverType string) []string {
	switch serverType {
	case "paper":
		return []string{"paper", "spigot", "bukkit"}
	case "purpur":
		return []string{"purpur", "paper", "spigot", "bukkit"}
	case "folia":
		return []string{"folia"}
	case "fabric", "forge", "neoforge":
		return []string{serverType}
	}
	return nil
}

func sha1File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha1.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// modrinthPostJSON posts a JSON body to the Modrinth API and decodes the reply.
func modrinthPostJSON(r *http.Request, requestURL string, body any, target any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	var lastErr error
	for _, client := range []*http.Client{externalHTTPClient, externalIPv4HTTPClient} {
		request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, requestURL, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		request.Header.Set("User-Agent", "cliff/0.1.0")
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			lastErr = err
			if r.Context().Err() != nil {
				return err
			}
			continue
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return errors.New(requestURL + " returned " + strconv.Itoa(response.StatusCode))
		}
		return decodeBoundedJSON(response.Body, target)
	}
	return lastErr
}

// analyzeMods compares every installed mod or plugin with the newest Modrinth
// file for targetVersion. It identifies mods by file hash, so mods installed
// by hand are found too.
func (h apiHandler) analyzeMods(r *http.Request, server store.Server, targetVersion string) ([]modUpdateInfo, error) {
	mods := listServerMods(server)
	infos := make([]modUpdateInfo, 0, len(mods))
	hashes := make([]string, 0, len(mods))
	hashByName := map[string]string{}
	for _, mod := range mods {
		info := modUpdateInfo{FileName: mod.FileName, Enabled: mod.Enabled, Status: modStatusUnknown, Title: mod.FileName}
		if mod.Metadata != nil {
			info.Title = firstNonEmpty(mod.Metadata.Title, mod.FileName)
			info.IconURL = mod.Metadata.IconURL
			info.CurrentVersion = firstNonEmpty(mod.Metadata.VersionNumber, mod.Metadata.VersionName)
		}
		if hash, err := sha1File(mod.Path); err == nil {
			hashByName[mod.FileName] = hash
			hashes = append(hashes, hash)
		}
		infos = append(infos, info)
	}
	if len(hashes) == 0 {
		return infos, nil
	}
	loaders := modrinthLoadersFor(server.Type)

	// Which of the installed files does Modrinth know?
	known := map[string]modrinthFileVersion{}
	if err := modrinthPostJSON(r, "https://api.modrinth.com/v2/version_files", map[string]any{"hashes": hashes, "algorithm": "sha1"}, &known); err != nil {
		return nil, err
	}
	// And what is the newest file for the target version?
	latest := map[string]modrinthFileVersion{}
	if len(known) > 0 {
		knownHashes := make([]string, 0, len(known))
		for hash := range known {
			knownHashes = append(knownHashes, hash)
		}
		sort.Strings(knownHashes)
		body := map[string]any{"hashes": knownHashes, "algorithm": "sha1", "game_versions": []string{targetVersion}}
		if len(loaders) > 0 {
			body["loaders"] = loaders
		}
		if err := modrinthPostJSON(r, "https://api.modrinth.com/v2/version_files/update", body, &latest); err != nil {
			return nil, err
		}
	}

	projectIDs := map[string]bool{}
	for _, version := range known {
		projectIDs[version.ProjectID] = true
	}
	projects := h.modrinthProjects(r, projectIDs)

	for index := range infos {
		info := &infos[index]
		hash := hashByName[info.FileName]
		current, isKnown := known[hash]
		if !isKnown {
			continue
		}
		info.ProjectID = current.ProjectID
		info.project = projects[current.ProjectID]
		if info.project.Title != "" && (info.Title == info.FileName || info.Title == "") {
			info.Title = info.project.Title
		}
		if info.IconURL == "" {
			info.IconURL = info.project.IconURL
		}
		if info.CurrentVersion == "" {
			info.CurrentVersion = firstNonEmpty(current.VersionNumber, current.Name)
		}
		newest, hasNewest := latest[hash]
		switch {
		case !hasNewest:
			// Nothing for the target version. On the server's own version that
			// only means no newer file passed the filters.
			if targetVersion == server.MinecraftVersion {
				info.Status = modStatusCurrent
			} else {
				info.Status = modStatusIncompatible
			}
		case newest.hasHash(hash):
			info.Status = modStatusCurrent
		default:
			downloadURL, fileName, ok := newest.primaryFile()
			if !ok {
				info.Status = modStatusIncompatible
				continue
			}
			info.Status = modStatusUpdate
			info.LatestVersionID = newest.ID
			info.LatestVersionNumber = firstNonEmpty(newest.VersionNumber, newest.Name)
			info.LatestFileName = fileName
			info.downloadURL = downloadURL
		}
	}
	return infos, nil
}

// modrinthProjects fetches project details for a set of ids in one request.
func (h apiHandler) modrinthProjects(r *http.Request, ids map[string]bool) map[string]modrinthProject {
	projects := map[string]modrinthProject{}
	if len(ids) == 0 {
		return projects
	}
	list := make([]string, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	sort.Strings(list)
	encoded, _ := json.Marshal(list)
	var found []modrinthProject
	if err := fetchJSON(r, "https://api.modrinth.com/v2/projects?ids="+url.QueryEscape(string(encoded)), &found); err != nil {
		return projects // titles are a nicety; the check still works without them
	}
	for _, project := range found {
		projects[project.ID] = project
	}
	return projects
}

// modUpdatesSummary is the reply to the check-updates action.
func summarizeModUpdates(infos []modUpdateInfo) map[string]any {
	counts := map[string]int{}
	for _, info := range infos {
		counts[info.Status]++
	}
	return map[string]any{"mods": infos, "counts": counts}
}

func (h apiHandler) checkModUpdates(w http.ResponseWriter, r *http.Request, server store.Server) {
	infos, err := h.analyzeMods(r, server, server.MinecraftVersion)
	if err != nil {
		h.writeModSearchError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summarizeModUpdates(infos))
}

// updateMods downloads the newer files for the named mods (all outdated mods
// when none are named), after taking a snapshot so the change can be undone.
func (h apiHandler) updateMods(w http.ResponseWriter, r *http.Request, server store.Server, fileNames []string) {
	if h.process.IsRunning(server.ID) {
		writeError(w, http.StatusConflict, "Stop the server before updating mods")
		return
	}
	infos, err := h.analyzeMods(r, server, server.MinecraftVersion)
	if err != nil {
		h.writeModSearchError(w, err)
		return
	}
	wanted := map[string]bool{}
	for _, name := range fileNames {
		wanted[safeBaseName(name)] = true
	}
	pending := []modUpdateInfo{}
	for _, info := range infos {
		if info.Status == modStatusUpdate && (len(wanted) == 0 || wanted[info.FileName]) {
			pending = append(pending, info)
		}
	}
	if len(pending) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "updated": []string{}, "failed": []string{}})
		return
	}
	if _, err := h.createBackup(r.Context(), server, "pre-mod-update snapshot"); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not take a safety snapshot first: "+err.Error())
		return
	}

	updated := []string{}
	failed := []string{}
	for _, info := range pending {
		if err := h.replaceMod(r, server, info); err != nil {
			failed = append(failed, info.Title+": "+err.Error())
			continue
		}
		updated = append(updated, info.Title)
	}
	if len(updated) > 0 {
		h.notifier.notify(notification{
			Event:      notifyModsUpdated,
			Level:      "success",
			Title:      "Updated " + strconv.Itoa(len(updated)) + " mods on " + server.Name,
			Message:    strings.Join(updated, ", "),
			ServerID:   server.ID,
			ServerName: server.Name,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": len(failed) == 0, "updated": updated, "failed": failed})
}

// replaceMod swaps one installed file for its newer version, keeping it
// disabled if it was disabled.
func (h apiHandler) replaceMod(r *http.Request, server store.Server, info modUpdateInfo) error {
	newName, err := h.downloadToMods(r, info.downloadURL, info.LatestFileName, server)
	if err != nil {
		return err
	}
	if err := deleteModFile(server, info.FileName, info.Enabled); err != nil {
		return err
	}
	if !info.Enabled {
		if err := moveMod(server, newName, false); err != nil {
			return err
		}
	}
	project := info.project
	return saveModMetadata(server, newName, modMetadata{
		Source:        "modrinth",
		ProjectID:     info.ProjectID,
		Slug:          project.Slug,
		Title:         info.Title,
		Summary:       project.Description,
		Description:   project.Body,
		IconURL:       info.IconURL,
		PageURL:       "https://modrinth.com/project/" + firstNonEmpty(project.Slug, info.ProjectID),
		VersionID:     info.LatestVersionID,
		VersionNumber: info.LatestVersionNumber,
		InstalledAt:   time.Now().UTC().Format(time.RFC3339),
	})
}
