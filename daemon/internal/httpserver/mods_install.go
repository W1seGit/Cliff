package httpserver

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func (h apiHandler) installModrinthDependencies(r *http.Request, dependencies []modDependencyWarning, server store.Server) ([]string, error) {
	installed := []string{}
	seen := map[string]bool{}
	for _, dependency := range dependencies {
		files, err := h.installModrinthProject(r, dependency.ProjectID, server, dependency.VersionID, seen, true, nil)
		if err != nil {
			return installed, err
		}
		installed = append(installed, files...)
	}
	return installed, nil
}

func (h apiHandler) installModrinthProject(r *http.Request, projectID string, server store.Server, versionID string, seen map[string]bool, includeDependencies bool, warnings []modDependencyWarning) ([]string, error) {
	if seen[projectID] {
		return []string{}, nil
	}
	seen[projectID] = true
	project, version, downloadURL, fileName, err := h.resolveModrinthInstallTarget(r, projectID, server, versionID)
	if err != nil {
		return nil, err
	}
	installed := []string{}
	if includeDependencies {
		for _, dependency := range version.Dependencies {
			if dependency.DependencyType != "required" {
				continue
			}
			dependencyProjectID := dependency.ProjectID
			dependencyVersionID := ""
			if dependencyProjectID == "" && dependency.VersionID != "" {
				dependencyVersion, err := h.getModrinthVersion(r, dependency.VersionID)
				if err != nil {
					return nil, err
				}
				dependencyProjectID = dependencyVersion.ProjectID
				dependencyVersionID = dependency.VersionID
			}
			if dependencyProjectID == "" {
				continue
			}
			files, err := h.installModrinthProject(r, dependencyProjectID, server, dependencyVersionID, seen, true, nil)
			if err != nil {
				return nil, err
			}
			installed = append(installed, files...)
		}
	}
	installedFile, err := h.downloadToMods(r, downloadURL, fileName, server)
	if err != nil {
		return nil, err
	}
	metadata := modMetadata{
		Source:             "modrinth",
		ProjectID:          project.ID,
		Slug:               project.Slug,
		Title:              project.Title,
		Summary:            project.Description,
		Description:        project.Body,
		IconURL:            project.IconURL,
		PageURL:            "https://modrinth.com/mod/" + firstNonEmpty(project.Slug, project.ID),
		VersionID:          version.ID,
		VersionName:        version.Name,
		VersionNumber:      version.VersionNumber,
		DependencyWarnings: warnings,
		InstalledAt:        time.Now().UTC().Format(time.RFC3339),
	}
	if err := saveModMetadata(server, installedFile, metadata); err != nil {
		return nil, err
	}
	installed = append(installed, installedFile)
	return installed, nil
}

func (h apiHandler) downloadToMods(r *http.Request, requestURL string, fileName string, server store.Server) (string, error) {
	response, err := fetchResponse(r, requestURL)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", errors.New("Mod download failed: " + strconv.Itoa(response.StatusCode))
	}
	safeName, err := uniqueModFileName(server, fileName)
	if err != nil {
		return "", err
	}
	modsPath := filepath.Join(server.Path, modsActiveDir(server))
	if err := os.MkdirAll(modsPath, 0o755); err != nil {
		return "", err
	}
	targetPath := filepath.Join(modsPath, safeName)
	output, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	copyErr := copyBoundedDownload(output, response.Body, maxArtifactDownloadBytes)
	closeErr := output.Close()
	if copyErr != nil {
		_ = os.Remove(targetPath)
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return safeName, nil
}
