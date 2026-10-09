package httpserver

import (
	"strconv"
	"testing"
)

func TestMetadataCacheStoresMavenVersionsByCopy(t *testing.T) {
	cache := &metadataCache{}
	source := []string{"1.20.1-47.4.0", "1.20.1-47.3.0"}
	cache.setMavenVersions("https://example.invalid/maven-metadata.xml", source)
	source[0] = "mutated"

	first, ok := cache.getMavenVersions("https://example.invalid/maven-metadata.xml")
	if !ok {
		t.Fatal("expected cached Maven versions")
	}
	if first[0] != "1.20.1-47.4.0" {
		t.Fatalf("cache should not share input slice, got %#v", first)
	}

	first[0] = "mutated-again"
	second, ok := cache.getMavenVersions("https://example.invalid/maven-metadata.xml")
	if !ok {
		t.Fatal("expected cached Maven versions on second read")
	}
	if second[0] != "1.20.1-47.4.0" {
		t.Fatalf("cache should not share returned slice, got %#v", second)
	}
}

func TestMetadataCacheBoundsLoaderEntries(t *testing.T) {
	cache := &metadataCache{}
	for index := 0; index < maxMetadataLoaderCacheEntries+40; index++ {
		cache.setLoaders("fabric", "1."+strconv.Itoa(index), []loaderOption{{Version: strconv.Itoa(index), Stable: true}})
	}

	cache.mu.Lock()
	entryCount := len(cache.loaders)
	cache.mu.Unlock()
	if entryCount > maxMetadataLoaderCacheEntries {
		t.Fatalf("expected loader cache to stay at or below %d entries, got %d", maxMetadataLoaderCacheEntries, entryCount)
	}

	loaders, ok := cache.getLoaders("fabric", "1."+strconv.Itoa(maxMetadataLoaderCacheEntries+39))
	if !ok || len(loaders) != 1 || loaders[0].Version != strconv.Itoa(maxMetadataLoaderCacheEntries+39) {
		t.Fatalf("expected newest loader entry to be retained, got %#v ok=%v", loaders, ok)
	}
}

func TestMetadataCacheStoresForgePromotions(t *testing.T) {
	cache := &metadataCache{}
	cache.setForgePromotions(forgePromotions{Promos: map[string]string{"1.20.1-latest": "47.4.0"}})

	promotions, ok := cache.getForgePromotions()
	if !ok {
		t.Fatal("expected cached Forge promotions")
	}
	if promotions.Promos["1.20.1-latest"] != "47.4.0" {
		t.Fatalf("unexpected cached promotions: %#v", promotions.Promos)
	}
}
