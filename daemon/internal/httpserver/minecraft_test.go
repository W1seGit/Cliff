package httpserver

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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

func TestExternalHTTPTransportsHaveBoundedHeaderTimeout(t *testing.T) {
	for name, transport := range map[string]*http.Transport{
		"default": externalHTTPTransport(false),
		"ipv4":    externalHTTPTransport(true),
	} {
		if transport.ResponseHeaderTimeout != externalResponseHeaderTimeout {
			t.Fatalf("%s transport header timeout = %s, want %s", name, transport.ResponseHeaderTimeout, externalResponseHeaderTimeout)
		}
		if transport.ResponseHeaderTimeout <= 0 || transport.ResponseHeaderTimeout > 30*time.Second {
			t.Fatalf("%s transport header timeout should be bounded, got %s", name, transport.ResponseHeaderTimeout)
		}
	}
}

func TestFetchTextRejectsOversizedResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", int(maxExternalResponseBytes)+1)))
	}))
	defer server.Close()

	_, err := fetchText(httptest.NewRequest(http.MethodGet, "/", nil), server.URL)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected oversized response error, got %v", err)
	}
}

func TestDecodeBoundedJSONRejectsOversizedResponses(t *testing.T) {
	var target map[string]string
	oversized := `{"value":"` + strings.Repeat("x", int(maxExternalResponseBytes)+1) + `"}`

	err := decodeBoundedJSON(strings.NewReader(oversized), &target)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected oversized JSON error, got %v", err)
	}
}

func TestCopyBoundedDownloadAllowsExactLimit(t *testing.T) {
	var output bytes.Buffer
	err := copyBoundedDownload(&output, strings.NewReader("12345"), 5)
	if err != nil {
		t.Fatalf("expected exact limit to succeed, got %v", err)
	}
	if output.String() != "12345" {
		t.Fatalf("unexpected output: %q", output.String())
	}
}

func TestCopyBoundedDownloadRejectsOversizedData(t *testing.T) {
	var output bytes.Buffer
	err := copyBoundedDownload(&output, strings.NewReader("123456"), 5)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected oversized download error, got %v", err)
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

func TestPaperServerTypeDoesNotUseLoaderVersions(t *testing.T) {
	if !validServerType("paper") {
		t.Fatal("paper should be a valid server type")
	}
	if serverTypeNeedsLoader("paper") {
		t.Fatal("paper should not use Fabric, Forge, or NeoForge loader versions")
	}
}

func TestDetectServerTypeFromPathFindsPaperJar(t *testing.T) {
	serverDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(serverDir, "paper-1.21.8-45.jar"), []byte("jar"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := detectServerTypeFromPath(serverDir); got != "paper" {
		t.Fatalf("expected paper server type, got %q", got)
	}
}

func TestDetectLaunchJarSkipsInstallerOnlyFabric(t *testing.T) {
	serverDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(serverDir, "fabric-installer-1.1.1.jar"), []byte("jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Only an installer jar is present — detectLaunchJar should return ""
	// rather than picking the installer as the launch target.
	if got := detectLaunchJar(serverDir, "fabric"); got != "" {
		t.Fatalf("detectLaunchJar should return empty when only installer jar present, got %q", got)
	}
}

func TestDetectLaunchJarPrefersFabricServerLaunch(t *testing.T) {
	serverDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(serverDir, "fabric-installer-1.1.1.jar"), []byte("jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(serverDir, "fabric-server-launch.jar"), []byte("jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectLaunchJar(serverDir, "fabric"); got != "fabric-server-launch.jar" {
		t.Fatalf("detectLaunchJar should prefer fabric-server-launch.jar, got %q", got)
	}
}

func TestScanImportedFabricServerReadsLibrariesAndJarManifest(t *testing.T) {
	serverDir := t.TempDir()
	writeTestJar(t, filepath.Join(serverDir, "fabric-installer-1.1.1.jar"), "net.fabricmc.installer.Main")
	writeTestJar(t, filepath.Join(serverDir, "fabric-server-launch.jar"), "net.fabricmc.loader.impl.launch.server.FabricServerLauncher")
	mustMkdir(t, filepath.Join(serverDir, "libraries", "net", "fabricmc", "fabric-loader", "0.16.10"))
	if err := os.WriteFile(filepath.Join(serverDir, "libraries", "net", "fabricmc", "fabric-loader", "0.16.10", "fabric-loader-0.16.10.jar"), []byte("loader"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, filepath.Join(serverDir, "libraries", "net", "minecraft", "server", "1.21.4"))
	if err := os.WriteFile(filepath.Join(serverDir, "libraries", "net", "minecraft", "server", "1.21.4", "server-1.21.4.jar"), []byte("server"), 0o644); err != nil {
		t.Fatal(err)
	}

	scan := scanImportedServer(serverDir)
	if scan.ServerType != "fabric" || scan.MinecraftVersion != "1.21.4" || scan.LoaderVersion != "0.16.10" || scan.LaunchTarget != "fabric-server-launch.jar" {
		t.Fatalf("unexpected fabric scan: %#v", scan)
	}
}

func TestScanImportedForgeServerReadsScriptAndArgFile(t *testing.T) {
	serverDir := t.TempDir()
	argDir := filepath.Join(serverDir, "libraries", "net", "minecraftforge", "forge", "1.20.1-47.2.0")
	mustMkdir(t, argDir)
	if err := os.WriteFile(filepath.Join(argDir, "unix_args.txt"), []byte("--launchTarget forge_server"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "java @libraries/net/minecraftforge/forge/1.20.1-47.2.0/unix_args.txt nogui\n"
	if err := os.WriteFile(filepath.Join(serverDir, "run.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(serverDir, "forge-1.20.1-47.2.0-installer.jar"), []byte("installer"), 0o644); err != nil {
		t.Fatal(err)
	}

	scan := scanImportedServer(serverDir)
	if scan.ServerType != "forge" || scan.MinecraftVersion != "1.20.1" || scan.LoaderVersion != "47.2.0" || scan.LaunchTarget != "run.sh" {
		t.Fatalf("unexpected forge scan: %#v", scan)
	}
}

func TestScanImportedServerWarnsForInstallerOnly(t *testing.T) {
	serverDir := t.TempDir()
	writeTestJar(t, filepath.Join(serverDir, "fabric-installer-1.1.1.jar"), "net.fabricmc.installer.Main")

	scan := scanImportedServer(serverDir)
	if scan.ServerType != "fabric" || scan.LaunchTarget != "" || len(scan.Warnings) == 0 {
		t.Fatalf("expected installer-only fabric warning, got %#v", scan)
	}
}

func TestDetectLaunchJarSkipsForgeInstaller(t *testing.T) {
	serverDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(serverDir, "forge-1.20.1-47.2.0-installer.jar"), []byte("jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(serverDir, "minecraft_server.1.20.1.jar"), []byte("jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectLaunchJar(serverDir, "forge"); got != "minecraft_server.1.20.1.jar" {
		t.Fatalf("detectLaunchJar should skip forge installer and pick server jar, got %q", got)
	}
}

func writeTestJar(t *testing.T, path string, mainClass string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	writer, err := archive.Create("META-INF/MANIFEST.MF")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("Manifest-Version: 1.0\nMain-Class: " + mainClass + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}
