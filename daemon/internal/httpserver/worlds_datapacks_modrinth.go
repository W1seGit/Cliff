package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func searchModrinthDatapacks(r *http.Request, server store.Server) ([]map[string]any, error) {
	limit := boundedQueryInt(r, "limit", 20, 1, 100)
	offset := boundedQueryInt(r, "offset", 0, 0, 10000)
	version := firstNonEmpty(strings.TrimSpace(r.URL.Query().Get("version")), server.MinecraftVersion)
	facets, _ := json.Marshal([][]string{
		{"project_type:datapack"},
		{"versions:" + version},
	})
	requestURL, _ := url.Parse("https://api.modrinth.com/v2/search")
	query := requestURL.Query()
	rawQuery := strings.TrimSpace(r.URL.Query().Get("q"))
	if rawQuery != "" && rawQuery != "." {
		query.Set("query", rawQuery)
	}
	query.Set("limit", strconv.Itoa(limit))
	query.Set("offset", strconv.Itoa(offset))
	query.Set("facets", string(facets))
	if index := modrinthSortIndex[strings.TrimSpace(r.URL.Query().Get("sort"))]; index != "" {
		query.Set("index", index)
	} else if rawQuery == "" || rawQuery == "." {
		query.Set("index", "downloads")
	} else {
		query.Set("index", "relevance")
	}
	requestURL.RawQuery = query.Encode()

	var payload struct {
		Hits []map[string]any `json:"hits"`
	}
	if err := fetchJSON(r, requestURL.String(), &payload); err != nil {
		if strings.Contains(err.Error(), "429") {
			return nil, errors.New("Modrinth rate limit reached. Wait a moment before loading more results.")
		}
		return nil, err
	}
	return payload.Hits, nil
}

func (h apiHandler) installModrinthDatapack(r *http.Request, server store.Server, worldName string, projectID string, versionID string) ([]string, error) {
	return h.installModrinthDatapackProject(r, server, worldName, projectID, versionID, map[string]bool{})
}

func (h apiHandler) installModrinthDatapackProject(r *http.Request, server store.Server, worldName string, projectID string, versionID string, seen map[string]bool) ([]string, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, errors.New("Modrinth project id is required")
	}
	if seen[projectID] {
		return []string{}, nil
	}
	seen[projectID] = true

	var version modrinthVersion
	if strings.TrimSpace(versionID) != "" {
		selected, err := h.getModrinthVersion(r, versionID)
		if err != nil {
			return nil, err
		}
		if selected.ProjectID != projectID || !stringSliceContains(selected.GameVersions, server.MinecraftVersion) || !stringSliceContains(selected.Loaders, "datapack") {
			return nil, errors.New("Selected Modrinth datapack version is not compatible with this server")
		}
		version = selected
	} else {
		versions, err := h.compatibleModrinthDatapackVersions(r, projectID, server, "")
		if err != nil {
			return nil, err
		}
		if len(versions) == 0 {
			return nil, errors.New("No compatible Modrinth datapack file found")
		}
		version = versions[0]
	}
	fileURL := ""
	fileName := ""
	for _, file := range version.Files {
		if file.Primary && strings.HasSuffix(strings.ToLower(file.Filename), ".zip") {
			fileURL = file.URL
			fileName = file.Filename
			break
		}
	}
	if fileURL == "" {
		for _, file := range version.Files {
			if strings.HasSuffix(strings.ToLower(file.Filename), ".zip") {
				fileURL = file.URL
				fileName = file.Filename
				break
			}
		}
	}
	if fileURL == "" {
		for _, file := range version.Files {
			if file.Primary {
				fileURL = file.URL
				fileName = file.Filename
				break
			}
		}
	}
	if fileURL == "" && len(version.Files) > 0 {
		fileURL = version.Files[0].URL
		fileName = version.Files[0].Filename
	}
	if fileURL == "" || fileName == "" {
		return nil, errors.New("No downloadable Modrinth datapack file found for the selected version")
	}

	installed := []string{}
	for _, dependency := range version.Dependencies {
		if dependency.DependencyType != "required" {
			continue
		}
		dependencyProjectID := dependency.ProjectID
		if dependencyProjectID == "" && dependency.VersionID != "" {
			dependencyVersion, err := h.getModrinthVersion(r, dependency.VersionID)
			if err != nil {
				return nil, err
			}
			dependencyProjectID = dependencyVersion.ProjectID
		}
		if dependencyProjectID == "" {
			continue
		}
		dependencyFiles, err := h.installModrinthDatapackProject(r, server, worldName, dependencyProjectID, dependency.VersionID, seen)
		if err != nil {
			return nil, err
		}
		installed = append(installed, dependencyFiles...)
	}

	safeName, err := uniqueDatapackFileName(server, worldName, fileName)
	if err != nil {
		return nil, err
	}
	safeWorld, err := safePathSegment(worldName, "World name")
	if err != nil {
		return nil, err
	}
	targetDir, err := resolveInside(server.Path, filepath.Join(safeWorld, "datapacks"))
	if err != nil {
		return nil, err
	}
	target, err := resolveInside(targetDir, safeName)
	if err != nil {
		return nil, err
	}
	if err := downloadFile(r, fileURL, target); err != nil {
		return nil, err
	}
	project, _ := h.getModrinthProject(r, projectID)
	if project.ID != "" {
		metadata := modMetadata{
			Source:        "modrinth",
			ProjectID:     project.ID,
			Slug:          project.Slug,
			Title:         project.Title,
			Summary:       project.Description,
			IconURL:       project.IconURL,
			PageURL:       "https://modrinth.com/datapack/" + firstNonEmpty(project.Slug, project.ID),
			VersionID:     version.ID,
			VersionName:   version.Name,
			VersionNumber: version.VersionNumber,
			InstalledAt:   time.Now().UTC().Format(time.RFC3339),
		}
		_ = saveDatapackMetadata(server, worldName, safeName, metadata)
	}
	installed = append(installed, safeName)
	return installed, nil
}

func (h apiHandler) modrinthDatapackProjectDetails(r *http.Request, projectID string, server store.Server, requestedVersion string) (map[string]any, error) {
	project, err := h.getModrinthProject(r, projectID)
	if err != nil {
		return nil, err
	}
	versions, err := h.compatibleModrinthDatapackVersions(r, projectID, server, requestedVersion)
	if err != nil {
		return nil, err
	}
	return map[string]any{"project": project, "versions": versions}, nil
}

func (h apiHandler) compatibleModrinthDatapackVersions(r *http.Request, projectID string, server store.Server, requestedVersion string) ([]modrinthVersion, error) {
	version := firstNonEmpty(strings.TrimSpace(requestedVersion), server.MinecraftVersion)
	parsed, _ := url.Parse("https://api.modrinth.com/v2/project/" + url.PathEscape(projectID) + "/version")
	values := parsed.Query()
	values.Set("game_versions", jsonArrayParam(version))
	values.Set("loaders", jsonArrayParam("datapack"))
	parsed.RawQuery = values.Encode()
	var versions []modrinthVersion
	err := fetchJSON(r, parsed.String(), &versions)
	return versions, err
}
