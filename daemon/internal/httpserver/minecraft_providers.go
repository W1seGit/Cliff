package httpserver

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type papermcFillProjectResponse struct {
	Versions map[string][]string `json:"versions"`
}

type papermcOfficialProjectResponse struct {
	Versions []string `json:"versions"`
}

// fetchPaperProjectVersions returns stable and experimental Minecraft versions
// for a PaperMC project (paper or folia). A version is "stable" if it has at
// least one STABLE-channel build; otherwise it's "experimental" (alpha-only).
// The official api.papermc.io/v2 API is used as a fast path for known stable
// versions, but it can lag behind the fill API. For versions only in the fill
// API, we fetch their builds to check the channel.
func (h apiHandler) fetchPaperProjectVersions(r *http.Request, project string) ([]string, []string, error) {
	// Fetch all versions from the fill API (includes experimental)
	fillURL := "https://fill.papermc.io/v3/projects/" + url.PathEscape(project)
	var fillResp papermcFillProjectResponse
	if err := fetchJSON(r, fillURL, &fillResp); err != nil {
		return nil, nil, err
	}
	allVersions := []string{}
	for _, group := range fillResp.Versions {
		allVersions = append(allVersions, group...)
	}

	// Fetch stable versions from the official API (fast path — these are
	// definitely stable and we don't need to check their builds)
	officialURL := "https://api.papermc.io/v2/projects/" + url.PathEscape(project)
	var officialResp papermcOfficialProjectResponse
	knownStable := map[string]bool{}
	if err := fetchJSON(r, officialURL, &officialResp); err == nil {
		for _, v := range officialResp.Versions {
			knownStable[v] = true
		}
	}

	stable := []string{}
	experimental := []string{}
	for _, v := range allVersions {
		if knownStable[v] {
			stable = append(stable, v)
			continue
		}
		// Version not in official API — check if it has any STABLE builds
		hasStable, err := h.paperVersionHasStableBuild(r, project, v)
		if err != nil {
			// If we can't check, treat as experimental (safe default)
			experimental = append(experimental, v)
		} else if hasStable {
			stable = append(stable, v)
		} else {
			experimental = append(experimental, v)
		}
	}
	return stable, experimental, nil
}

// paperVersionHasStableBuild checks if a PaperMC project version has at least
// one STABLE-channel build via the fill API.
func (h apiHandler) paperVersionHasStableBuild(r *http.Request, project, version string) (bool, error) {
	requestURL := "https://fill.papermc.io/v3/projects/" + url.PathEscape(project) + "/versions/" + url.PathEscape(version) + "/builds"
	var builds []paperBuild
	if err := fetchJSON(r, requestURL, &builds); err != nil {
		return false, err
	}
	for _, build := range builds {
		if build.Channel == "STABLE" {
			return true, nil
		}
	}
	return false, nil
}

type purpurProjectResponse struct {
	Versions []string `json:"versions"`
	Metadata struct {
		Current string `json:"current"`
	} `json:"metadata"`
}

type purpurBuildDetail struct {
	Metadata struct {
		Type string `json:"type"`
	} `json:"metadata"`
}

// fetchPurpurVersions returns stable and experimental Minecraft versions for
// Purpur. The Purpur API lists all versions but doesn't indicate which are
// experimental at the project level. We use the metadata.current field as a
// boundary — versions newer than current are checked via their latest build's
// metadata.type to determine if they're experimental. Versions at or older
// than current are always stable (they've had stable builds for a long time).
func (h apiHandler) fetchPurpurVersions(r *http.Request) ([]string, []string, error) {
	var resp purpurProjectResponse
	if err := fetchJSON(r, "https://api.purpurmc.org/v2/purpur", &resp); err != nil {
		return nil, nil, err
	}
	current := resp.Metadata.Current
	stable := []string{}
	experimental := []string{}
	for _, version := range resp.Versions {
		// Only check versions newer than the current stable version
		if current != "" && compareMinecraftVersionStrings(version, current) > 0 {
			isExp, err := h.purpurVersionIsExperimental(r, version)
			if err != nil {
				// If we can't check, treat as stable
				stable = append(stable, version)
			} else if isExp {
				experimental = append(experimental, version)
			} else {
				stable = append(stable, version)
			}
		} else {
			stable = append(stable, version)
		}
	}
	return stable, experimental, nil
}

// purpurVersionIsExperimental checks if the latest build of a Purpur version
// is marked as experimental.
func (h apiHandler) purpurVersionIsExperimental(r *http.Request, version string) (bool, error) {
	var detail purpurBuildDetail
	requestURL := "https://api.purpurmc.org/v2/purpur/" + url.PathEscape(version) + "/latest"
	if err := fetchJSON(r, requestURL, &detail); err != nil {
		return false, err
	}
	return detail.Metadata.Type == "experimental", nil
}

// compareMinecraftVersionStrings compares two Minecraft version strings.
// Returns >0 if a > b, <0 if a < b, 0 if equal.
// Handles versions like "1.21.11", "26.2", "26.1.2".
func compareMinecraftVersionStrings(a, b string) int {
	partsA := strings.Split(a, ".")
	partsB := strings.Split(b, ".")
	maxLen := len(partsA)
	if len(partsB) > maxLen {
		maxLen = len(partsB)
	}
	for i := 0; i < maxLen; i++ {
		var pa, pb string
		if i < len(partsA) {
			pa = partsA[i]
		}
		if i < len(partsB) {
			pb = partsB[i]
		}
		na, errA := strconv.Atoi(pa)
		nb, errB := strconv.Atoi(pb)
		if errA == nil && errB == nil {
			if na != nb {
				return na - nb
			}
		} else if errA == nil {
			return 1 // numeric > non-numeric
		} else if errB == nil {
			return -1
		} else {
			if pa != pb {
				if pa > pb {
					return 1
				}
				return -1
			}
		}
	}
	return 0
}

type fabricGameVersionEntry struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
}

func (h apiHandler) fetchFabricGameVersions(r *http.Request) ([]string, error) {
	var entries []fabricGameVersionEntry
	if err := fetchJSON(r, "https://meta.fabricmc.net/v2/versions/game", &entries); err != nil {
		return nil, err
	}
	versions := []string{}
	for _, entry := range entries {
		if entry.Stable {
			versions = append(versions, entry.Version)
		}
	}
	if len(versions) == 0 {
		// Fallback: include all versions if no stable ones are returned
		for _, entry := range entries {
			versions = append(versions, entry.Version)
		}
	}
	return versions, nil
}

func (h apiHandler) fetchForgeMinecraftVersions(r *http.Request, refresh bool) ([]string, error) {
	versions, err := h.fetchMavenVersions(r, "https://maven.minecraftforge.net/net/minecraftforge/forge/maven-metadata.xml", refresh)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	result := []string{}
	for _, v := range versions {
		idx := strings.Index(v, "-")
		if idx == -1 {
			continue
		}
		mcVersion := v[:idx]
		if !seen[mcVersion] {
			seen[mcVersion] = true
			result = append(result, mcVersion)
		}
	}
	return result, nil
}

func (h apiHandler) fetchNeoForgeMinecraftVersions(r *http.Request, refresh bool) ([]string, error) {
	versions, err := h.fetchMavenVersions(r, "https://maven.neoforged.net/releases/net/neoforged/neoforge/maven-metadata.xml", refresh)
	if err != nil {
		return nil, err
	}
	// NeoForge versions look like "21.1.123" or "20.6.119-beta" etc.
	// Map them back to Minecraft versions:
	//   21.x → 1.21.x, 20.x → 1.20.x, etc.
	seen := map[string]bool{}
	result := []string{}
	for _, v := range versions {
		mcVersion := neoforgeVersionToMinecraft(v)
		if mcVersion != "" && !seen[mcVersion] {
			seen[mcVersion] = true
			result = append(result, mcVersion)
		}
	}
	return result, nil
}

// neoforgeVersionToMinecraft converts a NeoForge artifact version like
// "21.1.123" to a Minecraft version like "1.21.1".
// For the new Minecraft versioning (26.x+), "26.2.0.7" maps to "26.2".
// NeoForge uses: major = MC minor (without the "1." prefix for old versions),
// first patch = MC patch. Versions with major >= 22 use the new MC format.
func neoforgeVersionToMinecraft(neoforgeVersion string) string {
	parts := strings.SplitN(neoforgeVersion, "-", 2)
	versionParts := strings.Split(parts[0], ".")
	if len(versionParts) < 2 {
		return ""
	}
	major := versionParts[0]
	minor := versionParts[1]
	majorNum, err := strconv.Atoi(major)
	if err != nil {
		return ""
	}
	if majorNum >= 22 {
		// New Minecraft versioning (e.g., 26.2)
		return major + "." + minor
	}
	// Old Minecraft versioning (e.g., 1.21.1)
	return "1." + major + "." + minor
}

func (h apiHandler) fetchFabricLoaders(r *http.Request, minecraftVersion string) ([]loaderOption, error) {
	requestURL := "https://meta.fabricmc.net/v2/versions/loader/" + url.PathEscape(minecraftVersion)
	var data []fabricLoaderEntry
	if err := fetchJSON(r, requestURL, &data); err != nil {
		return nil, err
	}
	loaders := []loaderOption{}
	for _, entry := range data {
		loaders = append(loaders, loaderOption{Version: entry.Loader.Version, Stable: entry.Loader.Stable})
		if len(loaders) >= 80 {
			break
		}
	}
	return loaders, nil
}

func (h apiHandler) fetchForgePromotions(r *http.Request, refresh bool) (forgePromotions, error) {
	if !refresh && h.metadataCache != nil {
		if cached, ok := h.metadataCache.getForgePromotions(); ok {
			return cached, nil
		}
	}
	promotions, err := fetchForgePromotions(r)
	if err == nil && h.metadataCache != nil {
		h.metadataCache.setForgePromotions(promotions)
	}
	return promotions, err
}

func fetchForgePromotions(r *http.Request) (forgePromotions, error) {
	var promotions forgePromotions
	return promotions, fetchForgePromotionsInto(r, &promotions)
}

func fetchForgePromotionsInto(r *http.Request, promotions *forgePromotions) error {
	return fetchJSON(r, "https://files.minecraftforge.net/net/minecraftforge/forge/promotions_slim.json", promotions)
}

func fetchMavenVersions(r *http.Request, requestURL string) ([]string, error) {
	text, err := fetchText(r, requestURL)
	if err != nil {
		return nil, err
	}
	matches := mavenVersionPattern.FindAllStringSubmatch(text, -1)
	versions := []string{}
	for i := len(matches) - 1; i >= 0; i-- {
		versions = append(versions, matches[i][1])
	}
	return versions, nil
}

func (h apiHandler) fetchMavenVersions(r *http.Request, requestURL string, refresh bool) ([]string, error) {
	if !refresh && h.metadataCache != nil {
		if cached, ok := h.metadataCache.getMavenVersions(requestURL); ok {
			return cached, nil
		}
	}
	versions, err := fetchMavenVersions(r, requestURL)
	if err == nil && h.metadataCache != nil {
		h.metadataCache.setMavenVersions(requestURL, versions)
	}
	return versions, err
}

func forgePromotedVersions(promotions forgePromotions, minecraftVersion string) []loaderOption {
	promos := promotions.Promos
	if promos == nil {
		return []loaderOption{}
	}
	loaders := []loaderOption{}
	for _, key := range []string{minecraftVersion + "-recommended", minecraftVersion + "-latest"} {
		if value := promos[key]; value != "" {
			loaders = append(loaders, loaderOption{Version: value, Stable: true})
		}
	}
	return uniqueLoaders(loaders)
}

func forgeVersionsForMinecraft(versions []string, minecraftVersion string) []loaderOption {
	loaders := []loaderOption{}
	prefix := minecraftVersion + "-"
	for _, version := range versions {
		if strings.HasPrefix(version, prefix) {
			loaders = append(loaders, loaderOption{Version: strings.TrimPrefix(version, prefix), Stable: true})
		}
	}
	return loaders
}

func neoforgeVersionsForMinecraft(versions []string, minecraftVersion string) []loaderOption {
	prefixes := []string{minecraftVersion}
	parts := strings.Split(strings.TrimPrefix(minecraftVersion, "1."), ".")
	if strings.HasPrefix(minecraftVersion, "1.") && len(parts) >= 1 {
		patch := "0"
		if len(parts) >= 2 && parts[1] != "" {
			patch = parts[1]
		}
		prefixes = append(prefixes, parts[0]+"."+patch)
	}
	loaders := []loaderOption{}
	for _, version := range versions {
		for _, prefix := range prefixes {
			if version == prefix || strings.HasPrefix(version, prefix+".") {
				loaders = append(loaders, loaderOption{Version: version, Stable: !strings.Contains(version, "beta") && !strings.Contains(version, "alpha")})
				break
			}
		}
	}
	return loaders
}

func recentForgeLoaderVersions(versions []string) []loaderOption {
	seen := map[string]bool{}
	loaders := []loaderOption{}
	for _, version := range versions {
		index := strings.Index(version, "-")
		if index == -1 {
			continue
		}
		loader := version[index+1:]
		if seen[loader] {
			continue
		}
		seen[loader] = true
		loaders = append(loaders, loaderOption{Version: loader, Stable: true})
		if len(loaders) >= 50 {
			break
		}
	}
	return loaders
}

func recentNeoForgeLoaderVersions(versions []string) []loaderOption {
	loaders := []loaderOption{}
	for _, version := range versions {
		loaders = append(loaders, loaderOption{Version: version, Stable: !strings.Contains(version, "beta") && !strings.Contains(version, "alpha")})
		if len(loaders) >= 50 {
			break
		}
	}
	return loaders
}

func uniqueLoaders(loaders []loaderOption) []loaderOption {
	seen := map[string]bool{}
	result := []loaderOption{}
	for _, loader := range loaders {
		if loader.Version == "" || seen[loader.Version] {
			continue
		}
		seen[loader.Version] = true
		result = append(result, loader)
	}
	return result
}

func firstN(loaders []loaderOption, limit int) []loaderOption {
	if len(loaders) <= limit {
		return loaders
	}
	return loaders[:limit]
}
