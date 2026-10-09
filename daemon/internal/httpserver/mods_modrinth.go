package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

type modrinthProject struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	ProjectType string `json:"project_type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Body        string `json:"body"`
	IconURL     string `json:"icon_url"`
	Downloads   int64  `json:"downloads"`
	Followers   int64  `json:"followers"`
}

type modrinthDependency struct {
	VersionID      string `json:"version_id"`
	ProjectID      string `json:"project_id"`
	DependencyType string `json:"dependency_type"`
}

type modrinthVersion struct {
	ID            string               `json:"id"`
	ProjectID     string               `json:"project_id"`
	Name          string               `json:"name"`
	VersionNumber string               `json:"version_number"`
	GameVersions  []string             `json:"game_versions"`
	Loaders       []string             `json:"loaders"`
	Dependencies  []modrinthDependency `json:"dependencies"`
	Files         []struct {
		Primary  bool   `json:"primary"`
		URL      string `json:"url"`
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
	} `json:"files"`
}

func (h apiHandler) searchModrinth(r *http.Request, query string, server store.Server, options modSearchOptions) ([]map[string]any, int, error) {
	version := firstNonEmpty(options.Version, server.MinecraftVersion)
	requestLimit := options.Limit
	if requestLimit > 100 {
		requestLimit = 100
	}
	projectType := firstNonEmpty(options.ProjectType, "mod")
	// Modrinth has a dedicated "plugin" project type. Plugins are also tagged
	// with loader categories like paper, purpur, folia, spigot, bukkit. When
	// searching for plugins, filter by the server's platform as a loader
	// category (defaulting to the server type when no loader is selected).
	facetGroups := [][]string{{"project_type:" + projectType}, {"versions:" + version}}
	if projectType == "plugin" {
		loader := options.Loader
		if loader == "" {
			loader = server.Type
		}
		if loader != "" {
			facetGroups = append(facetGroups, []string{"categories:" + loader})
		}
	} else if options.Loader != "" {
		facetGroups = append(facetGroups, []string{"categories:" + options.Loader})
	}
	if options.Category != "" {
		facetGroups = append(facetGroups, []string{"categories:" + options.Category})
	}
	if projectType == "mod" || projectType == "plugin" {
		switch options.Side {
		case "server":
			facetGroups = append(facetGroups, []string{"server_side:required", "server_side:optional"})
		case "client":
			facetGroups = append(facetGroups, []string{"client_side:required", "client_side:optional"})
		case "both":
			facetGroups = append(facetGroups, []string{"server_side:required", "server_side:optional"}, []string{"client_side:required", "client_side:optional"})
		}
	}
	facets, _ := json.Marshal(facetGroups)
	requestURL := "https://api.modrinth.com/v2/search"
	parsed, _ := url.Parse(requestURL)
	values := parsed.Query()
	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery != "" && trimmedQuery != "." {
		values.Set("query", trimmedQuery)
	}
	values.Set("limit", strconv.Itoa(requestLimit))
	values.Set("offset", strconv.Itoa(options.Offset))
	values.Set("facets", string(facets))
	if index := modrinthSortIndex[options.Sort]; index != "" {
		values.Set("index", index)
	} else if trimmedQuery == "" || trimmedQuery == "." {
		values.Set("index", "downloads")
	} else {
		values.Set("index", "relevance")
	}
	parsed.RawQuery = values.Encode()
	var data struct {
		Hits []map[string]any `json:"hits"`
	}
	if err := fetchJSON(r, parsed.String(), &data); err != nil {
		return nil, 0, err
	}
	return data.Hits, options.Offset + requestLimit, nil
}

func (h apiHandler) compatibleModrinthVersions(r *http.Request, projectID string, server store.Server, options modSearchOptions) ([]modrinthVersion, error) {
	version := firstNonEmpty(options.Version, server.MinecraftVersion)
	loader := options.Loader
	if options.ProjectType == "modpack" {
		loader = ""
	} else if loader == "" && (serverTypeNeedsLoader(server.Type) || serverTypeNeedsPlugins(server.Type)) {
		loader = server.Type
	}
	parsed, _ := url.Parse("https://api.modrinth.com/v2/project/" + url.PathEscape(projectID) + "/version")
	values := parsed.Query()
	values.Set("game_versions", jsonArrayParam(version))
	if loader != "" {
		values.Set("loaders", jsonArrayParam(loader))
	}
	parsed.RawQuery = values.Encode()
	var versions []modrinthVersion
	err := fetchJSON(r, parsed.String(), &versions)
	return versions, err
}

func (h apiHandler) modrinthProjectDetails(r *http.Request, projectID string, server store.Server, options modSearchOptions) (map[string]any, error) {
	project, err := h.getModrinthProject(r, projectID)
	if err != nil {
		return nil, err
	}
	versions, err := h.compatibleModrinthVersions(r, projectID, server, options)
	if err != nil {
		return nil, err
	}
	return map[string]any{"project": project, "versions": versions}, nil
}

func (h apiHandler) getModrinthProject(r *http.Request, projectID string) (modrinthProject, error) {
	var project modrinthProject
	err := fetchJSON(r, "https://api.modrinth.com/v2/project/"+url.PathEscape(projectID), &project)
	return project, err
}

func (h apiHandler) getModrinthVersion(r *http.Request, versionID string) (modrinthVersion, error) {
	var version modrinthVersion
	err := fetchJSON(r, "https://api.modrinth.com/v2/version/"+url.PathEscape(versionID), &version)
	return version, err
}

func (h apiHandler) resolveModrinthInstallTarget(r *http.Request, projectID string, server store.Server, versionID string) (modrinthProject, modrinthVersion, string, string, error) {
	return h.resolveModrinthInstallTargetWith(r, projectID, server, versionID, modSearchOptions{})
}

func (h apiHandler) resolveModrinthInstallTargetWith(r *http.Request, projectID string, server store.Server, versionID string, options modSearchOptions) (modrinthProject, modrinthVersion, string, string, error) {
	project, err := h.getModrinthProject(r, projectID)
	if err != nil {
		return modrinthProject{}, modrinthVersion{}, "", "", err
	}
	var version modrinthVersion
	if versionID != "" {
		version, err = h.getModrinthVersion(r, versionID)
		if err != nil {
			return modrinthProject{}, modrinthVersion{}, "", "", err
		}
		if !modrinthVersionCompatible(version, server) {
			return modrinthProject{}, modrinthVersion{}, "", "", errors.New("Selected Modrinth version is not compatible with this server")
		}
	} else {
		versions, err := h.compatibleModrinthVersions(r, projectID, server, options)
		if err != nil {
			return modrinthProject{}, modrinthVersion{}, "", "", err
		}
		if len(versions) == 0 {
			return modrinthProject{}, modrinthVersion{}, "", "", errors.New("No compatible Modrinth file found")
		}
		version = versions[0]
	}
	for _, file := range version.Files {
		if file.Primary {
			return project, version, file.URL, file.Filename, nil
		}
	}
	if len(version.Files) > 0 {
		return project, version, version.Files[0].URL, version.Files[0].Filename, nil
	}
	return modrinthProject{}, modrinthVersion{}, "", "", errors.New("No compatible Modrinth file found")
}

func modrinthVersionCompatible(version modrinthVersion, server store.Server) bool {
	needsLoader := serverTypeNeedsLoader(server.Type) || serverTypeNeedsPlugins(server.Type)
	return stringSliceContains(version.GameVersions, server.MinecraftVersion) && (!needsLoader || stringSliceContains(version.Loaders, server.Type))
}

func (h apiHandler) modrinthInstallPlan(r *http.Request, projectID string, server store.Server, versionID string) (map[string]any, error) {
	project, version, _, _, err := h.resolveModrinthInstallTarget(r, projectID, server, versionID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"project": map[string]any{
			"projectId":     project.ID,
			"title":         project.Title,
			"slug":          project.Slug,
			"versionId":     version.ID,
			"versionNumber": version.VersionNumber,
		},
		"dependencies": h.requiredModrinthDependencies(r, version),
	}, nil
}

func (h apiHandler) requiredModrinthDependencies(r *http.Request, version modrinthVersion) []modDependencyWarning {
	dependencies := []modDependencyWarning{}
	seen := map[string]bool{}
	for _, dependency := range version.Dependencies {
		if dependency.DependencyType != "required" {
			continue
		}
		projectID := dependency.ProjectID
		versionNumber := ""
		if projectID == "" && dependency.VersionID != "" {
			dependencyVersion, err := h.getModrinthVersion(r, dependency.VersionID)
			if err != nil {
				continue
			}
			projectID = dependencyVersion.ProjectID
			versionNumber = dependencyVersion.VersionNumber
		}
		if projectID == "" || seen[projectID] {
			continue
		}
		seen[projectID] = true
		project, err := h.getModrinthProject(r, projectID)
		if err != nil {
			continue
		}
		dependencies = append(dependencies, modDependencyWarning{ProjectID: project.ID, VersionID: dependency.VersionID, Title: project.Title, Slug: project.Slug, Summary: project.Description, IconURL: project.IconURL, VersionNumber: versionNumber})
	}
	return dependencies
}

func (h apiHandler) modrinthDependencyDetails(r *http.Request, dependencies []modDependencyWarning) ([]modDependencyWarning, error) {
	details := make([]modDependencyWarning, 0, len(dependencies))
	for _, dependency := range dependencies {
		project, err := h.getModrinthProject(r, dependency.ProjectID)
		if err != nil {
			return nil, err
		}
		if dependency.Title == "" {
			dependency.Title = project.Title
		}
		if dependency.Slug == "" {
			dependency.Slug = project.Slug
		}
		if dependency.Summary == "" {
			dependency.Summary = project.Description
		}
		if dependency.IconURL == "" {
			dependency.IconURL = project.IconURL
		}
		details = append(details, dependency)
	}
	return details, nil
}
