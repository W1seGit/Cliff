package httpserver

import (
	"bufio"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

// protectedItem is a file or folder that Safe mode keeps from being moved,
// renamed or deleted, because the server would stop working without it.
type protectedItem struct {
	Path   string
	Reason string
	// Tree protects everything inside the folder as well.
	Tree bool
}

// launcherNames are the top-level files a server is started with or configured by.
var launcherNames = map[string]string{
	"run.bat":           "Launcher script",
	"run.sh":            "Launcher script",
	"run.ps1":           "Launcher script",
	"start.bat":         "Launcher script",
	"start.sh":          "Launcher script",
	"start.cmd":         "Launcher script",
	"launch.bat":        "Launcher script",
	"launch.sh":         "Launcher script",
	"eula.txt":          "Minecraft EULA",
	"server.properties": "Server settings",
	"user_jvm_args.txt": "Java arguments",
}

// serverProtections lists what Safe mode protects for one server.
func serverProtections(server store.Server) []protectedItem {
	items := []protectedItem{
		{Path: "libraries", Reason: "Server libraries", Tree: true},
		{Path: "versions", Reason: "Server versions", Tree: true},
		{Path: ".fabric", Reason: "Fabric files", Tree: true},
		{Path: "mods", Reason: "Mods folder"},
		{Path: "plugins", Reason: "Plugins folder"},
	}
	for name, reason := range launcherNames {
		items = append(items, protectedItem{Path: name, Reason: reason})
	}
	if jar := strings.TrimSpace(filepath.ToSlash(server.LaunchJar)); jar != "" {
		items = append(items, protectedItem{Path: jar, Reason: "Server launch file"})
	}
	level := levelName(server.Path)
	for _, suffix := range []string{"", "_nether", "_the_end"} {
		items = append(items, protectedItem{Path: level + suffix, Reason: "World folder"})
	}
	return items
}

// levelName reads the world folder name from server.properties, defaulting to "world".
func levelName(serverPath string) string {
	file, err := os.Open(filepath.Join(serverPath, "server.properties"))
	if err != nil {
		return "world"
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") {
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok && strings.TrimSpace(key) == "level-name" {
			if name := strings.TrimSpace(value); name != "" {
				return name
			}
		}
	}
	return "world"
}

// protectionReason returns why a server-relative path is protected, or "".
func protectionReason(items []protectedItem, relative string) string {
	relative = strings.Trim(filepath.ToSlash(relative), "/")
	if relative == "" {
		return ""
	}
	lower := strings.ToLower(relative)
	// Launcher and server jars sit at the top of the server folder.
	if !strings.Contains(relative, "/") && strings.HasSuffix(lower, ".jar") {
		return "Server jar"
	}
	for _, item := range items {
		path := strings.ToLower(strings.Trim(item.Path, "/"))
		if lower == path || (item.Tree && strings.HasPrefix(lower, path+"/")) {
			return item.Reason
		}
	}
	return ""
}

// refuseProtected answers 403 when Safe mode is on and any path is protected.
// Safe mode is on unless the request says otherwise.
func refuseProtected(w http.ResponseWriter, server store.Server, safeMode *bool, verb string, relativePaths ...string) bool {
	if safeMode != nil && !*safeMode {
		return false
	}
	items := serverProtections(server)
	for _, relative := range relativePaths {
		if reason := protectionReason(items, relative); reason != "" {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": fmt.Sprintf("%s is protected by Safe mode (%s) and cannot be %s. Turn Safe mode off to change it.", filepath.Base(relative), strings.ToLower(reason), verb),
				"code":  "protected",
			})
			return true
		}
	}
	return false
}

// validFileName rejects names that are not a single, portable file name.
func validFileName(name string) error {
	if strings.TrimSpace(name) == "" || name == "." || name == ".." {
		return errors.New("Enter a name")
	}
	if len(name) > 255 {
		return errors.New("That name is too long")
	}
	if strings.ContainsAny(name, "/\\:*?\"<>|") {
		return errors.New(`A name cannot contain / \ : * ? " < > |`)
	}
	for _, r := range name {
		if r < 32 {
			return errors.New("A name cannot contain control characters")
		}
	}
	if strings.HasSuffix(name, " ") || strings.HasSuffix(name, ".") {
		return errors.New("A name cannot end with a space or a dot")
	}
	return nil
}

// renameManagedPath renames a file or folder within its own folder.
func renameManagedPath(root string, target string, newName string) (string, error) {
	if filepath.Clean(target) == filepath.Clean(root) {
		return "", errors.New("The server folder cannot be renamed here")
	}
	if err := validFileName(newName); err != nil {
		return "", err
	}
	source, err := os.Lstat(target)
	if err != nil {
		return "", errors.New("File or folder not found")
	}
	destination := filepath.Join(filepath.Dir(target), newName)
	if filepath.Clean(destination) == filepath.Clean(target) {
		return toRelative(root, target), nil
	}
	if existing, err := os.Lstat(destination); err == nil && !os.SameFile(source, existing) {
		return "", fmt.Errorf("%s already exists in this folder", newName)
	}
	if err := os.Rename(target, destination); err != nil {
		return "", err
	}
	return toRelative(root, destination), nil
}

// moveManagedPaths moves files and folders into another folder of the same
// server. Everything is checked first, so a conflict moves nothing.
func moveManagedPaths(root string, sources []string, destination string) ([]string, error) {
	if len(sources) == 0 {
		return nil, errors.New("Select something to move")
	}
	destDir, err := resolveInside(root, destination)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(destDir); err != nil || !info.IsDir() {
		return nil, errors.New("The destination folder was not found")
	}
	type step struct{ from, to string }
	steps := []step{}
	seen := map[string]bool{}
	for _, item := range sources {
		from, err := resolveInside(root, item)
		if err != nil {
			return nil, err
		}
		if filepath.Clean(from) == filepath.Clean(root) {
			return nil, errors.New("The server folder cannot be moved")
		}
		info, err := os.Lstat(from)
		if err != nil {
			return nil, errors.New("File or folder not found: " + toRelative(root, from))
		}
		base := filepath.Base(from)
		if info.IsDir() && (filepath.Clean(destDir) == filepath.Clean(from) || strings.HasPrefix(filepath.Clean(destDir), filepath.Clean(from)+string(os.PathSeparator))) {
			return nil, fmt.Errorf("A folder cannot be moved into itself (%s)", base)
		}
		if filepath.Clean(filepath.Dir(from)) == filepath.Clean(destDir) {
			return nil, fmt.Errorf("%s is already in that folder", base)
		}
		to := filepath.Join(destDir, base)
		if _, err := os.Lstat(to); err == nil {
			return nil, fmt.Errorf("%s already exists in the destination folder", base)
		}
		key := strings.ToLower(base)
		if seen[key] {
			return nil, fmt.Errorf("Two of the selected items are both called %s", base)
		}
		seen[key] = true
		steps = append(steps, step{from, to})
	}
	moved := make([]string, 0, len(steps))
	for _, s := range steps {
		if err := os.Rename(s.from, s.to); err != nil {
			return moved, err
		}
		moved = append(moved, toRelative(root, s.to))
	}
	return moved, nil
}
