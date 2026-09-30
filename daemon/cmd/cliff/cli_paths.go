package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// pathsFileName remembers where this install keeps its data and servers, so a
// custom location chosen at install time is used by every later command.
const pathsFileName = "cliff-paths.json"

type savedPaths struct {
	DataDir    string `json:"dataDir,omitempty"`
	ServerRoot string `json:"serverRoot,omitempty"`
}

func readSavedPaths(root string) savedPaths {
	var paths savedPaths
	raw, err := os.ReadFile(filepath.Join(root, pathsFileName))
	if err != nil {
		return paths
	}
	_ = json.Unmarshal(raw, &paths)
	return paths
}

func writeSavedPaths(root string, paths savedPaths) error {
	path := filepath.Join(root, pathsFileName)
	if paths == (savedPaths{}) {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(paths, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// dataDirFallback is where data lives when no flag or environment variable says
// otherwise: the folder saved for this install, else <install>/data.
func dataDirFallback(root string) string {
	if saved := readSavedPaths(root).DataDir; saved != "" {
		return saved
	}
	return filepath.Join(root, "data")
}

// serverRootFallback is the same for the folder that holds the Minecraft servers.
func serverRootFallback(root string) string {
	if saved := readSavedPaths(root).ServerRoot; saved != "" {
		return saved
	}
	return filepath.Join(root, "servers")
}

// hasExistingData reports whether a folder already holds a Cliff database.
func hasExistingData(dataDir string) bool {
	info, err := os.Stat(filepath.Join(dataDir, "dashboard.sqlite"))
	return err == nil && !info.IsDir()
}

// ---- cliff configure ----

// runConfigure saves where this install keeps its data and servers. The
// installer calls it for --data-dir and --server-root; it can also be run by hand.
func runConfigure(args []string) {
	fs := flag.NewFlagSet("configure", flag.ExitOnError)
	var dataDir string
	var serverRoot string
	var clear bool
	var show bool
	fs.StringVar(&dataDir, "data-dir", "", "folder for Cliff's settings and database")
	fs.StringVar(&serverRoot, "server-root", "", "folder that holds your Minecraft servers")
	fs.BoolVar(&clear, "clear", false, "forget saved folders and go back to the defaults")
	fs.BoolVar(&show, "show", false, "print where data and servers are kept")
	fs.Parse(args)

	root := installRoot()
	saved := readSavedPaths(root)

	if show || (!clear && dataDir == "" && serverRoot == "") {
		fmt.Printf("Data and settings:  %s\n", resolveCLIPath(saved.DataDir, filepath.Join(root, "data")))
		fmt.Printf("Minecraft servers:  %s\n", resolveCLIPath(saved.ServerRoot, filepath.Join(root, "servers")))
		return
	}
	if clear {
		saved = savedPaths{}
	}
	if dataDir != "" {
		saved.DataDir = resolveCLIPath(dataDir, "")
	}
	if serverRoot != "" {
		saved.ServerRoot = resolveCLIPath(serverRoot, "")
	}
	for _, dir := range []string{saved.DataDir, saved.ServerRoot} {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "Could not use %s: %s\n", dir, err)
			os.Exit(1)
		}
	}
	if err := writeSavedPaths(root, saved); err != nil {
		fmt.Fprintf(os.Stderr, "Could not save the folders: %s\n", err)
		os.Exit(1)
	}

	data := resolveCLIPath(saved.DataDir, filepath.Join(root, "data"))
	servers := resolveCLIPath(saved.ServerRoot, filepath.Join(root, "servers"))
	if hasExistingData(data) {
		fmt.Printf("Using your existing Cliff data in %s\n", data)
	} else {
		fmt.Printf("Cliff will keep its data in %s (new)\n", data)
	}
	fmt.Printf("Minecraft servers are kept in %s\n", servers)
}
