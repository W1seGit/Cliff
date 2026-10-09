package httpserver

import (
	"path/filepath"
	"runtime"
	"testing"
)

const (
	fabricLauncherClass = "net.fabricmc.loader.impl.launch.server.FabricServerLauncher"
	fabricInstallerMain = "net.fabricmc.installer.Main"
)

// writeTestJar writes a jar whose manifest names mainClass.
func writeTestJar(t *testing.T, path string, mainClass string) {
	t.Helper()
	writeZip(t, path, map[string]string{
		"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\nMain-Class: " + mainClass + "\n",
	})
}

func TestDetectServerTypeFromPathFindsPaperJar(t *testing.T) {
	serverDir := t.TempDir()
	touch(t, filepath.Join(serverDir, "paper-1.21.8-45.jar"), "jar")

	if got := detectServerTypeFromPath(serverDir); got != "paper" {
		t.Fatalf("detectServerTypeFromPath = %q, want %q", got, "paper")
	}
}

func TestDetectLaunchJar(t *testing.T) {
	tests := []struct {
		name       string
		serverType string
		jars       []string
		want       string
	}{
		{"fabric with only an installer has no launch jar", "fabric", []string{"fabric-installer-1.1.1.jar"}, ""},
		{"fabric prefers fabric-server-launch over the installer", "fabric", []string{"fabric-installer-1.1.1.jar", "fabric-server-launch.jar"}, "fabric-server-launch.jar"},
		{"forge skips its installer and picks the server jar", "forge", []string{"forge-1.20.1-47.2.0-installer.jar", "minecraft_server.1.20.1.jar"}, "minecraft_server.1.20.1.jar"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			serverDir := t.TempDir()
			for _, jar := range tc.jars {
				touch(t, filepath.Join(serverDir, jar), "jar")
			}
			if got := detectLaunchJar(serverDir, tc.serverType); got != tc.want {
				t.Fatalf("detectLaunchJar(%s, %v) = %q, want %q", tc.serverType, tc.jars, got, tc.want)
			}
		})
	}
}

func TestScanImportedFabricServerReadsLibrariesAndJarManifest(t *testing.T) {
	serverDir := t.TempDir()
	writeTestJar(t, filepath.Join(serverDir, "fabric-installer-1.1.1.jar"), fabricInstallerMain)
	writeTestJar(t, filepath.Join(serverDir, "fabric-server-launch.jar"), fabricLauncherClass)
	touch(t, filepath.Join(serverDir, "libraries", "net", "fabricmc", "fabric-loader", "0.16.10", "fabric-loader-0.16.10.jar"), "loader")
	touch(t, filepath.Join(serverDir, "libraries", "net", "minecraft", "server", "1.21.4", "server-1.21.4.jar"), "server")

	scan := scanImportedServer(serverDir)
	if scan.ServerType != "fabric" || scan.MinecraftVersion != "1.21.4" || scan.LoaderVersion != "0.16.10" || scan.LaunchTarget != "fabric-server-launch.jar" {
		t.Fatalf("unexpected fabric scan: %#v", scan)
	}
}

func TestScanImportedForgeServerSelectsPlatformLaunchScript(t *testing.T) {
	serverDir := t.TempDir()
	argDir := filepath.Join(serverDir, "libraries", "net", "minecraftforge", "forge", "1.20.1-47.2.0")
	touch(t, filepath.Join(argDir, "unix_args.txt"), "--launchTarget forge_server")
	touch(t, filepath.Join(argDir, "win_args.txt"), "--launchTarget forge_server")
	touch(t, filepath.Join(serverDir, "run.sh"), "java @libraries/net/minecraftforge/forge/1.20.1-47.2.0/unix_args.txt nogui\n")
	touch(t, filepath.Join(serverDir, "run.bat"), "java @libraries/net/minecraftforge/forge/1.20.1-47.2.0/win_args.txt nogui\r\n")
	touch(t, filepath.Join(serverDir, "forge-1.20.1-47.2.0-installer.jar"), "installer")

	scan := scanImportedServer(serverDir)
	platformTarget := "run.sh"
	if runtime.GOOS == "windows" {
		platformTarget = "run.bat"
	}
	if scan.ServerType != "forge" || scan.MinecraftVersion != "1.20.1" || scan.LoaderVersion != "47.2.0" || scan.LaunchTarget != platformTarget {
		t.Fatalf("unexpected forge scan: %#v (want launch target %s)", scan, platformTarget)
	}
	for _, target := range []struct{ goos, want string }{
		{goos: "windows", want: "run.bat"},
		{goos: "linux", want: "run.sh"},
		{goos: "darwin", want: "run.sh"},
	} {
		if got := platformLaunchScript(serverDir, target.goos); got != target.want {
			t.Fatalf("platform %s should select %s, got %s", target.goos, target.want, got)
		}
	}
}

func TestScanImportedServerPrefersJarOverAbsolutePathScript(t *testing.T) {
	serverDir := t.TempDir()
	touch(t, filepath.Join(serverDir, "start.sh"), "/opt/homebrew/opt/openjdk@25/bin/java -jar fabric-server-launch.jar nogui\n")
	writeTestJar(t, filepath.Join(serverDir, "fabric-server-launch.jar"), fabricLauncherClass)

	scan := scanImportedServer(serverDir)
	if scan.ServerType != "fabric" || scan.LaunchTarget != "fabric-server-launch.jar" {
		t.Fatalf("expected jar launch target instead of start.sh, got %#v", scan)
	}
}

func TestScanImportedServerWarnsForInstallerOnly(t *testing.T) {
	serverDir := t.TempDir()
	writeTestJar(t, filepath.Join(serverDir, "fabric-installer-1.1.1.jar"), fabricInstallerMain)

	scan := scanImportedServer(serverDir)
	if scan.ServerType != "fabric" || scan.LaunchTarget != "" || len(scan.Warnings) == 0 {
		t.Fatalf("expected installer-only fabric warning, got %#v", scan)
	}
}
