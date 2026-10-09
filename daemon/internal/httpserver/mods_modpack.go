package httpserver

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func (h apiHandler) installModrinth(r *http.Request, projectID string, server store.Server, versionID string, includeDependencies bool, warnings []modDependencyWarning) ([]string, error) {
	return h.installModrinthProject(r, projectID, server, versionID, map[string]bool{}, includeDependencies, warnings)
}

func (h apiHandler) installModrinthModpack(r *http.Request, projectID string, server store.Server, versionID string) ([]string, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, errors.New("Modrinth project id is required")
	}
	options := modSearchOptions{Version: server.MinecraftVersion, ProjectType: "modpack"}
	project, version, downloadURL, _, err := h.resolveModrinthInstallTargetWith(r, projectID, server, versionID, options)
	if err != nil {
		return nil, err
	}
	response, err := fetchResponse(r, downloadURL)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, errors.New("Modpack download failed: " + strconv.Itoa(response.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxArtifactDownloadBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxArtifactDownloadBytes {
		return nil, errors.New("Modpack download is too large")
	}
	return h.applyModrinthModpack(r, server, data, project, version)
}

func (h apiHandler) applyModrinthModpack(r *http.Request, server store.Server, data []byte, project modrinthProject, version modrinthVersion) ([]string, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("Modpack file could not be read")
	}
	var index mrpackManifest
	indexFound := false
	for _, entry := range reader.File {
		if entry.Name != "modrinth.index.json" {
			continue
		}
		indexFound = true
		file, err := entry.Open()
		if err != nil {
			return nil, err
		}
		decodeErr := json.NewDecoder(file).Decode(&index)
		closeErr := file.Close()
		if decodeErr != nil {
			return nil, errors.New("Modpack index could not be read")
		}
		if closeErr != nil {
			return nil, closeErr
		}
		break
	}
	if !indexFound {
		return nil, errors.New("Modpack does not contain a Modrinth index")
	}
	installed := []string{}
	metadata := modMetadata{
		Source:        "modrinth-modpack",
		ProjectID:     project.ID,
		Slug:          project.Slug,
		Title:         project.Title,
		Summary:       project.Description,
		Description:   project.Body,
		IconURL:       project.IconURL,
		PageURL:       "https://modrinth.com/modpack/" + firstNonEmpty(project.Slug, project.ID),
		VersionID:     version.ID,
		VersionName:   version.Name,
		VersionNumber: version.VersionNumber,
		InstalledAt:   time.Now().UTC().Format(time.RFC3339),
	}
	for _, entry := range reader.File {
		name := filepath.ToSlash(entry.Name)
		prefix := ""
		if strings.HasPrefix(name, "server-overrides/") {
			prefix = "server-overrides/"
		} else if strings.HasPrefix(name, "overrides/") {
			prefix = "overrides/"
		}
		if prefix == "" || entry.FileInfo().IsDir() {
			continue
		}
		rel := strings.TrimPrefix(name, prefix)
		if rel == "" || strings.Contains(rel, "..") {
			continue
		}
		target, err := resolveInside(server.Path, filepath.FromSlash(rel))
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		src, err := entry.Open()
		if err != nil {
			return nil, err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			_ = src.Close()
			return nil, err
		}
		copyErr := copyBoundedDownload(out, src, maxArtifactDownloadBytes)
		closeOutErr := out.Close()
		closeSrcErr := src.Close()
		if copyErr != nil {
			_ = os.Remove(target)
			return nil, copyErr
		}
		if closeOutErr != nil {
			return nil, closeOutErr
		}
		if closeSrcErr != nil {
			return nil, closeSrcErr
		}
		if strings.HasPrefix(filepath.ToSlash(rel), "mods/") && strings.HasSuffix(strings.ToLower(rel), ".jar") {
			if err := saveModMetadata(server, filepath.Base(rel), metadata); err != nil {
				return nil, err
			}
		}
		installed = append(installed, rel)
	}
	for _, file := range index.Files {
		if strings.TrimSpace(file.Path) == "" || len(file.Downloads) == 0 {
			continue
		}
		if file.Env != nil && file.Env.Server == "unsupported" {
			continue // client-only file
		}
		rel := filepath.FromSlash(file.Path)
		if strings.Contains(filepath.ToSlash(rel), "..") {
			continue
		}
		target, err := resolveInside(server.Path, rel)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		if err := downloadFile(r, file.Downloads[0], target); err != nil {
			return nil, err
		}
		if err := verifyPackFile(target, file.Hashes); err != nil {
			_ = os.Remove(target)
			return nil, err
		}
		if strings.HasPrefix(filepath.ToSlash(file.Path), "mods/") && strings.HasSuffix(strings.ToLower(file.Path), ".jar") {
			if err := saveModMetadata(server, filepath.Base(file.Path), metadata); err != nil {
				return nil, err
			}
		}
		installed = append(installed, filepath.ToSlash(file.Path))
	}
	return installed, nil
}
