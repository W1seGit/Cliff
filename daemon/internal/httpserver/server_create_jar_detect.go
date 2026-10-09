package httpserver

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func inspectImportJar(serverPath string, name string) importJarCandidate {
	candidate := importJarCandidate{Name: name, Lower: strings.ToLower(name)}
	candidate.Installer = isInstallerJar(candidate.Lower)
	candidate.MainClass = readJarMainClass(filepath.Join(serverPath, name))
	lowerMain := strings.ToLower(candidate.MainClass)
	evidenceFromJarName(&candidate)
	if strings.Contains(lowerMain, "installer") {
		candidate.Installer = true
		candidate.Score -= 120
	}
	switch {
	case strings.Contains(lowerMain, "fabric") && strings.Contains(lowerMain, "server"):
		candidate.ServerType = "fabric"
		candidate.Score += 110
	case strings.Contains(lowerMain, "net.minecraft.server"):
		candidate.Score += 90
	case strings.Contains(lowerMain, "paper"):
		candidate.ServerType = "paper"
		candidate.Score += 80
	case strings.Contains(lowerMain, "purpur"):
		candidate.ServerType = "purpur"
		candidate.Score += 80
	case strings.Contains(lowerMain, "folia"):
		candidate.ServerType = "folia"
		candidate.Score += 80
	}
	return candidate
}

func evidenceFromJarName(candidate *importJarCandidate) {
	lower := candidate.Lower
	if lower == "fabric-server-launch.jar" {
		candidate.ServerType = "fabric"
		candidate.Score += 150
	}
	switch {
	case strings.Contains(lower, "neoforge"):
		candidate.ServerType = "neoforge"
		candidate.Score += 35
	case strings.Contains(lower, "forge"):
		candidate.ServerType = "forge"
		candidate.Score += 35
	case strings.Contains(lower, "fabric"):
		candidate.ServerType = "fabric"
		candidate.Score += 40
	case strings.Contains(lower, "paper"):
		candidate.ServerType = "paper"
		candidate.Score += 45
	case strings.Contains(lower, "purpur"):
		candidate.ServerType = "purpur"
		candidate.Score += 45
	case strings.Contains(lower, "folia"):
		candidate.ServerType = "folia"
		candidate.Score += 45
	case strings.Contains(lower, "server"):
		candidate.Score += 25
	}
	if candidate.Installer {
		candidate.Score -= 120
	}
}

func readJarMainClass(path string) string {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return ""
	}
	defer reader.Close()
	for _, file := range reader.File {
		if strings.EqualFold(file.Name, "META-INF/MANIFEST.MF") {
			if file.UncompressedSize64 > 128*1024 {
				return ""
			}
			rc, err := file.Open()
			if err != nil {
				return ""
			}
			data, _ := io.ReadAll(io.LimitReader(rc, 128*1024))
			_ = rc.Close()
			return manifestValue(string(data), "Main-Class")
		}
	}
	return ""
}

func manifestValue(manifest string, key string) string {
	lines := strings.Split(strings.ReplaceAll(manifest, "\r\n", "\n"), "\n")
	prefix := strings.ToLower(key) + ":"
	for index, line := range lines {
		if strings.HasPrefix(strings.ToLower(line), prefix) {
			value := strings.TrimSpace(line[len(prefix):])
			for next := index + 1; next < len(lines); next++ {
				if !strings.HasPrefix(lines[next], " ") {
					break
				}
				value += strings.TrimSpace(lines[next])
			}
			return value
		}
	}
	return ""
}

func evidenceFromJar(candidate importJarCandidate, evidence map[string]int, scan *importedServerScan) {
	if candidate.ServerType != "" {
		evidence[candidate.ServerType] += maxInt(candidate.Score, 1)
	}
	evidenceFromName(candidate.Name, evidence, scan)
	if candidate.MinecraftVersion != "" {
		scan.MinecraftVersion = firstNonEmpty(scan.MinecraftVersion, candidate.MinecraftVersion)
	}
	if candidate.LoaderVersion != "" {
		scan.LoaderVersion = firstNonEmpty(scan.LoaderVersion, candidate.LoaderVersion)
	}
}

func strongestServerType(evidence map[string]int) string {
	bestType, bestScore := "vanilla", evidence["vanilla"]
	preference := map[string]int{"fabric": 6, "neoforge": 5, "forge": 4, "paper": 3, "purpur": 2, "folia": 1, "vanilla": 0}
	for serverType, score := range evidence {
		if score > bestScore || (score == bestScore && preference[serverType] > preference[bestType]) {
			bestType, bestScore = serverType, score
		}
	}
	return bestType
}

func detectMinecraftProfileFromJars(candidates []importJarCandidate, serverType string) (string, string) {
	for _, candidate := range candidates {
		lower := candidate.Lower
		if serverType == "fabric" && strings.Contains(lower, "fabric") {
			pattern := regexp.MustCompile(`(?:mc|minecraft)[-_.]?(\d+\.\d+(?:\.\d+)?)[-_.].*loader[-_.]?([0-9][a-z0-9.+-]*)`)
			if match := pattern.FindStringSubmatch(lower); len(match) == 3 {
				return match[1], strings.TrimSuffix(strings.TrimSuffix(regexp.MustCompile(`[-_.]?launcher.*$`).ReplaceAllString(match[2], ""), ".jar"), "-installer")
			}
		}
		if serverType == "forge" && strings.Contains(lower, "forge") {
			pattern := regexp.MustCompile(`forge-(\d+\.\d+(?:\.\d+)?)-([0-9][a-z0-9.+-]*)`)
			if match := pattern.FindStringSubmatch(lower); len(match) == 3 {
				return match[1], cleanJarVersion(match[2])
			}
		}
		if serverType == "neoforge" && strings.Contains(lower, "neoforge") {
			pattern := regexp.MustCompile(`neoforge-([0-9][a-z0-9.+-]*)`)
			if match := pattern.FindStringSubmatch(lower); len(match) == 2 {
				loader := cleanJarVersion(match[1])
				return minecraftVersionFromNeoForgeLoader(loader), loader
			}
		}
	}
	versionPattern := regexp.MustCompile(`(\d+\.\d+(?:\.\d+)?)`)
	for _, candidate := range candidates {
		if regexp.MustCompile(`(?:minecraft[_-]?server|server)[_.-]\d+\.\d+`).MatchString(candidate.Lower) {
			if match := versionPattern.FindStringSubmatch(candidate.Lower); len(match) == 2 {
				return match[1], ""
			}
		}
	}
	return "", ""
}

func bestJarLaunchTarget(candidates []importJarCandidate, serverType string) (string, string) {
	if len(candidates) == 0 {
		return "", ""
	}
	best := importJarCandidate{Score: -1000}
	installerCount := 0
	for _, candidate := range candidates {
		if candidate.Installer {
			installerCount++
			continue
		}
		score := candidate.Score
		if candidate.ServerType == serverType {
			score += 30
		}
		if strings.Contains(candidate.Lower, "server") {
			score += 20
		}
		if score > best.Score {
			best = candidate
			best.Score = score
		}
	}
	if best.Name != "" && best.Score >= 20 {
		return best.Name, ""
	}
	if installerCount > 0 {
		return "", "Installer jar found but not launchable. Choose a start script or generated server launcher before importing."
	}
	return "", ""
}

func cleanJarVersion(value string) string {
	value = strings.TrimSuffix(value, "-installer.jar")
	value = strings.TrimSuffix(value, ".jar")
	return value
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func minecraftVersionFromNeoForgeLoader(loaderVersion string) string {
	parts := strings.Split(loaderVersion, ".")
	if len(parts) < 2 {
		return ""
	}
	if parts[0] == "20" || parts[0] == "21" {
		return "1." + parts[0] + "." + parts[1]
	}
	if len(parts) >= 3 {
		return parts[0] + "." + parts[1] + "." + parts[2]
	}
	return parts[0] + "." + parts[1]
}

func countImportedMods(serverPath string) (int, int) {
	countJars := func(path string) int {
		entries, err := os.ReadDir(path)
		if err != nil {
			return 0
		}
		count := 0
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".jar") {
				count++
			}
		}
		return count
	}
	return countJars(filepath.Join(serverPath, "mods")), countJars(filepath.Join(serverPath, ".dashboard-disabled-mods"))
}
