package httpserver

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	javamanager "github.com/W1seGit/Cliff/daemon/internal/java"
	"github.com/W1seGit/Cliff/daemon/internal/store"
	"github.com/W1seGit/Cliff/daemon/internal/winproc"
)

func (h apiHandler) importSessionPath(token string) string {
	return filepath.Join(h.config.DataDir, "imports", token)
}

func newImportToken() (string, error) {
	data := make([]byte, 8)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return "imp_" + hex.EncodeToString(data), nil
}

func fileDisplayName(fileName string) string {
	name := strings.TrimSuffix(filepath.Base(fileName), filepath.Ext(fileName))
	if name == "" {
		return "Imported server"
	}
	return name
}

func (h apiHandler) provisionServer(r *http.Request, server *store.Server) (string, error) {
	if server.Type == "vanilla" {
		download, err := h.vanillaServerDownload(r, server.MinecraftVersion)
		if err != nil {
			return "", err
		}
		if err := downloadFile(r, download.URL, filepath.Join(server.Path, "server.jar")); err != nil {
			return "", err
		}
		server.LaunchJar = "server.jar"
		progressFrom(r).start("settings")
		if err := writeDefaultServerFiles(*server); err != nil {
			return "", err
		}
		return "Vanilla server jar downloaded", nil
	}
	if server.Type == "paper" {
		downloadURL, err := h.paperServerDownload(r, server.MinecraftVersion)
		if err != nil {
			return "", err
		}
		if err := downloadFile(r, downloadURL, filepath.Join(server.Path, "server.jar")); err != nil {
			return "", err
		}
		server.LaunchJar = "server.jar"
		progressFrom(r).start("settings")
		if err := writeDefaultServerFiles(*server); err != nil {
			return "", err
		}
		_ = os.MkdirAll(filepath.Join(server.Path, "plugins"), 0o755)
		return "Paper server jar downloaded", nil
	}
	if server.Type == "purpur" {
		downloadURL, err := h.purpurServerDownload(r, server.MinecraftVersion)
		if err != nil {
			return "", err
		}
		if err := downloadFile(r, downloadURL, filepath.Join(server.Path, "server.jar")); err != nil {
			return "", err
		}
		server.LaunchJar = "server.jar"
		progressFrom(r).start("settings")
		if err := writeDefaultServerFiles(*server); err != nil {
			return "", err
		}
		_ = os.MkdirAll(filepath.Join(server.Path, "plugins"), 0o755)
		return "Purpur server jar downloaded", nil
	}
	if server.Type == "folia" {
		downloadURL, err := h.foliaServerDownload(r, server.MinecraftVersion)
		if err != nil {
			return "", err
		}
		if err := downloadFile(r, downloadURL, filepath.Join(server.Path, "server.jar")); err != nil {
			return "", err
		}
		server.LaunchJar = "server.jar"
		progressFrom(r).start("settings")
		if err := writeDefaultServerFiles(*server); err != nil {
			return "", err
		}
		_ = os.MkdirAll(filepath.Join(server.Path, "plugins"), 0o755)
		return "Folia server jar downloaded", nil
	}
	if server.Type == "fabric" {
		installer, err := h.latestFabricInstaller(r)
		if err != nil {
			return "", err
		}
		requestURL := "https://meta.fabricmc.net/v2/versions/loader/" + url.PathEscape(server.MinecraftVersion) + "/" + url.PathEscape(server.LoaderVersion) + "/" + url.PathEscape(installer) + "/server/jar"
		if err := downloadFile(r, requestURL, filepath.Join(server.Path, "fabric-server-launch.jar")); err != nil {
			return "", err
		}
		server.LaunchJar = "fabric-server-launch.jar"
		progressFrom(r).start("settings")
		if err := writeDefaultServerFiles(*server); err != nil {
			return "", err
		}
		_ = os.MkdirAll(filepath.Join(server.Path, "mods"), 0o755)
		return "Fabric server launcher downloaded", nil
	}
	if server.Type == "forge" {
		fullVersion := server.MinecraftVersion + "-" + server.LoaderVersion
		installerName := "forge-" + fullVersion + "-installer.jar"
		requestURL := "https://maven.minecraftforge.net/net/minecraftforge/forge/" + url.PathEscape(fullVersion) + "/" + installerName
		if err := downloadFile(r, requestURL, filepath.Join(server.Path, installerName)); err != nil {
			return "", err
		}
		return h.provisionInstallerServer(r, server, installerName, "Forge")
	}
	if server.Type == "neoforge" {
		installerName := "neoforge-" + server.LoaderVersion + "-installer.jar"
		requestURL := "https://maven.neoforged.net/releases/net/neoforged/neoforge/" + url.PathEscape(server.LoaderVersion) + "/" + installerName
		if err := downloadFile(r, requestURL, filepath.Join(server.Path, installerName)); err != nil {
			return "", err
		}
		return h.provisionInstallerServer(r, server, installerName, "NeoForge")
	}
	return "", nil
}

func (h apiHandler) provisionInstallerServer(r *http.Request, server *store.Server, installerName string, loaderName string) (string, error) {
	progressFrom(r).start("settings")
	if err := writeDefaultServerFiles(*server); err != nil {
		return "", err
	}
	if err := h.runLoaderInstaller(r, *server, installerName); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(server.Path, "mods"), 0o755); err != nil {
		return "", err
	}

	server.LaunchJar = platformLaunchScript(server.Path, runtime.GOOS)
	if server.LaunchJar == "" {
		server.LaunchJar = detectLaunchJar(server.Path, server.Type)
	}
	if server.LaunchJar == "" {
		return "", fmt.Errorf("%s installer completed but did not create a launch script or server jar for %s", loaderName, runtime.GOOS)
	}
	return loaderName + " server installed", nil
}

func (h apiHandler) runLoaderInstaller(r *http.Request, server store.Server, installerName string) error {
	progressFrom(r).start("java")
	javaPath, err := (javamanager.Resolver{DataDir: h.config.DataDir}).Resolve(r.Context(), server.JavaPath, server.MinecraftVersion)
	if err != nil {
		return fmt.Errorf("managed Java setup failed: %w", err)
	}
	progressFrom(r).start("install")

	cmd := exec.CommandContext(r.Context(), javaPath, "-jar", installerName, "--installServer")
	winproc.Hide(cmd)
	cmd.Dir = server.Path
	output := &tailBuffer{limit: 16 << 10}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		if details := strings.TrimSpace(output.String()); details != "" {
			return fmt.Errorf("%s server installer failed: %w\n%s", server.Type, err, details)
		}
		return fmt.Errorf("%s server installer failed: %w", server.Type, err)
	}
	return nil
}

type tailBuffer struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func (b *tailBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	length := len(value)
	if b.limit <= 0 {
		return length, nil
	}
	if length >= b.limit {
		b.data = append(b.data[:0], value[length-b.limit:]...)
		return length, nil
	}
	if overflow := len(b.data) + length - b.limit; overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:len(b.data)-overflow]
	}
	b.data = append(b.data, value...)
	return length, nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}
