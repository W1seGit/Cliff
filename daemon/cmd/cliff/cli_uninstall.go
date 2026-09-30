package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	rcBlockStart = "# >>> cliff >>>"
	rcBlockEnd   = "# <<< cliff <<<"
	// Older installers wrote these two lines without markers.
	legacyRCComment = "# Cliff CLI"
	legacyRCExport  = `export PATH="$HOME/.local/bin:$PATH"`
)

// isCliffInstall reports whether dir looks like a Cliff install. It guards
// uninstall from deleting an unrelated folder that happens to hold a cliff binary.
func isCliffInstall(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "package-manifest.json"))
	return err == nil && !info.IsDir()
}

// pathInside reports whether path is dir itself or lies within it.
func pathInside(path string, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// removeShellBlocks strips the PATH lines the installer added to a shell
// profile. It removes the marked block and the legacy two-line form, and
// leaves everything else untouched. The bool reports whether anything changed.
func removeShellBlocks(content string) (string, bool) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	kept := make([]string, 0, len(lines))
	changed := false

	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		switch {
		case trimmed == rcBlockStart:
			end := -1
			for j := i + 1; j < len(lines); j++ {
				if strings.TrimSpace(lines[j]) == rcBlockEnd {
					end = j
					break
				}
			}
			if end < 0 {
				kept = append(kept, lines[i])
				continue
			}
			i = end
			changed = true
			// Drop the blank separator the installer put before the block.
			if len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
				kept = kept[:len(kept)-1]
			}
		case trimmed == legacyRCComment && i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == legacyRCExport:
			i++
			changed = true
			if len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
				kept = kept[:len(kept)-1]
			}
		default:
			kept = append(kept, lines[i])
		}
	}
	if !changed {
		return content, false
	}
	return strings.Join(kept, "\n"), true
}

// cleanShellProfiles removes the installer's PATH lines from the user's shell files.
func cleanShellProfiles() []string {
	home := homeDir()
	if home == "" || runtime.GOOS == "windows" {
		return nil
	}
	cleaned := []string{}
	for _, name := range []string{".zshrc", ".zprofile", ".bashrc", ".bash_profile", ".profile"} {
		path := filepath.Join(home, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		updated, changed := removeShellBlocks(string(raw))
		if !changed {
			continue
		}
		if err := os.WriteFile(path, []byte(updated), 0o644); err == nil {
			cleaned = append(cleaned, path)
		}
	}
	fish := filepath.Join(home, ".config", "fish", "conf.d", "cliff.fish")
	if raw, err := os.ReadFile(fish); err == nil && strings.Contains(string(raw), "cliff") {
		if os.Remove(fish) == nil {
			cleaned = append(cleaned, fish)
		}
	}
	return cleaned
}

// removePathLinks removes `cliff` links that point at this install, and nothing else.
func removePathLinks(installedBinary string) []string {
	if runtime.GOOS == "windows" {
		return nil
	}
	dirs := []string{"/usr/local/bin", "/opt/homebrew/bin", filepath.Join(homeDir(), ".local", "bin"), filepath.Join(homeDir(), "bin")}
	resolvedBinary, err := filepath.EvalSymlinks(installedBinary)
	if err != nil {
		resolvedBinary = installedBinary
	}
	removed := []string{}
	for _, dir := range dirs {
		link := filepath.Join(dir, "cliff")
		info, err := os.Lstat(link)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		target, err := filepath.EvalSymlinks(link)
		if err != nil {
			// A dangling link is ours if it used to point into this install.
			raw, readErr := os.Readlink(link)
			if readErr != nil || !pathInside(raw, filepath.Dir(installedBinary)) {
				continue
			}
		} else if target != resolvedBinary {
			continue
		}
		if os.Remove(link) == nil {
			removed = append(removed, link)
		}
	}
	return removed
}

func confirmPrompt(question string) bool {
	return strings.EqualFold(strings.TrimSpace(readLine(question)), "yes")
}

// ---- cliff uninstall ----

// countServerFolders counts the folders directly inside the servers folder.
func countServerFolders(serverRoot string) int {
	entries, err := os.ReadDir(serverRoot)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			count++
		}
	}
	return count
}

// askAboutData makes the user choose what happens to their servers and
// settings. It returns keep=true to leave them alone, and ok=false to cancel.
func askAboutData(root string, dataPaths []string, serverRoot string) (keep bool, ok bool) {
	fmt.Println()
	fmt.Println("Your servers, worlds and settings are stored here:")
	fmt.Printf("  Data and settings:   %s\n", dataPaths[0])
	fmt.Printf("  Minecraft servers:   %s\n", dataPaths[1])
	fmt.Printf("The Cliff program itself lives in %s\n", root)
	fmt.Println()
	fmt.Println("What should happen to your servers and settings?")
	fmt.Println()
	fmt.Println("  [1] Keep them. Remove only the Cliff program and its dashboard.")
	fmt.Println("      Your servers, worlds and settings stay exactly where they are.")
	fmt.Println("      Install Cliff again later and it can use them as they are.")
	fmt.Println()
	fmt.Println("  [2] Delete everything, including all servers and worlds.")
	fmt.Println("      Nothing is left behind. This cannot be undone.")
	fmt.Println()
	fmt.Println("  [3] Cancel.")
	fmt.Println()
	switch strings.TrimSpace(readLine("Choose 1, 2 or 3: ")) {
	case "1":
		return true, true
	case "2":
		count := countServerFolders(serverRoot)
		fmt.Printf("\nThis permanently deletes %d server folder(s), their worlds, and Cliff's settings and database.\n", count)
		if strings.TrimSpace(readLine("Type 'delete' to confirm: ")) != "delete" {
			return false, false
		}
		return false, true
	}
	return false, false
}

// stdinReader is shared so answers typed or piped one after another are not
// lost between prompts.
var stdinReader = bufio.NewReader(os.Stdin)

func readLine(prompt string) string {
	fmt.Print(prompt)
	line, _ := stdinReader.ReadString('\n')
	return line
}

func runUninstall(args []string) {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	var yes bool
	var keepData bool
	var deleteData bool
	fs.BoolVar(&yes, "yes", false, "skip the confirmation prompts (needs --keep-data or --delete-data)")
	fs.BoolVar(&yes, "y", false, "skip the confirmation prompts (shorthand)")
	fs.BoolVar(&keepData, "keep-data", false, "remove only the program; keep servers, worlds and settings")
	fs.BoolVar(&deleteData, "delete-data", false, "also delete all servers, worlds and settings")
	fs.Parse(args)

	if keepData && deleteData {
		fmt.Fprintln(os.Stderr, "Choose one: --keep-data keeps your servers, --delete-data removes them.")
		os.Exit(1)
	}

	root := installRoot()
	self, _ := os.Executable()
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}

	// Never delete a folder that is not a real Cliff install.
	if !isCliffInstall(root) {
		fmt.Fprintf(os.Stderr, "Refusing to uninstall: %s does not look like a Cliff install (package-manifest.json is missing).\n", root)
		fmt.Fprintln(os.Stderr, "Nothing was removed. Run the 'cliff' from your Cliff install folder, or delete that folder yourself.")
		os.Exit(1)
	}

	dataDir := defaultDataDir()
	info := findDaemon(dataDir)
	serverRoot := resolveCLIPath(os.Getenv("CLIFF_SERVER_ROOT"), serverRootFallback(root))
	if info != nil && info.State != nil {
		if info.State.DataDir != "" {
			dataDir = resolveCLIPath(info.State.DataDir, dataDir)
		}
		if info.State.ServerRoot != "" {
			serverRoot = resolveCLIPath(info.State.ServerRoot, serverRoot)
		}
	}

	// User data may live outside the install folder (a custom --data-dir or --server-root).
	dataPaths := []string{dataDir, serverRoot}
	external := []string{}
	for _, path := range dataPaths {
		if !pathInside(path, root) {
			external = append(external, path)
		}
	}

	// A script has to say what it means; only a person at the keyboard is asked.
	if !keepData && !deleteData {
		if yes {
			fmt.Fprintln(os.Stderr, "Say what should happen to your servers and settings:")
			fmt.Fprintln(os.Stderr, "  --keep-data     remove only the program, keep servers, worlds and settings")
			fmt.Fprintln(os.Stderr, "  --delete-data   also delete all servers, worlds and settings")
			os.Exit(1)
		}
		keep, ok := askAboutData(root, dataPaths, serverRoot)
		if !ok {
			fmt.Println("Uninstall cancelled. Nothing was changed.")
			return
		}
		keepData, deleteData = keep, !keep
		yes = true // the choice above already was the confirmation
	}

	if keepData {
		fmt.Println("Removing the Cliff program. Your servers, worlds and settings will be kept:")
		for _, path := range dataPaths {
			fmt.Printf("  %s\n", path)
		}
	} else {
		fmt.Println("Removing Cliff and ALL of its data, including servers and worlds:")
		for _, path := range dataPaths {
			fmt.Printf("  %s\n", path)
		}
	}
	if info != nil {
		fmt.Println("Cliff is running and will be stopped first.")
	}
	if !yes {
		prompt := "Type 'yes' to continue: "
		if deleteData {
			prompt = "Type 'delete' to delete everything: "
		}
		answer := strings.TrimSpace(readLine(prompt))
		if (deleteData && answer != "delete") || (keepData && answer != "yes") {
			fmt.Println("Uninstall cancelled. Nothing was changed.")
			return
		}
	}

	if info != nil {
		fmt.Printf("Stopping Cliff (PID %d)...\n", info.PID)
		if err := stopDaemon(info, dataDir); err != nil {
			fmt.Fprintf(os.Stderr, "Could not stop Cliff: %s. Nothing was removed.\n", err)
			os.Exit(1)
		}
	}

	// Undo what the installer added to PATH.
	for _, path := range removePathLinks(self) {
		fmt.Printf("Removed: %s\n", path)
	}
	for _, path := range cleanShellProfiles() {
		fmt.Printf("Cleaned PATH entry in: %s\n", path)
	}
	if runtime.GOOS == "windows" {
		if err := removeInstallFromUserPath(root); err != nil {
			fmt.Fprintf(os.Stderr, "Could not update your PATH: %s\n", err)
		} else {
			fmt.Println("Removed Cliff from your PATH.")
		}
	}

	keep := []string{}
	if keepData {
		keep = dataPaths
	}
	failures := removeInstallEntries(root, self, keep)

	if !keepData {
		for _, path := range external {
			if !safeToRemoveExternal(path) {
				fmt.Fprintf(os.Stderr, "Not removing %s: it is too broad to delete safely. Remove it yourself if you want it gone.\n", path)
				continue
			}
			if err := os.RemoveAll(path); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to remove %s: %s\n", path, err)
				failures++
			}
		}
	}

	if runtime.GOOS == "windows" {
		// The running cliff.exe is locked; a short-lived helper removes it after we exit.
		if scheduleSelfDelete(root, keep) {
			fmt.Println("Cliff has been uninstalled. The last files are removed a moment after this window closes.")
		} else {
			fmt.Printf("Cliff has been uninstalled, but %s could not be deleted automatically. Delete it yourself.\n", root)
		}
	} else if failures == 0 {
		// Removes the folder only when nothing was kept inside it.
		_ = os.Remove(root)
		fmt.Println("Cliff has been uninstalled.")
	} else {
		fmt.Fprintf(os.Stderr, "Some files could not be removed. Delete manually: %s\n", root)
		os.Exit(1)
	}
	if keepData {
		printKeptData(root, dataDir, serverRoot)
	}
	if runtime.GOOS != "windows" {
		fmt.Println("Open a new terminal so the PATH change takes effect.")
	}
}

// printKeptData says what was left behind and how to use it again.
func printKeptData(root string, dataDir string, serverRoot string) {
	fmt.Println()
	fmt.Println("Your servers, worlds and settings were kept:")
	fmt.Printf("  Data and settings:   %s\n", dataDir)
	fmt.Printf("  Minecraft servers:   %s\n", serverRoot)
	fmt.Println("To use them again, install Cliff and either:")
	if pathInside(dataDir, root) && pathInside(serverRoot, root) {
		fmt.Printf("  - install into the same folder (%s), and Cliff picks them up by itself, or\n", root)
	}
	fmt.Printf("  - install anywhere and add:  --data-dir \"%s\" --server-root \"%s\"\n", dataDir, serverRoot)
}

// removeInstallEntries deletes everything in root except the running binary on
// Windows and anything that is, or contains, a path in keep. It returns the
// number of entries that could not be removed.
func removeInstallEntries(root string, self string, keep []string) int {
	entries, err := os.ReadDir(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read install directory: %s\n", err)
		return 1
	}
	failures := 0
	for _, entry := range entries {
		entryPath := filepath.Join(root, entry.Name())
		if runtime.GOOS == "windows" && strings.EqualFold(entryPath, self) {
			continue
		}
		if entryHoldsKeptPath(entryPath, keep) {
			continue
		}
		if err := os.RemoveAll(entryPath); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to remove %s: %s\n", entryPath, err)
			failures++
		}
	}
	return failures
}

func entryHoldsKeptPath(entry string, keep []string) bool {
	for _, path := range keep {
		if pathInside(path, entry) {
			return true
		}
	}
	return false
}

// safeToRemoveExternal refuses paths that would wipe far more than Cliff data,
// such as a drive root, the home folder, or a parent of the home folder.
func safeToRemoveExternal(path string) bool {
	clean := filepath.Clean(path)
	if filepath.Dir(clean) == clean {
		return false
	}
	if home := homeDir(); home != "" && pathInside(filepath.Clean(home), clean) {
		return false
	}
	return true
}
