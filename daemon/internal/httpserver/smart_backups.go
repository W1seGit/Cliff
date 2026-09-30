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
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

const smartBackupFormatVersion = 1
const maxBackupDiffBytes = 256 * 1024

type smartBackupManifest struct {
	Version        int                  `json:"version"`
	ID             string               `json:"id"`
	ServerID       string               `json:"serverId"`
	Reason         string               `json:"reason"`
	Scope          string               `json:"scope"`
	CreatedAt      string               `json:"createdAt"`
	BaseRevisionID string               `json:"baseRevisionId,omitempty"`
	Stats          store.BackupStats    `json:"stats"`
	Changes        []store.BackupChange `json:"changes"`
	Files          []smartBackupFile    `json:"files"`
}

type smartBackupFile struct {
	Path       string `json:"path"`
	Hash       string `json:"hash"`
	Size       int64  `json:"size"`
	Mode       uint32 `json:"mode"`
	ModifiedAt string `json:"modifiedAt"`
	Category   string `json:"category"`
}

type archiveMetadata struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

type backupDiffLine struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (h apiHandler) smartBackupRoot(serverID string) string {
	return filepath.Join(h.config.ServerRoot, ".dashboard-snapshots", serverID)
}

func (h apiHandler) smartBackupBlobsRoot(serverID string) string {
	return filepath.Join(h.smartBackupRoot(serverID), "blobs")
}

func (h apiHandler) smartBackupRevisionsRoot(serverID string) string {
	return filepath.Join(h.smartBackupRoot(serverID), "revisions")
}

func (h apiHandler) createSmartBackup(ctx context.Context, server store.Server, reason string) (string, error) {
	root := h.smartBackupRoot(server.ID)
	revisionsRoot := h.smartBackupRevisionsRoot(server.ID)
	if err := os.MkdirAll(revisionsRoot, 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(h.smartBackupBlobsRoot(server.ID), 0o755); err != nil {
		return "", err
	}

	var previous *smartBackupManifest
	backups, err := h.store.ListBackups(ctx, server.ID)
	if err == nil {
		for _, backup := range backups {
			manifest, loadErr := h.loadSmartBackupManifest(backup)
			if loadErr == nil {
				previous = &manifest
				break
			}
		}
	}

	backupID, err := h.store.CreateBackupRecord(ctx, server.ID, reason, filepath.Join(root, "__pending__.json"))
	if err != nil {
		return "", err
	}
	backup, ok, err := h.store.Backup(ctx, server.ID, backupID)
	if err != nil {
		_ = h.store.DeleteBackupRecord(ctx, server.ID, backupID)
		return "", err
	}
	if !ok {
		return "", errors.New("Snapshot record was not created")
	}

	manifestPath := filepath.Join(revisionsRoot, backupID+".json")
	manifest, err := h.buildSmartBackupManifest(server, backup, previous)
	if err != nil {
		_ = h.store.DeleteBackupRecord(ctx, server.ID, backupID)
		_ = os.Remove(manifestPath)
		return "", err
	}
	if err := writeJSONAtomic(manifestPath, manifest); err != nil {
		_ = h.store.DeleteBackupRecord(ctx, server.ID, backupID)
		_ = os.Remove(manifestPath)
		return "", err
	}
	if err := h.store.RenameBackupPath(ctx, server.ID, backupID, manifestPath); err != nil {
		_ = h.store.DeleteBackupRecord(ctx, server.ID, backupID)
		_ = os.Remove(manifestPath)
		return "", err
	}
	return backupID, nil
}

func (h apiHandler) buildSmartBackupManifest(server store.Server, backup store.Backup, previous *smartBackupManifest) (smartBackupManifest, error) {
	previousFiles := map[string]smartBackupFile{}
	if previous != nil {
		for _, file := range previous.Files {
			previousFiles[file.Path] = file
		}
	}

	currentFiles := []smartBackupFile{}
	currentByPath := map[string]smartBackupFile{}
	stats := store.BackupStats{}
	blobsRoot := h.smartBackupBlobsRoot(server.ID)

	err := filepath.WalkDir(server.Path, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == server.Path {
			return nil
		}
		relative, err := filepath.Rel(server.Path, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if shouldIgnoreBackupPath(relative, entry.IsDir()) {
			stats.IgnoredFiles++
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			stats.IgnoredFiles++
			return nil
		}
		hash, err := hashFile(path)
		if err != nil {
			return err
		}
		blobPath := smartBlobPath(blobsRoot, hash)
		if _, err := os.Stat(blobPath); errors.Is(err, os.ErrNotExist) {
			if err := copyFile(path, blobPath); err != nil {
				return err
			}
			stats.BytesStored += info.Size()
		} else if err != nil {
			return err
		}
		file := smartBackupFile{
			Path:       relative,
			Hash:       hash,
			Size:       info.Size(),
			Mode:       uint32(info.Mode().Perm()),
			ModifiedAt: info.ModTime().UTC().Format(time.RFC3339),
			Category:   backupPathCategory(relative),
		}
		stats.LogicalBytes += info.Size()
		currentFiles = append(currentFiles, file)
		currentByPath[file.Path] = file
		return nil
	})
	if err != nil {
		return smartBackupManifest{}, err
	}

	changes := []store.BackupChange{}
	for _, file := range currentFiles {
		previousFile, existed := previousFiles[file.Path]
		switch {
		case !existed:
			stats.FilesAdded++
			changes = append(changes, store.BackupChange{Path: file.Path, Type: "added", Category: file.Category, Size: file.Size, NewHash: file.Hash})
		case previousFile.Hash != file.Hash:
			stats.FilesModified++
			changes = append(changes, store.BackupChange{Path: file.Path, Type: "modified", Category: file.Category, Size: file.Size, OldHash: previousFile.Hash, NewHash: file.Hash})
		default:
			stats.FilesUnchanged++
		}
	}
	for path, previousFile := range previousFiles {
		if _, ok := currentByPath[path]; !ok {
			stats.FilesRemoved++
			changes = append(changes, store.BackupChange{Path: path, Type: "removed", Category: previousFile.Category, Size: previousFile.Size, OldHash: previousFile.Hash})
		}
	}
	for index := range changes {
		changes[index] = enrichSmartBackupChange(blobsRoot, changes[index])
	}
	sort.Slice(changes, func(left int, right int) bool {
		if changes[left].Category == changes[right].Category {
			return changes[left].Path < changes[right].Path
		}
		return changes[left].Category < changes[right].Category
	})
	sort.Slice(currentFiles, func(left int, right int) bool {
		return currentFiles[left].Path < currentFiles[right].Path
	})
	for _, change := range changes {
		switch change.Category {
		case "config":
			stats.ConfigChanges++
		case "content":
			stats.ContentChanges++
		case "world":
			stats.WorldChanges++
		default:
			stats.OtherChanges++
		}
	}

	baseRevisionID := ""
	if previous != nil {
		baseRevisionID = previous.ID
	}
	return smartBackupManifest{
		Version:        smartBackupFormatVersion,
		ID:             backup.ID,
		ServerID:       server.ID,
		Reason:         backup.Reason,
		Scope:          "full",
		CreatedAt:      backup.CreatedAt,
		BaseRevisionID: baseRevisionID,
		Stats:          stats,
		Changes:        changes,
		Files:          currentFiles,
	}, nil
}

func (h apiHandler) hydrateSmartBackup(backup *store.Backup) {
	manifest, err := h.loadSmartBackupManifest(*backup)
	if err != nil {
		backup.SizeBytes = h.cachedDirectorySize(backup.SnapshotPath)
		return
	}
	backup.Scope = manifest.Scope
	backup.Stats = manifest.Stats
	backup.Changes = summarizeSmartBackupChanges(manifest.Changes)
	backup.Summary = smartBackupSummary(manifest.Stats)
	backup.SizeBytes = manifest.Stats.BytesStored
	backup.LogicalSizeBytes = manifest.Stats.LogicalBytes
}

func (h apiHandler) loadSmartBackupManifest(backup store.Backup) (smartBackupManifest, error) {
	data, err := os.ReadFile(backup.SnapshotPath)
	if err != nil {
		return smartBackupManifest{}, err
	}
	var manifest smartBackupManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return smartBackupManifest{}, err
	}
	if manifest.Version != smartBackupFormatVersion || manifest.ID == "" {
		return smartBackupManifest{}, errors.New("unsupported snapshot manifest")
	}
	return manifest, nil
}

func summarizeSmartBackupChanges(changes []store.BackupChange) []store.BackupChange {
	const limit = 80
	if len(changes) <= limit {
		return changes
	}
	return changes[:limit]
}

func smartBackupSummary(stats store.BackupStats) string {
	parts := []string{}
	if stats.ContentChanges > 0 {
		parts = append(parts, pluralCount(stats.ContentChanges, "content file")+" changed")
	}
	if stats.ConfigChanges > 0 {
		parts = append(parts, pluralCount(stats.ConfigChanges, "config file")+" changed")
	}
	if stats.WorldChanges > 0 {
		parts = append(parts, pluralCount(stats.WorldChanges, "world file")+" changed")
	}
	if stats.OtherChanges > 0 {
		parts = append(parts, pluralCount(stats.OtherChanges, "other file")+" changed")
	}
	if len(parts) == 0 {
		return "No file changes"
	}
	return strings.Join(parts, ", ")
}

func pluralCount(count int, singular string) string {
	if count == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(count) + " " + singular + "s"
}

func (h apiHandler) restoreSmartBackup(r *http.Request, server store.Server, backup store.Backup) error {
	manifest, err := h.loadSmartBackupManifest(backup)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(server.Path); err != nil {
		return err
	}
	if err := os.MkdirAll(server.Path, 0o755); err != nil {
		return err
	}
	blobsRoot := h.smartBackupBlobsRoot(server.ID)
	for _, file := range manifest.Files {
		target, err := safeJoinServerPath(server.Path, file.Path)
		if err != nil {
			return err
		}
		source := smartBlobPath(blobsRoot, file.Hash)
		if err := copyFile(source, target); err != nil {
			return err
		}
		_ = os.Chmod(target, os.FileMode(file.Mode))
		if modifiedAt, err := time.Parse(time.RFC3339, file.ModifiedAt); err == nil {
			_ = os.Chtimes(target, modifiedAt, modifiedAt)
		}
	}
	return nil
}

func (h apiHandler) writeSmartBackupZip(w http.ResponseWriter, fileName string, backup store.Backup) {
	manifest, err := h.loadSmartBackupManifest(backup)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Snapshot manifest could not be read")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(fileName, `"`, "")+`"`)
	writer := zip.NewWriter(w)
	defer writer.Close()

	blobsRoot := h.smartBackupBlobsRoot(backup.ServerID)
	for _, file := range manifest.Files {
		info := zip.FileHeader{
			Name:   file.Path,
			Method: zip.Deflate,
		}
		info.SetMode(os.FileMode(file.Mode))
		if modifiedAt, err := time.Parse(time.RFC3339, file.ModifiedAt); err == nil {
			info.SetModTime(modifiedAt)
		}
		output, err := writer.CreateHeader(&info)
		if err != nil {
			return
		}
		input, err := os.Open(smartBlobPath(blobsRoot, file.Hash))
		if err != nil {
			return
		}
		_, copyErr := io.Copy(output, input)
		closeErr := input.Close()
		if copyErr != nil || closeErr != nil {
			return
		}
	}
}

func (h apiHandler) backupDiff(w http.ResponseWriter, r *http.Request) {
	server, ok, err := h.store.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	backupID := strings.TrimSpace(r.URL.Query().Get("backupId"))
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if backupID == "" || path == "" {
		writeError(w, http.StatusBadRequest, "backupId and path are required")
		return
	}
	backup, err := h.safeBackup(r, server, backupID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	manifest, err := h.loadSmartBackupManifest(backup)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Diffs are only available for smart snapshots")
		return
	}
	path = filepath.ToSlash(strings.TrimPrefix(path, "/"))
	var change store.BackupChange
	found := false
	for _, candidate := range manifest.Changes {
		if candidate.Path == path {
			change = candidate
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "Changed file not found in snapshot")
		return
	}
	if change.Category != "config" {
		writeError(w, http.StatusBadRequest, "Diffs are only available for config/text files")
		return
	}
	blobsRoot := h.smartBackupBlobsRoot(server.ID)
	oldText, oldTruncated, err := readSmartBackupTextBlob(blobsRoot, change.OldHash)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	newText, newTruncated, err := readSmartBackupTextBlob(blobsRoot, change.NewHash)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	lines := buildSimpleLineDiff(splitDiffLines(oldText), splitDiffLines(newText))
	writeJSON(w, http.StatusOK, map[string]any{
		"path":      change.Path,
		"change":    change,
		"lines":     lines,
		"truncated": oldTruncated || newTruncated,
	})
}

func readSmartBackupTextBlob(blobsRoot string, hash string) (string, bool, error) {
	if hash == "" {
		return "", false, nil
	}
	path := smartBlobPath(blobsRoot, hash)
	info, err := os.Stat(path)
	if err != nil {
		return "", false, err
	}
	limit := int64(maxBackupDiffBytes)
	truncated := info.Size() > limit
	input, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer input.Close()
	data, err := io.ReadAll(io.LimitReader(input, limit+1))
	if err != nil {
		return "", false, err
	}
	if len(data) > int(limit) {
		data = data[:limit]
		truncated = true
	}
	if bytesLookBinary(data) {
		return "", truncated, errors.New("File appears to be binary")
	}
	return string(data), truncated, nil
}

func bytesLookBinary(data []byte) bool {
	for _, value := range data {
		if value == 0 {
			return true
		}
	}
	return false
}

func splitDiffLines(value string) []string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.TrimRight(value, "\n")
	if value == "" {
		return []string{}
	}
	return strings.Split(value, "\n")
}

func buildSimpleLineDiff(oldLines []string, newLines []string) []backupDiffLine {
	rows := len(oldLines) + 1
	cols := len(newLines) + 1
	lcs := make([][]int, rows)
	for i := range lcs {
		lcs[i] = make([]int, cols)
	}
	for i := len(oldLines) - 1; i >= 0; i-- {
		for j := len(newLines) - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	result := []backupDiffLine{}
	i, j := 0, 0
	for i < len(oldLines) && j < len(newLines) {
		if oldLines[i] == newLines[j] {
			result = append(result, backupDiffLine{Type: "context", Text: oldLines[i]})
			i++
			j++
		} else if lcs[i+1][j] >= lcs[i][j+1] {
			result = append(result, backupDiffLine{Type: "removed", Text: oldLines[i]})
			i++
		} else {
			result = append(result, backupDiffLine{Type: "added", Text: newLines[j]})
			j++
		}
	}
	for ; i < len(oldLines); i++ {
		result = append(result, backupDiffLine{Type: "removed", Text: oldLines[i]})
	}
	for ; j < len(newLines); j++ {
		result = append(result, backupDiffLine{Type: "added", Text: newLines[j]})
	}
	return result
}

func enrichSmartBackupChange(blobsRoot string, change store.BackupChange) store.BackupChange {
	if change.Category != "content" {
		return change
	}
	oldMeta := archiveMetadata{}
	newMeta := archiveMetadata{}
	if change.OldHash != "" {
		oldMeta = readArchiveMetadata(smartBlobPath(blobsRoot, change.OldHash), change.Path)
	}
	if change.NewHash != "" {
		newMeta = readArchiveMetadata(smartBlobPath(blobsRoot, change.NewHash), change.Path)
	}
	if newMeta.Name != "" {
		change.DisplayName = newMeta.Name
	} else if oldMeta.Name != "" {
		change.DisplayName = oldMeta.Name
	}
	if newMeta.Version != "" {
		change.Version = newMeta.Version
	}
	if oldMeta.Version != "" {
		change.OldVersion = oldMeta.Version
	}
	if newMeta.Version != "" {
		change.NewVersion = newMeta.Version
	}
	return change
}

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
