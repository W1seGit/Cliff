package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type minecraftVersionOption struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	URL         string `json:"url"`
	Time        string `json:"time"`
	ReleaseTime string `json:"releaseTime"`
}

type loaderOption struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
}

type minecraftMetadata struct {
	FetchedAt         string                    `json:"fetchedAt"`
	Latest            minecraftLatest           `json:"latest"`
	MinecraftVersions []minecraftVersionOption  `json:"minecraftVersions"`
	Loaders           map[string][]loaderOption `json:"loaders"`
	LoaderCatalog     map[string][]loaderOption `json:"loaderCatalog"`
}

type minecraftLatest struct {
	Release  string `json:"release"`
	Snapshot string `json:"snapshot"`
}

type mojangManifest struct {
	Latest   minecraftLatest          `json:"latest"`
	Versions []minecraftVersionOption `json:"versions"`
}

type fabricLoaderEntry struct {
	Loader struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
	} `json:"loader"`
}

type forgePromotions struct {
	Promos map[string]string `json:"promos"`
}

var mavenVersionPattern = regexp.MustCompile(`<version>([^<]+)</version>`)

const (
	maxMetadataLoaderCacheEntries = 256
	externalResponseHeaderTimeout = 15 * time.Second
	maxExternalResponseBytes      = 16 * 1024 * 1024
	maxArtifactDownloadBytes      = 512 * 1024 * 1024
)

var (
	externalHTTPClient     = &http.Client{Transport: externalHTTPTransport(false)}
	externalIPv4HTTPClient = &http.Client{Transport: externalHTTPTransport(true)}
)

type metadataCache struct {
	mu                   sync.Mutex
	metadata             minecraftMetadata
	loaders              map[string][]loaderOption
	mavenVersions        map[string][]string
	forgePromotions      *forgePromotions
	typeVersions         map[string][]string
	typeExpVersions      map[string][]string
}

func (c *metadataCache) getMetadata() (minecraftMetadata, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.metadata.MinecraftVersions) == 0 {
		return minecraftMetadata{}, false
	}
	return c.metadata, true
}

func (c *metadataCache) setMetadata(metadata minecraftMetadata) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.metadata = metadata
}

func (c *metadataCache) getLoaders(serverType string, minecraftVersion string) ([]loaderOption, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaders == nil {
		return nil, false
	}
	loaders, ok := c.loaders[loaderCacheKey(serverType, minecraftVersion)]
	if !ok {
		return nil, false
	}
	return append([]loaderOption(nil), loaders...), true
}

func (c *metadataCache) setLoaders(serverType string, minecraftVersion string, loaders []loaderOption) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaders == nil {
		c.loaders = map[string][]loaderOption{}
	}
	key := loaderCacheKey(serverType, minecraftVersion)
	if _, exists := c.loaders[key]; !exists {
		enforceLoaderCacheLimitLocked(c.loaders, maxMetadataLoaderCacheEntries-1)
	}
	c.loaders[key] = append([]loaderOption(nil), loaders...)
}

func enforceLoaderCacheLimitLocked(loaders map[string][]loaderOption, limit int) {
	for len(loaders) > limit {
		for key := range loaders {
			delete(loaders, key)
			break
		}
	}
}

func (c *metadataCache) getMavenVersions(requestURL string) ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.mavenVersions == nil {
		return nil, false
	}
	versions, ok := c.mavenVersions[requestURL]
	if !ok {
		return nil, false
	}
	return append([]string(nil), versions...), true
}

func (c *metadataCache) setMavenVersions(requestURL string, versions []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.mavenVersions == nil {
		c.mavenVersions = map[string][]string{}
	}
	c.mavenVersions[requestURL] = append([]string(nil), versions...)
}

func (c *metadataCache) getForgePromotions() (forgePromotions, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.forgePromotions == nil {
		return forgePromotions{}, false
	}
	return *c.forgePromotions, true
}

func (c *metadataCache) setForgePromotions(promotions forgePromotions) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.forgePromotions = &promotions
}

func (c *metadataCache) getTypeVersions(serverType string) ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.typeVersions == nil {
		return nil, false
	}
	versions, ok := c.typeVersions[serverType]
	if !ok {
		return nil, false
	}
	return append([]string(nil), versions...), true
}

func (c *metadataCache) setTypeVersions(serverType string, versions []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.typeVersions == nil {
		c.typeVersions = map[string][]string{}
	}
	c.typeVersions[serverType] = append([]string(nil), versions...)
}

func (c *metadataCache) getTypeExpVersions(serverType string) ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.typeExpVersions == nil {
		return nil, false
	}
	versions, ok := c.typeExpVersions[serverType]
	if !ok {
		return nil, false
	}
	return append([]string(nil), versions...), true
}

func (c *metadataCache) setTypeExpVersions(serverType string, versions []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.typeExpVersions == nil {
		c.typeExpVersions = map[string][]string{}
	}
	c.typeExpVersions[serverType] = append([]string(nil), versions...)
}

func loaderCacheKey(serverType string, minecraftVersion string) string {
	return serverType + ":" + minecraftVersion
}

func (h apiHandler) minecraftVersions(w http.ResponseWriter, r *http.Request) {
	serverType := r.URL.Query().Get("type")
	minecraftVersion := r.URL.Query().Get("minecraftVersion")
	refresh := r.URL.Query().Get("refresh") == "1"
	if serverType != "" && minecraftVersion != "" {
		if !validServerType(serverType) {
			writeError(w, http.StatusBadRequest, "Invalid server type")
			return
		}
		loaders, err := h.getLoaderVersions(r, serverType, minecraftVersion, refresh)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"loaders": loaders})
		return
	}
	if serverType != "" && minecraftVersion == "" {
		if !validServerType(serverType) {
			writeError(w, http.StatusBadRequest, "Invalid server type")
			return
		}
		versions, expVersions, err := h.getSupportedVersions(r, serverType, refresh)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		response := map[string]any{"versions": versions}
		if len(expVersions) > 0 {
			response["experimentalVersions"] = expVersions
		}
		writeJSON(w, http.StatusOK, response)
		return
	}
	metadata, err := h.getMinecraftMetadata(r, refresh)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, metadata)
}

func (h apiHandler) getMinecraftMetadata(r *http.Request, refresh bool) (minecraftMetadata, error) {
	cachePath := h.minecraftMetadataCachePath()
	if !refresh {
		if h.metadataCache != nil {
			if cached, ok := h.metadataCache.getMetadata(); ok {
				return cached, nil
			}
		}
		var cached minecraftMetadata
		if readJSONFile(cachePath, &cached) == nil && len(cached.MinecraftVersions) > 0 {
			if h.metadataCache != nil {
				h.metadataCache.setMetadata(cached)
			}
			return cached, nil
		}
	}

	var manifest mojangManifest
	if err := fetchJSON(r, "https://piston-meta.mojang.com/mc/game/version_manifest_v2.json", &manifest); err != nil {
		var cached minecraftMetadata
		if readJSONFile(cachePath, &cached) == nil && len(cached.MinecraftVersions) > 0 {
			if h.metadataCache != nil {
				h.metadataCache.setMetadata(cached)
			}
			return cached, nil
		}
		return minecraftMetadata{}, err
	}

	snapshots := []minecraftVersionOption{}
	releases := []minecraftVersionOption{}
	for _, version := range manifest.Versions {
		if version.Type == "snapshot" && len(snapshots) < 25 {
			snapshots = append(snapshots, version)
		}
		if version.Type == "release" {
			releases = append(releases, version)
		}
	}
	versions := append(snapshots, releases...)

	fallback := minecraftMetadata{
		Loaders: map[string][]loaderOption{
			"vanilla":  {},
			"paper":    {},
			"fabric":   {},
			"forge":    {},
			"neoforge": {},
		},
		LoaderCatalog: map[string][]loaderOption{
			"vanilla":  {},
			"paper":    {},
			"fabric":   {},
			"forge":    {},
			"neoforge": {},
		},
	}
	_ = readJSONFile(cachePath, &fallback)

	fabricLoaders, _ := h.fetchFabricLoaders(r, manifest.Latest.Release)
	forgePromos, _ := h.fetchForgePromotions(r, refresh)
	forgeVersions, _ := h.fetchMavenVersions(r, "https://maven.minecraftforge.net/net/minecraftforge/forge/maven-metadata.xml", refresh)
	neoforgeVersions, _ := h.fetchMavenVersions(r, "https://maven.neoforged.net/releases/net/neoforged/neoforge/maven-metadata.xml", refresh)

	fabric := firstN(fabricLoaders, 50)
	forge := firstN(uniqueLoaders(append(forgePromotedVersions(forgePromos, manifest.Latest.Release), forgeVersionsForMinecraft(forgeVersions, manifest.Latest.Release)...)), 50)
	if len(forge) == 0 && len(forgeVersions) > 0 {
		forge = recentForgeLoaderVersions(forgeVersions)
	}
	if len(forge) == 0 {
		forge = fallback.Loaders["forge"]
	}
	neoforge := firstN(neoforgeVersionsForMinecraft(neoforgeVersions, manifest.Latest.Release), 50)
	if len(neoforge) == 0 && len(neoforgeVersions) > 0 {
		neoforge = recentNeoForgeLoaderVersions(neoforgeVersions)
	}
	if len(neoforge) == 0 {
		neoforge = fallback.Loaders["neoforge"]
	}
	if len(fabric) == 0 {
		fabric = fallback.Loaders["fabric"]
	}

	forgeCatalog := recentForgeLoaderVersions(forgeVersions)
	if len(forgeCatalog) == 0 {
		forgeCatalog = fallback.LoaderCatalog["forge"]
	}
	neoforgeCatalog := recentNeoForgeLoaderVersions(neoforgeVersions)
	if len(neoforgeCatalog) == 0 {
		neoforgeCatalog = fallback.LoaderCatalog["neoforge"]
	}

	metadata := minecraftMetadata{
		FetchedAt:         time.Now().UTC().Format(time.RFC3339),
		Latest:            manifest.Latest,
		MinecraftVersions: versions,
		Loaders: map[string][]loaderOption{
			"vanilla":  {},
			"paper":    {},
			"fabric":   fabric,
			"forge":    forge,
			"neoforge": neoforge,
		},
		LoaderCatalog: map[string][]loaderOption{
			"vanilla":  {},
			"paper":    {},
			"fabric":   fabric,
			"forge":    forgeCatalog,
			"neoforge": neoforgeCatalog,
		},
	}
	if err := writeJSONFile(cachePath, metadata); err != nil {
		return minecraftMetadata{}, err
	}
	if h.metadataCache != nil {
		h.metadataCache.setMetadata(metadata)
	}
	_ = h.persistLoaderVersions("fabric", manifest.Latest.Release, fabric)
	_ = h.persistLoaderVersions("forge", manifest.Latest.Release, forge)
	_ = h.persistLoaderVersions("neoforge", manifest.Latest.Release, neoforge)
	return metadata, nil
}

func (h apiHandler) getLoaderVersions(r *http.Request, serverType string, minecraftVersion string, refresh bool) ([]loaderOption, error) {
	if !serverTypeNeedsLoader(serverType) {
		return []loaderOption{}, nil
	}
	if !refresh {
		if h.metadataCache != nil {
			if cached, ok := h.metadataCache.getLoaders(serverType, minecraftVersion); ok {
				return cached, nil
			}
		}
		var cached []loaderOption
		if readJSONFile(h.loaderCachePath(serverType, minecraftVersion), &cached) == nil {
			if h.metadataCache != nil {
				h.metadataCache.setLoaders(serverType, minecraftVersion, cached)
			}
			return cached, nil
		}
	}

	var loaders []loaderOption
	var err error
	switch serverType {
	case "fabric":
		loaders, err = h.fetchFabricLoaders(r, minecraftVersion)
	case "forge":
		var promotions forgePromotions
		promotions, _ = h.fetchForgePromotions(r, refresh)
		var versions []string
		versions, err = h.fetchMavenVersions(r, "https://maven.minecraftforge.net/net/minecraftforge/forge/maven-metadata.xml", refresh)
		if err == nil {
			loaders = firstN(uniqueLoaders(append(forgePromotedVersions(promotions, minecraftVersion), forgeVersionsForMinecraft(versions, minecraftVersion)...)), 80)
		}
	case "neoforge":
		var versions []string
		versions, err = h.fetchMavenVersions(r, "https://maven.neoforged.net/releases/net/neoforged/neoforge/maven-metadata.xml", refresh)
		if err == nil {
			loaders = firstN(neoforgeVersionsForMinecraft(versions, minecraftVersion), 80)
		}
	}
	if err != nil {
		var cached []loaderOption
		if readJSONFile(h.loaderCachePath(serverType, minecraftVersion), &cached) == nil {
			if h.metadataCache != nil {
				h.metadataCache.setLoaders(serverType, minecraftVersion, cached)
			}
			return cached, nil
		}
		return nil, err
	}
	if loaders == nil {
		loaders = []loaderOption{}
	}
	if err := h.persistLoaderVersions(serverType, minecraftVersion, loaders); err != nil {
		return nil, err
	}
	if h.metadataCache != nil {
		h.metadataCache.setLoaders(serverType, minecraftVersion, loaders)
	}
	return loaders, nil
}

// getSupportedVersions returns the Minecraft versions supported by the given server type.
// The first return value is stable versions, the second is experimental versions.
func (h apiHandler) getSupportedVersions(r *http.Request, serverType string, refresh bool) ([]string, []string, error) {
	if !refresh && h.metadataCache != nil {
		if cached, ok := h.metadataCache.getTypeVersions(serverType); ok {
			expCached, _ := h.metadataCache.getTypeExpVersions(serverType)
			return cached, expCached, nil
		}
	}
	cachePath := h.typeVersionsCachePath(serverType)
	expCachePath := h.typeExpVersionsCachePath(serverType)
	if !refresh {
		var cached []string
		if readJSONFile(cachePath, &cached) == nil && len(cached) > 0 {
			var expCached []string
			_ = readJSONFile(expCachePath, &expCached)
			if h.metadataCache != nil {
				h.metadataCache.setTypeVersions(serverType, cached)
				h.metadataCache.setTypeExpVersions(serverType, expCached)
			}
			return cached, expCached, nil
		}
	}

	var versions []string
	var expVersions []string
	var err error
	switch serverType {
	case "vanilla":
		versions, err = h.fetchVanillaSupportedVersions(r)
	case "paper":
		versions, expVersions, err = h.fetchPaperProjectVersions(r, "paper")
	case "folia":
		versions, expVersions, err = h.fetchPaperProjectVersions(r, "folia")
	case "purpur":
		versions, expVersions, err = h.fetchPurpurVersions(r)
	case "fabric":
		versions, err = h.fetchFabricGameVersions(r)
	case "forge":
		versions, err = h.fetchForgeMinecraftVersions(r, refresh)
	case "neoforge":
		versions, err = h.fetchNeoForgeMinecraftVersions(r, refresh)
	default:
		return nil, nil, errors.New("unsupported server type: " + serverType)
	}
	if err != nil {
		var cached []string
		if readJSONFile(cachePath, &cached) == nil && len(cached) > 0 {
			var expCached []string
			_ = readJSONFile(expCachePath, &expCached)
			if h.metadataCache != nil {
				h.metadataCache.setTypeVersions(serverType, cached)
				h.metadataCache.setTypeExpVersions(serverType, expCached)
			}
			return cached, expCached, nil
		}
		return nil, nil, err
	}
	if versions == nil {
		versions = []string{}
	}
	if expVersions == nil {
		expVersions = []string{}
	}
	_ = writeJSONFile(cachePath, versions)
	_ = writeJSONFile(expCachePath, expVersions)
	if h.metadataCache != nil {
		h.metadataCache.setTypeVersions(serverType, versions)
		h.metadataCache.setTypeExpVersions(serverType, expVersions)
	}
	return versions, expVersions, nil
}

func (h apiHandler) fetchVanillaSupportedVersions(r *http.Request) ([]string, error) {
	metadata, err := h.getMinecraftMetadata(r, false)
	if err != nil {
		return nil, err
	}
	// Return all release versions — the Mojang manifest already only lists
	// versions that exist. The provisioning step will check for server jar
	// availability per-version.
	versions := []string{}
	for _, v := range metadata.MinecraftVersions {
		if v.Type == "release" || v.Type == "snapshot" {
			versions = append(versions, v.ID)
		}
	}
	return versions, nil
}

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

func (h apiHandler) typeVersionsCachePath(serverType string) string {
	return filepath.Join(h.config.DataDir, "cache", "type-versions", serverType+".json")
}

func (h apiHandler) typeExpVersionsCachePath(serverType string) string {
	return filepath.Join(h.config.DataDir, "cache", "type-versions", serverType+"-experimental.json")
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

func validServerType(value string) bool {
	switch value {
	case "vanilla", "paper", "purpur", "folia", "fabric", "forge", "neoforge":
		return true
	default:
		return false
	}
}

func serverTypeNeedsLoader(serverType string) bool {
	switch serverType {
	case "fabric", "forge", "neoforge":
		return true
	default:
		return false
	}
}

func serverTypeNeedsPlugins(serverType string) bool {
	switch serverType {
	case "paper", "purpur", "folia":
		return true
	default:
		return false
	}
}

func fetchJSON(r *http.Request, requestURL string, target any) error {
	response, err := fetchResponse(r, requestURL)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New(requestURL + " returned " + strconv.Itoa(response.StatusCode))
	}
	return decodeBoundedJSON(response.Body, target)
}

func fetchText(r *http.Request, requestURL string) (string, error) {
	response, err := fetchResponse(r, requestURL)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", errors.New(requestURL + " returned " + strconv.Itoa(response.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxExternalResponseBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxExternalResponseBytes {
		return "", errors.New("External response is too large")
	}
	return string(data), nil
}

func decodeBoundedJSON(reader io.Reader, target any) error {
	limited := &io.LimitedReader{R: reader, N: maxExternalResponseBytes + 1}
	if err := json.NewDecoder(limited).Decode(target); err != nil {
		if limited.N <= 0 {
			return errors.New("External response is too large")
		}
		return err
	}
	if limited.N <= 0 {
		return errors.New("External response is too large")
	}
	return nil
}

func fetchResponse(r *http.Request, requestURL string) (*http.Response, error) {
	response, err := fetchResponseWithClient(r, requestURL, externalHTTPClient)
	if err == nil || r.Context().Err() != nil {
		return response, err
	}
	ipv4Response, ipv4Err := fetchResponseWithClient(r, requestURL, externalIPv4HTTPClient)
	if ipv4Err == nil {
		return ipv4Response, nil
	}
	return nil, errors.Join(err, ipv4Err)
}

func fetchResponseWithClient(r *http.Request, requestURL string, client *http.Client) (*http.Response, error) {
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "cliff/0.1.0")
	return client.Do(request)
}

func copyBoundedDownload(output io.Writer, input io.Reader, maxBytes int64) error {
	written, err := io.Copy(output, io.LimitReader(input, maxBytes+1))
	if err != nil {
		return err
	}
	if written > maxBytes {
		return errors.New("Download is too large")
	}
	return nil
}

func externalHTTPTransport(forceIPv4 bool) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = externalResponseHeaderTimeout
	if forceIPv4 {
		transport.DialContext = func(ctx context.Context, network string, address string) (net.Conn, error) {
			dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
			return dialer.DialContext(ctx, "tcp4", address)
		}
	}
	return transport
}

func readJSONFile(filePath string, target any) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func writeJSONFile(filePath string, value any) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, data, 0o644)
}

func (h apiHandler) minecraftMetadataCachePath() string {
	return filepath.Join(h.config.DataDir, "cache", "minecraft-metadata.json")
}

func (h apiHandler) loaderCachePath(serverType string, minecraftVersion string) string {
	safeVersion := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, minecraftVersion)
	return filepath.Join(h.config.DataDir, "cache", "loaders", serverType+"-"+safeVersion+".json")
}

func (h apiHandler) persistLoaderVersions(serverType string, minecraftVersion string, loaders []loaderOption) error {
	return writeJSONFile(h.loaderCachePath(serverType, minecraftVersion), loaders)
}
