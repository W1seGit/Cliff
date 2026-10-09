package httpserver

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

type importedServerScan struct {
	ServerType       string
	MinecraftVersion string
	LoaderVersion    string
	LaunchTarget     string
	Warnings         []string
}

type importJarCandidate struct {
	Name             string
	Lower            string
	MainClass        string
	ServerType       string
	MinecraftVersion string
	LoaderVersion    string
	Score            int
	Installer        bool
}

func detectServerTypeFromPath(serverPath string) string {
	return scanImportedServer(serverPath).ServerType
}

func detectMinecraftProfileFromPath(serverPath string, serverType string) (string, string) {
	scan := scanImportedServer(serverPath)
	if scan.ServerType != serverType {
		return "", ""
	}
	return scan.MinecraftVersion, scan.LoaderVersion
}

func detectLaunchJar(serverPath string, serverType string) string {
	scan := scanImportedServer(serverPath)
	if scan.ServerType != serverType && serverType != "" {
		if scan.ServerType == "vanilla" {
			return ""
		}
	}
	return scan.LaunchTarget
}

func scanImportedServer(serverPath string) importedServerScan {
	scan := importedServerScan{ServerType: "vanilla"}
	evidence := map[string]int{"vanilla": 1}
	topJars, _ := topLevelJars(serverPath)

	scriptName, scriptJar, scriptArgFile := detectLaunchScript(serverPath)
	if scriptJar != "" {
		evidenceFromName(filepath.Base(scriptJar), evidence, &scan)
	}
	if scriptArgFile != "" {
		evidenceFromArgFilePath(scriptArgFile, evidence, &scan)
	}

	if mc, loader := detectForgeProfileFromLibraries(serverPath); mc != "" || loader != "" {
		evidence["forge"] += 90
		scan.MinecraftVersion = firstNonEmpty(scan.MinecraftVersion, mc)
		scan.LoaderVersion = firstNonEmpty(scan.LoaderVersion, loader)
	}
	if mc, loader := detectNeoForgeProfileFromLibraries(serverPath); mc != "" || loader != "" {
		evidence["neoforge"] += 95
		scan.MinecraftVersion = firstNonEmpty(scan.MinecraftVersion, mc)
		scan.LoaderVersion = firstNonEmpty(scan.LoaderVersion, loader)
	}
	if mc, loader := detectFabricProfileFromLibraries(serverPath); mc != "" || loader != "" {
		evidence["fabric"] += 95
		scan.MinecraftVersion = firstNonEmpty(scan.MinecraftVersion, mc)
		scan.LoaderVersion = firstNonEmpty(scan.LoaderVersion, loader)
	}

	candidates := []importJarCandidate{}
	for _, jar := range topJars {
		candidate := inspectImportJar(serverPath, jar)
		candidates = append(candidates, candidate)
		evidenceFromJar(candidate, evidence, &scan)
	}

	scan.ServerType = strongestServerType(evidence)
	if scan.MinecraftVersion == "" || (serverTypeNeedsLoader(scan.ServerType) && scan.LoaderVersion == "") {
		if mc, loader := detectMinecraftProfileFromJars(candidates, scan.ServerType); mc != "" || loader != "" {
			scan.MinecraftVersion = firstNonEmpty(scan.MinecraftVersion, mc)
			scan.LoaderVersion = firstNonEmpty(scan.LoaderVersion, loader)
		}
	}

	if scan.LaunchTarget == "" {
		target, warning := bestJarLaunchTarget(candidates, scan.ServerType)
		if target != "" {
			scan.LaunchTarget = target
		} else if scriptName != "" && serverTypeNeedsLoader(scan.ServerType) {
			scan.LaunchTarget = scriptName
		} else if warning != "" {
			scan.Warnings = append(scan.Warnings, warning)
		}
	}
	if scan.LaunchTarget == "" && serverTypeNeedsLoader(scan.ServerType) {
		scan.Warnings = append(scan.Warnings, "No launchable server jar or platform launcher was detected. Choose the generated server launcher before importing.")
	}
	return scan
}

func topLevelJars(serverPath string) ([]string, error) {
	entries, err := os.ReadDir(serverPath)
	if err != nil {
		return nil, err
	}
	jars := []string{}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".jar") {
			jars = append(jars, entry.Name())
		}
	}
	sort.Strings(jars)
	return jars, nil
}

func detectLaunchScript(serverPath string) (string, string, string) {
	return detectLaunchScriptForOS(serverPath, runtime.GOOS)
}

func detectLaunchScriptForOS(serverPath string, goos string) (string, string, string) {
	names := launchScriptNames(goos)
	for _, name := range names {
		path := filepath.Join(serverPath, name)
		if !fileExists(path) {
			continue
		}
		text := readSmallText(path, 64*1024)
		jar := extractJarFromLaunchText(text)
		argFile := extractArgFileFromLaunchText(text)
		return name, jar, argFile
	}
	return "", "", ""
}

func launchScriptNames(goos string) []string {
	if goos == "windows" {
		return []string{"run.bat", "start.bat", "server.bat"}
	}
	return []string{"run.sh", "start.sh", "start.command", "server.sh"}
}

func platformLaunchScript(serverPath string, goos string) string {
	name, _, _ := detectLaunchScriptForOS(serverPath, goos)
	return name
}

func readSmallText(path string, limit int64) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	data, _ := io.ReadAll(io.LimitReader(file, limit))
	return string(data)
}

func extractJarFromLaunchText(text string) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)-jar\s+"([^"]+\.jar)"`),
		regexp.MustCompile(`(?i)-jar\s+'([^']+\.jar)'`),
		regexp.MustCompile(`(?i)-jar\s+([^\s]+\.jar)`),
	}
	for _, pattern := range patterns {
		if match := pattern.FindStringSubmatch(text); len(match) == 2 {
			return strings.Trim(match[1], `"'`)
		}
	}
	return ""
}

func extractArgFileFromLaunchText(text string) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`@"([^"]+\.txt)"`),
		regexp.MustCompile(`@'([^']+\.txt)'`),
		regexp.MustCompile(`@([^\s]+\.txt)`),
	}
	for _, pattern := range patterns {
		if match := pattern.FindStringSubmatch(text); len(match) == 2 {
			return strings.Trim(match[1], `"'`)
		}
	}
	return ""
}

func evidenceFromName(name string, evidence map[string]int, scan *importedServerScan) {
	lower := strings.ToLower(filepath.ToSlash(name))
	switch {
	case strings.Contains(lower, "neoforge"):
		evidence["neoforge"] += 40
	case strings.Contains(lower, "forge"):
		evidence["forge"] += 35
	case strings.Contains(lower, "fabric"):
		evidence["fabric"] += 40
	case strings.Contains(lower, "paper"):
		evidence["paper"] += 35
	case strings.Contains(lower, "purpur"):
		evidence["purpur"] += 35
	case strings.Contains(lower, "folia"):
		evidence["folia"] += 35
	}
	if scan.MinecraftVersion == "" {
		if match := regexp.MustCompile(`(\d+\.\d+(?:\.\d+)?)`).FindStringSubmatch(lower); len(match) == 2 {
			scan.MinecraftVersion = match[1]
		}
	}
}

func evidenceFromArgFilePath(path string, evidence map[string]int, scan *importedServerScan) {
	normalized := strings.ToLower(filepath.ToSlash(path))
	if match := regexp.MustCompile(`net/minecraftforge/forge/(\d+\.\d+(?:\.\d+)?)-([0-9][a-z0-9.+-]*)/`).FindStringSubmatch(normalized); len(match) == 3 {
		evidence["forge"] += 90
		scan.MinecraftVersion = firstNonEmpty(scan.MinecraftVersion, match[1])
		scan.LoaderVersion = firstNonEmpty(scan.LoaderVersion, match[2])
	}
	if match := regexp.MustCompile(`net/neoforged/neoforge/([0-9][a-z0-9.+-]*)/`).FindStringSubmatch(normalized); len(match) == 2 {
		evidence["neoforge"] += 90
		loader := match[1]
		scan.LoaderVersion = firstNonEmpty(scan.LoaderVersion, loader)
		scan.MinecraftVersion = firstNonEmpty(scan.MinecraftVersion, minecraftVersionFromNeoForgeLoader(loader))
	}
}

func detectForgeProfileFromLibraries(serverPath string) (string, string) {
	return detectProfileFromArgFiles(serverPath, regexp.MustCompile(`(?i)libraries/net/minecraftforge/forge/(\d+\.\d+(?:\.\d+)?)-([0-9][a-z0-9.+-]*)/(?:unix|win)_args\.txt$`))
}

func detectNeoForgeProfileFromLibraries(serverPath string) (string, string) {
	mc, loader := detectProfileFromArgFiles(serverPath, regexp.MustCompile(`(?i)libraries/net/neoforged/neoforge/([0-9][a-z0-9.+-]*)/(?:unix|win)_args\.txt$`))
	if loader == "" && mc != "" {
		loader = mc
		mc = minecraftVersionFromNeoForgeLoader(loader)
	}
	return mc, loader
}

func detectProfileFromArgFiles(serverPath string, pattern *regexp.Regexp) (string, string) {
	minecraftVersion, loaderVersion := "", ""
	_ = filepath.WalkDir(filepath.Join(serverPath, "libraries"), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(serverPath, path)
		if relErr != nil {
			return nil
		}
		normalized := filepath.ToSlash(rel)
		match := pattern.FindStringSubmatch(normalized)
		if len(match) == 3 {
			minecraftVersion, loaderVersion = match[1], match[2]
			return filepath.SkipAll
		}
		if len(match) == 2 {
			minecraftVersion = match[1]
			return filepath.SkipAll
		}
		return nil
	})
	return minecraftVersion, loaderVersion
}

func detectFabricProfileFromLibraries(serverPath string) (string, string) {
	minecraftVersion, loaderVersion := "", ""
	_ = filepath.WalkDir(filepath.Join(serverPath, "libraries"), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(serverPath, path)
		if relErr != nil {
			return nil
		}
		normalized := strings.ToLower(filepath.ToSlash(rel))
		if loaderVersion == "" {
			if match := regexp.MustCompile(`libraries/net/fabricmc/fabric-loader/([0-9][a-z0-9.+-]*)/`).FindStringSubmatch(normalized); len(match) == 2 {
				loaderVersion = match[1]
			}
		}
		if minecraftVersion == "" {
			if match := regexp.MustCompile(`libraries/net/minecraft/server/(\d+\.\d+(?:\.\d+)?)/`).FindStringSubmatch(normalized); len(match) == 2 {
				minecraftVersion = match[1]
			}
		}
		if minecraftVersion != "" && loaderVersion != "" {
			return filepath.SkipAll
		}
		return nil
	})
	if minecraftVersion == "" {
		minecraftVersion = detectMinecraftVersionFromVersionDirs(serverPath)
	}
	return minecraftVersion, loaderVersion
}

func detectMinecraftVersionFromVersionDirs(serverPath string) string {
	entries, err := os.ReadDir(filepath.Join(serverPath, "versions"))
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			name := entry.Name()
			if regexp.MustCompile(`^\d+\.\d+(?:\.\d+)?$`).MatchString(name) {
				return name
			}
		}
	}
	return ""
}
