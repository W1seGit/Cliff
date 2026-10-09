package httpserver

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func readArchiveMetadata(path string, backupPath string) archiveMetadata {
	lower := strings.ToLower(backupPath)
	if !strings.HasSuffix(lower, ".jar") && !strings.HasSuffix(lower, ".zip") {
		return archiveMetadata{Name: strings.TrimSuffix(filepath.Base(backupPath), filepath.Ext(backupPath))}
	}
	reader, err := zip.OpenReader(path)
	if err != nil {
		return archiveMetadata{Name: strings.TrimSuffix(filepath.Base(backupPath), filepath.Ext(backupPath))}
	}
	defer reader.Close()

	readEntry := func(name string) []byte {
		for _, file := range reader.File {
			if strings.EqualFold(file.Name, name) {
				input, err := file.Open()
				if err != nil {
					return nil
				}
				defer input.Close()
				data, _ := io.ReadAll(io.LimitReader(input, 64*1024))
				return data
			}
		}
		return nil
	}
	if data := readEntry("fabric.mod.json"); len(data) > 0 {
		var parsed struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if json.Unmarshal(data, &parsed) == nil {
			return archiveMetadata{Name: firstNonEmptyBackupValue(parsed.Name, parsed.ID), Version: parsed.Version}
		}
	}
	for _, name := range []string{"plugin.yml", "paper-plugin.yml"} {
		if data := readEntry(name); len(data) > 0 {
			fields := parseSimpleKeyValues(string(data), "name", "version")
			return archiveMetadata{Name: fields["name"], Version: fields["version"]}
		}
	}
	for _, name := range []string{"META-INF/mods.toml", "META-INF/neoforge.mods.toml"} {
		if data := readEntry(name); len(data) > 0 {
			fields := parseSimpleKeyValues(string(data), "displayName", "modId", "version")
			return archiveMetadata{Name: firstNonEmptyBackupValue(fields["displayName"], fields["modId"]), Version: fields["version"]}
		}
	}
	if data := readEntry("pack.mcmeta"); len(data) > 0 {
		var parsed struct {
			Pack struct {
				Description any `json:"description"`
			} `json:"pack"`
		}
		if json.Unmarshal(data, &parsed) == nil {
			if description, ok := parsed.Pack.Description.(string); ok && strings.TrimSpace(description) != "" {
				return archiveMetadata{Name: strings.TrimSpace(description)}
			}
		}
	}
	return archiveMetadata{Name: strings.TrimSuffix(filepath.Base(backupPath), filepath.Ext(backupPath))}
}

func parseSimpleKeyValues(data string, keys ...string) map[string]string {
	result := map[string]string{}
	want := map[string]bool{}
	for _, key := range keys {
		want[strings.ToLower(key)] = true
	}
	keyValuePattern := regexp.MustCompile(`^\s*([A-Za-z0-9_.-]+)\s*[:=]\s*["']?([^"'#\r\n]+)`)
	for _, line := range strings.Split(data, "\n") {
		matches := keyValuePattern.FindStringSubmatch(line)
		if len(matches) != 3 {
			continue
		}
		key := strings.TrimSpace(matches[1])
		if want[strings.ToLower(key)] {
			result[key] = strings.TrimSpace(matches[2])
		}
	}
	return result
}

func firstNonEmptyBackupValue(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (h apiHandler) collectSmartBackupGarbage(ctx context.Context, serverID string) error {
	backups, err := h.store.ListBackups(ctx, serverID)
	if err != nil {
		return err
	}
	referenced := map[string]bool{}
	for _, backup := range backups {
		manifest, err := h.loadSmartBackupManifest(backup)
		if err != nil {
			continue
		}
		for _, file := range manifest.Files {
			referenced[file.Hash] = true
		}
	}
	blobsRoot := h.smartBackupBlobsRoot(serverID)
	if _, err := os.Stat(blobsRoot); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(blobsRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		hash := filepath.Base(path)
		if !referenced[hash] {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("remove unreferenced backup blob %s: %w", hash, err)
			}
		}
		return nil
	})
}

func hashFile(path string) (string, error) {
	input, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer input.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, input); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func smartBlobPath(root string, hash string) string {
	prefix := hash
	if len(prefix) > 2 {
		prefix = hash[:2]
	}
	return filepath.Join(root, prefix, hash)
}

func shouldIgnoreBackupPath(path string, isDir bool) bool {
	path = strings.Trim(filepath.ToSlash(path), "/")
	lower := strings.ToLower(path)
	if lower == "" {
		return false
	}
	parts := strings.Split(lower, "/")
	if len(parts) > 0 {
		switch parts[0] {
		case ".dashboard-snapshots", "logs", "cache", "libraries", "versions", "crash-reports", "backups":
			return true
		}
	}
	base := parts[len(parts)-1]
	if isDir {
		return base == ".git" || base == "tmp" || base == "temp"
	}
	return strings.HasSuffix(base, ".log") || strings.HasSuffix(base, ".tmp") || strings.HasSuffix(base, ".lock")
}

func backupPathCategory(path string) string {
	lower := strings.ToLower(filepath.ToSlash(path))
	parts := strings.Split(lower, "/")
	if len(parts) > 0 {
		switch parts[0] {
		case "mods", "plugins", "datapacks", "resourcepacks":
			return "content"
		case "world", "world_nether", "world_the_end":
			return "world"
		case "config":
			return "config"
		}
	}
	if strings.Contains(lower, "/datapacks/") || strings.Contains(lower, "/playerdata/") || strings.Contains(lower, "/region/") || strings.Contains(lower, "/poi/") || strings.Contains(lower, "/entities/") || strings.Contains(lower, "/stats/") || strings.Contains(lower, "/advancements/") {
		return "world"
	}
	ext := filepath.Ext(lower)
	switch ext {
	case ".properties", ".yml", ".yaml", ".json", ".toml", ".conf", ".cfg", ".txt":
		return "config"
	}
	switch filepath.Base(lower) {
	case "server.properties", "bukkit.yml", "spigot.yml", "paper-global.yml", "paper-world-defaults.yml", "ops.json", "whitelist.json", "banned-players.json", "banned-ips.json", "eula.txt":
		return "config"
	}
	return "other"
}

func safeJoinServerPath(root string, relative string) (string, error) {
	relative = filepath.FromSlash(strings.TrimPrefix(relative, "/"))
	if relative == "." || relative == "" || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || relative == ".." || filepath.IsAbs(relative) {
		return "", errors.New("snapshot contains an unsafe path")
	}
	for _, part := range strings.Split(relative, string(os.PathSeparator)) {
		if part == ".." {
			return "", errors.New("snapshot contains an unsafe path")
		}
	}
	target := filepath.Clean(filepath.Join(root, relative))
	cleanRoot := filepath.Clean(root)
	if target != cleanRoot && !strings.HasPrefix(target, cleanRoot+string(os.PathSeparator)) {
		return "", errors.New("snapshot contains an unsafe path")
	}
	return target, nil
}

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
