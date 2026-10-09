package httpserver

import (
	"errors"
	"net/http"
	"regexp"
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
