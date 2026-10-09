package process

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxLaunchScriptBytes = 64 * 1024

// javaEnvironment returns the process environment with the given Java
// executable first on PATH and JAVA_HOME pointing at its install. Scripts such
// as Forge/NeoForge run.bat/run.sh call a bare `java`, which would otherwise
// resolve to whichever Java happens to be first on the system PATH (often an
// old one that cannot read @argfiles).
func javaEnvironment(javaPath string) []string {
	base := os.Environ()
	if javaPath == "" || !filepath.IsAbs(javaPath) {
		return base
	}
	binDir := filepath.Dir(javaPath)
	home := filepath.Dir(binDir)
	env := make([]string, 0, len(base)+2)
	pathValue := ""
	for _, entry := range base {
		key, value, _ := strings.Cut(entry, "=")
		switch {
		case strings.EqualFold(key, "PATH"):
			pathValue = value
		case strings.EqualFold(key, "JAVA_HOME"):
			// replaced below
		default:
			env = append(env, entry)
		}
	}
	env = append(env, "JAVA_HOME="+home)
	env = append(env, "PATH="+binDir+string(os.PathListSeparator)+pathValue)
	return env
}

// directScriptCommand reads a Forge/NeoForge style launch script and turns its
// `java ...` line into a command we can run without a shell. Running it
// directly lets us use the managed Java, apply the server's memory settings,
// skip the trailing `pause` (which would keep the process alive after a crash),
// and keep stdin free for console commands.
//
// It returns ok=false for scripts it cannot safely interpret (no java line, or
// shell variables it does not understand); callers then run the script as-is.
func directScriptCommand(scriptPath string, javaPath string, minMemoryMB int, maxMemoryMB int, jvmFlags []string, extraArgs []string) (string, []string, bool) {
	info, err := os.Stat(scriptPath)
	if err != nil || info.IsDir() || info.Size() > maxLaunchScriptBytes {
		return "", nil, false
	}
	raw, err := os.ReadFile(scriptPath)
	if err != nil {
		return "", nil, false
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || isScriptComment(trimmed) {
			continue
		}
		tokens := splitArgs(trimmed)
		if len(tokens) == 0 || !isJavaToken(tokens[0]) {
			continue
		}
		args := make([]string, 0, len(tokens)+4)
		for _, token := range tokens[1:] {
			switch token {
			case "%*", "$@", "$*", "\"$@\"":
				continue // replaced by our own arguments below
			}
			if strings.Contains(token, "%") || strings.Contains(token, "$") {
				return "", nil, false // variables we cannot expand safely
			}
			args = append(args, token)
		}
		command := tokens[0]
		if javaPath != "" && filepath.IsAbs(javaPath) {
			command = javaPath
		}
		var jvm []string
		if !hasArgPrefix(args, "-Xms") && minMemoryMB > 0 {
			jvm = append(jvm, fmt.Sprintf("-Xms%dM", minMemoryMB))
		}
		if !hasArgPrefix(args, "-Xmx") && maxMemoryMB > 0 {
			jvm = append(jvm, fmt.Sprintf("-Xmx%dM", maxMemoryMB))
		}
		jvm = append(jvm, jvmFlags...)
		full := append(jvm, args...)
		full = append(full, extraArgs...)
		if !hasNoGUIArg(full) {
			full = append(full, "nogui")
		}
		return command, full, true
	}
	return "", nil, false
}

func isScriptComment(line string) bool {
	lower := strings.ToLower(line)
	switch {
	case strings.HasPrefix(line, "#"), strings.HasPrefix(line, "::"):
		return true
	case lower == "rem", strings.HasPrefix(lower, "rem "), strings.HasPrefix(lower, "@rem"):
		return true
	case strings.HasPrefix(lower, "@echo"), strings.HasPrefix(lower, "echo"):
		return true
	case lower == "pause", strings.HasPrefix(lower, "exit"), lower == "@pause":
		return true
	}
	return false
}

func isJavaToken(token string) bool {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(token, "\\", "/")))
	base = strings.TrimSuffix(base, ".exe")
	return base == "java" || base == "javaw"
}

func hasArgPrefix(args []string, prefix string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return true
		}
	}
	return false
}
