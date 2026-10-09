package httpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type metadataCache struct {
	mu              sync.Mutex
	metadata        minecraftMetadata
	loaders         map[string][]loaderOption
	mavenVersions   map[string][]string
	forgePromotions *forgePromotions
	typeVersions    map[string][]string
	typeExpVersions map[string][]string
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

func (h apiHandler) typeVersionsCachePath(serverType string) string {
	return filepath.Join(h.config.DataDir, "cache", "type-versions", serverType+".json")
}

func (h apiHandler) typeExpVersionsCachePath(serverType string) string {
	return filepath.Join(h.config.DataDir, "cache", "type-versions", serverType+"-experimental.json")
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
