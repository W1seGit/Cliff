package process

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/W1seGit/Cliff/daemon/internal/store"
	"github.com/W1seGit/Cliff/daemon/internal/winproc"
)

func launchCommand(server store.Server) (*exec.Cmd, []string, string, error) {
	if server.LaunchJar == "" {
		return nil, nil, "", errors.New("no launch target configured")
	}
	launchPath := filepath.Join(server.Path, server.LaunchJar)
	if _, err := os.Stat(launchPath); err != nil {
		return nil, nil, "", fmt.Errorf("launch target not found: %w", err)
	}

	lower := strings.ToLower(server.LaunchJar)
	if isInstallerLaunchJar(lower) {
		if replacement := detectBetterLaunchTarget(server.Path); replacement != "" {
			return nil, nil, "", fmt.Errorf("launch target %s is an installer jar, not a server launcher. Set the launch target to %s instead", server.LaunchJar, replacement)
		}
		return nil, nil, "", fmt.Errorf("launch target %s is an installer jar, not a server launcher. Run the installer first or choose the generated server launch target", server.LaunchJar)
	}
	var command string
	var args []string
	javaPath := strings.TrimSpace(server.JavaPath)
	isScript := (runtime.GOOS == "windows" && strings.HasSuffix(lower, ".bat")) || strings.HasSuffix(lower, ".sh")
	switch {
	case isScript:
		// Prefer running the script's own java line directly (managed Java, our
		// memory settings, no trailing pause). Fall back to running the script.
		if direct, directArgs, ok := directScriptCommand(launchPath, javaPath, server.MinMemoryMB, server.MaxMemoryMB, JvmPresetFlags(server.JvmPreset, server.MaxMemoryMB), splitArgs(server.ExtraArgs)); ok {
			command, args = direct, directArgs
		} else if strings.HasSuffix(lower, ".bat") {
			command = "cmd.exe"
			args = []string{"/c", launchPath}
		} else {
			command = "sh"
			args = []string{launchPath}
		}
	default:
		command = strings.TrimSpace(server.JavaPath)
		if command == "" || command == "auto" || strings.HasPrefix(command, "managed:") {
			command = "java"
		}
		args = []string{
			fmt.Sprintf("-Xms%dM", server.MinMemoryMB),
			fmt.Sprintf("-Xmx%dM", server.MaxMemoryMB),
		}
		args = append(args, JvmPresetFlags(server.JvmPreset, server.MaxMemoryMB)...)
		args = append(args, "-jar", server.LaunchJar)
		extraArgs := splitArgs(server.ExtraArgs)
		args = append(args, extraArgs...)
		if !hasNoGUIArg(extraArgs) {
			args = append(args, "nogui")
		}
	}

	cmd := exec.Command(command, args...)
	winproc.Hide(cmd)
	if isScript {
		// Scripts (and the java they call) must see the managed Java first.
		cmd.Env = javaEnvironment(javaPath)
	}
	return cmd, args, strings.Join(append([]string{command}, args...), " "), nil
}

func hasNoGUIArg(args []string) bool {
	for _, arg := range args {
		normalized := strings.TrimLeft(strings.ToLower(strings.TrimSpace(arg)), "-")
		if normalized == "nogui" {
			return true
		}
	}
	return false
}

// isInstallerLaunchJar reports whether the launch jar is a mod-loader
// installer rather than a Minecraft server jar. Installer jars don't
// accept the nogui flag.
func isInstallerLaunchJar(lower string) bool {
	return strings.Contains(lower, "installer")
}

// SuggestLaunchTarget returns a usable replacement when a persisted profile
// still points at a loader installer jar.
func SuggestLaunchTarget(serverPath string, launchTarget string) string {
	if !isInstallerLaunchJar(strings.ToLower(launchTarget)) {
		return ""
	}
	return detectBetterLaunchTarget(serverPath)
}

func detectBetterLaunchTarget(serverPath string) string {
	if target := detectPlatformLaunchScript(serverPath, runtime.GOOS); target != "" {
		return target
	}
	entries, err := os.ReadDir(serverPath)
	if err != nil {
		return ""
	}
	jars := []string{}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".jar") {
			jars = append(jars, entry.Name())
		}
	}
	for _, jar := range jars {
		if strings.EqualFold(jar, "fabric-server-launch.jar") {
			return jar
		}
	}
	for _, jar := range jars {
		lower := strings.ToLower(jar)
		if !isInstallerLaunchJar(lower) && strings.Contains(lower, "server") {
			return jar
		}
	}
	for _, jar := range jars {
		if !isInstallerLaunchJar(strings.ToLower(jar)) {
			return jar
		}
	}
	return ""
}

func detectPlatformLaunchScript(serverPath string, goos string) string {
	names := []string{"run.sh", "start.sh", "start.command", "server.sh"}
	if goos == "windows" {
		names = []string{"run.bat", "start.bat", "server.bat"}
	}
	for _, name := range names {
		info, err := os.Stat(filepath.Join(serverPath, name))
		if err == nil && !info.IsDir() {
			return name
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func splitArgs(input string) []string {
	args := []string{}
	current := strings.Builder{}
	quote := rune(0)
	escaping := false

	for _, char := range input {
		if escaping {
			current.WriteRune(char)
			escaping = false
			continue
		}
		if char == '\\' {
			escaping = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			} else {
				current.WriteRune(char)
			}
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			continue
		}
		if char == ' ' || char == '\t' || char == '\n' || char == '\r' {
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(char)
	}

	if escaping {
		current.WriteRune('\\')
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}
