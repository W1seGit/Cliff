package process

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

// writeFiles creates placeholder files in a new temp dir.
func writeFiles(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("placeholder"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// jarServer is a server that launches jar from dir.
func jarServer(dir string, jar string, extraArgs string) store.Server {
	return store.Server{
		Name:        "Launch Test",
		Path:        dir,
		JavaPath:    "java",
		MinMemoryMB: 512,
		MaxMemoryMB: 1024,
		LaunchJar:   jar,
		ExtraArgs:   extraArgs,
	}
}

func TestSplitArgsMatchesDashboardParser(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty", "", []string{}},
		{"surrounding spaces", "  --nogui  ", []string{"--nogui"}},
		{"double quotes", "--world \"My World\" --flag=value", []string{"--world", "My World", "--flag=value"}},
		{"single quotes", "--path 'C:/Minecraft Servers/Fabric' --safe", []string{"--path", "C:/Minecraft Servers/Fabric", "--safe"}},
		{"escapes", "--escaped one\\ two --quote \\\"literal\\\"", []string{"--escaped", "one two", "--quote", "\"literal\""}},
		{"dangling backslash", "--dangling slash\\", []string{"--dangling", "slash\\"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := splitArgs(tc.input); !slices.Equal(got, tc.want) {
				t.Fatalf("splitArgs(%q) = %#v, want %#v", tc.input, got, tc.want)
			}
		})
	}
}

func TestLaunchCommandPreservesQuotedExtraArgs(t *testing.T) {
	dir := writeFiles(t, "server.jar")

	_, args, commandText, err := launchCommand(jarServer(dir, "server.jar", `--world "My World" --path C:\Servers\One`))
	if err != nil {
		t.Fatal(err)
	}

	wantArgs := []string{"-Xms512M", "-Xmx1024M", "-jar", "server.jar", "--world", "My World", "--path", "C:ServersOne", "nogui"}
	if !slices.Equal(args, wantArgs) {
		t.Fatalf("launch args = %#v, want %#v", args, wantArgs)
	}
	if !strings.Contains(commandText, "My World") {
		t.Fatalf("command text did not include quoted arg value: %s", commandText)
	}
}

func TestLaunchCommandDoesNotDuplicateNoGUI(t *testing.T) {
	dir := writeFiles(t, "server.jar")

	_, args, _, err := launchCommand(jarServer(dir, "server.jar", "nogui"))
	if err != nil {
		t.Fatal(err)
	}

	count := 0
	for _, arg := range args {
		if strings.TrimLeft(strings.ToLower(arg), "-") == "nogui" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected one nogui arg, got %d in %#v", count, args)
	}
}

func TestLaunchCommandRejectsInstallerJars(t *testing.T) {
	installerJars := []string{
		"fabric-installer-1.1.1.jar",
		"forge-1.20.1-47.2.0-installer.jar",
		"neoforge-47.1.0-installer.jar",
	}
	for _, jar := range installerJars {
		t.Run(jar, func(t *testing.T) {
			dir := writeFiles(t, jar)
			_, _, _, err := launchCommand(jarServer(dir, jar, ""))
			if err == nil || !strings.Contains(err.Error(), "installer jar") {
				t.Fatalf("launching %s: error = %v, want one mentioning \"installer jar\"", jar, err)
			}
		})
	}
}

func TestLaunchCommandSuggestsBetterTargetForInstallerJar(t *testing.T) {
	dir := writeFiles(t, "fabric-installer-1.1.1.jar", "fabric-server-launch.jar")

	_, _, _, err := launchCommand(jarServer(dir, "fabric-installer-1.1.1.jar", ""))
	if err == nil || !strings.Contains(err.Error(), "fabric-server-launch.jar") {
		t.Fatalf("error = %v, want a suggestion naming fabric-server-launch.jar", err)
	}
}

func TestDetectPlatformLaunchScript(t *testing.T) {
	dir := writeFiles(t, "run.sh", "run.bat")
	for _, target := range []struct{ goos, want string }{
		{goos: "windows", want: "run.bat"},
		{goos: "linux", want: "run.sh"},
		{goos: "darwin", want: "run.sh"},
	} {
		if got := detectPlatformLaunchScript(dir, target.goos); got != target.want {
			t.Fatalf("platform %s should select %s, got %s", target.goos, target.want, got)
		}
	}
	platformTarget := "run.sh"
	if runtime.GOOS == "windows" {
		platformTarget = "run.bat"
	}
	if got := SuggestLaunchTarget(dir, "neoforge-installer.jar"); got != platformTarget {
		t.Fatalf("should suggest the current platform launcher %s, got %s", platformTarget, got)
	}
}
