package httpserver

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
