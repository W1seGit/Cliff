package httpserver

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

const maxDirectorySizeCacheEntries = 256

func (h apiHandler) backups(w http.ResponseWriter, r *http.Request) {
	server, ok, err := h.store.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	if r.URL.Query().Get("current") == "1" {
		if h.process.IsRunning(server.ID) {
			writeError(w, http.StatusBadRequest, "Stop this server before exporting the current folder")
			return
		}
		fileName := safeArchiveName(server.Name) + "-current.zip"
		writeZipArchive(w, fileName, server.Path, safeArchiveName(server.Name))
		return
	}
	if backupID := r.URL.Query().Get("download"); backupID != "" {
		backup, err := h.safeBackup(r, server, backupID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		fileName := safeArchiveName(server.Name) + "-" + backup.ID + ".zip"
		writeZipArchive(w, fileName, backup.SnapshotPath, "")
		return
	}
	backups, err := h.store.ListBackups(r.Context(), server.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for index := range backups {
		backups[index].SizeBytes = h.cachedDirectorySize(backups[index].SnapshotPath)
	}
	writeJSON(w, http.StatusOK, map[string][]store.Backup{"backups": backups})
}

func (h apiHandler) backupAction(w http.ResponseWriter, r *http.Request) {
	server, ok, err := h.store.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	var input struct {
		Action    string   `json:"action"`
		BackupID  string   `json:"backupId"`
		BackupIDs []string `json:"backupIds"`
		Reason    string   `json:"reason"`
		KeepCount *int     `json:"keepCount"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid backup body")
		return
	}

	switch input.Action {
	case "restore":
		if h.process.IsRunning(server.ID) {
			writeError(w, http.StatusBadRequest, "Stop this server before restoring a snapshot")
			return
		}
		if err := h.restoreBackup(r, server, input.BackupID); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case "delete":
		if err := h.deleteBackup(r, server, input.BackupID); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case "delete-selected":
		deleted, err := h.deleteSelectedBackups(r, server, input.BackupIDs)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		backups, _ := h.backupsWithSizes(r, server.ID)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": deleted, "backups": backups})
	case "prune":
		deleted, err := h.pruneBackups(r, server, input.KeepCount)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		backups, _ := h.backupsWithSizes(r, server.ID)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": deleted, "backups": backups})
	case "rename":
		if err := h.store.RenameBackup(r.Context(), server.ID, input.BackupID, input.Reason); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		backupID, err := h.createBackup(r.Context(), server, input.Reason)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "backupId": backupID})
	}
}

func (h apiHandler) backupsWithSizes(r *http.Request, serverID string) ([]store.Backup, error) {
	backups, err := h.store.ListBackups(r.Context(), serverID)
	if err != nil {
		return nil, err
	}
	for index := range backups {
		backups[index].SizeBytes = h.cachedDirectorySize(backups[index].SnapshotPath)
	}
	return backups, nil
}

func (h apiHandler) createBackup(ctx context.Context, server store.Server, reason string) (string, error) {
	snapshotRoot := filepath.Join(h.config.ServerRoot, ".dashboard-snapshots", server.ID)
	if err := os.MkdirAll(snapshotRoot, 0o755); err != nil {
		return "", err
	}
	backupID, err := h.store.CreateBackupRecord(ctx, server.ID, reason, filepath.Join(snapshotRoot, "__pending__"))
	if err != nil {
		return "", err
	}
	target := filepath.Join(snapshotRoot, backupID)
	if err := copyDir(server.Path, target); err != nil {
		_ = h.store.DeleteBackupRecord(ctx, server.ID, backupID)
		_ = os.RemoveAll(target)
		return "", err
	}
	if err := h.store.RenameBackupPath(ctx, server.ID, backupID, target); err != nil {
		_ = h.store.DeleteBackupRecord(ctx, server.ID, backupID)
		_ = os.RemoveAll(target)
		return "", err
	}
	return backupID, nil
}

func (h apiHandler) createAutoSnapshot(ctx context.Context, server store.Server, reason string) error {
	if !server.SnapshotsEnabled {
		return nil
	}
	_, err := h.createBackup(ctx, server, reason)
	return err
}

func (h apiHandler) restoreBackup(r *http.Request, server store.Server, backupID string) error {
	backup, err := h.safeBackup(r, server, backupID)
	if err != nil {
		return err
	}
	if _, err := h.createBackup(r.Context(), server, "pre-restore safety snapshot"); err != nil {
		return err
	}
	if err := os.RemoveAll(server.Path); err != nil {
		return err
	}
	return copyDir(backup.SnapshotPath, server.Path)
}

func (h apiHandler) deleteBackup(r *http.Request, server store.Server, backupID string) error {
	backup, err := h.safeBackup(r, server, backupID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(backup.SnapshotPath); err != nil {
		return err
	}
	return h.store.DeleteBackupRecord(r.Context(), server.ID, backup.ID)
}

func (h apiHandler) deleteSelectedBackups(r *http.Request, server store.Server, backupIDs []string) ([]string, error) {
	seen := map[string]bool{}
	selected := []string{}
	for _, backupID := range backupIDs {
		backupID = strings.TrimSpace(backupID)
		if backupID != "" && !seen[backupID] {
			seen[backupID] = true
			selected = append(selected, backupID)
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("Select at least one snapshot")
	}
	if len(selected) > 200 {
		return nil, errors.New("Select 200 snapshots or fewer at a time")
	}
	deleted := []string{}
	for _, backupID := range selected {
		if err := h.deleteBackup(r, server, backupID); err != nil {
			return deleted, err
		}
		deleted = append(deleted, backupID)
	}
	return deleted, nil
}

func (h apiHandler) pruneBackups(r *http.Request, server store.Server, keepCount *int) ([]string, error) {
	if keepCount == nil || *keepCount < 0 {
		return nil, errors.New("Retention count is required")
	}
	if *keepCount > 200 {
		return nil, errors.New("Retention count must be 200 or fewer")
	}
	backups, err := h.store.ListBackups(r.Context(), server.ID)
	if err != nil {
		return nil, err
	}
	sort.Slice(backups, func(left int, right int) bool {
		return backups[left].CreatedAt > backups[right].CreatedAt
	})
	deleted := []string{}
	for _, backup := range backups[*keepCount:] {
		if err := h.deleteBackup(r, server, backup.ID); err != nil {
			return deleted, err
		}
		deleted = append(deleted, backup.ID)
	}
	return deleted, nil
}

func (h apiHandler) safeBackup(r *http.Request, server store.Server, backupID string) (store.Backup, error) {
	backup, ok, err := h.store.Backup(r.Context(), server.ID, backupID)
	if err != nil {
		return store.Backup{}, err
	}
	if !ok {
		return store.Backup{}, errors.New("Snapshot not found")
	}
	root := filepath.Clean(filepath.Join(h.config.ServerRoot, ".dashboard-snapshots", server.ID))
	target := filepath.Clean(backup.SnapshotPath)
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return store.Backup{}, errors.New("Snapshot path is outside dashboard snapshot storage")
	}
	return backup, nil
}

func directorySize(target string) int64 {
	info, err := os.Lstat(target)
	if err != nil {
		return 0
	}
	if !info.IsDir() {
		return info.Size()
	}
	var total int64
	entries, err := os.ReadDir(target)
	if err != nil {
		return 0
	}
	for _, entry := range entries {
		total += directorySize(filepath.Join(target, entry.Name()))
	}
	return total
}

func (h apiHandler) cachedDirectorySize(target string) int64 {
	if h.sizeCache == nil {
		return directorySize(target)
	}
	return h.sizeCache.get(target, 30*time.Second, directorySize)
}

func (c *directorySizeCache) get(target string, ttl time.Duration, compute func(string) int64) int64 {
	now := time.Now()
	c.mu.Lock()
	if c.entries != nil {
		if entry, ok := c.entries[target]; ok && now.Before(entry.expiresAt) {
			c.mu.Unlock()
			return entry.size
		}
	} else {
		c.entries = map[string]directorySizeCacheEntry{}
	}
	c.mu.Unlock()

	size := compute(target)

	c.mu.Lock()
	c.pruneLocked(now)
	c.entries[target] = directorySizeCacheEntry{size: size, expiresAt: now.Add(ttl)}
	c.enforceLimitLocked()
	c.mu.Unlock()
	return size
}

func (c *directorySizeCache) pruneLocked(now time.Time) {
	for target, entry := range c.entries {
		if !now.Before(entry.expiresAt) {
			delete(c.entries, target)
		}
	}
}

func (c *directorySizeCache) enforceLimitLocked() {
	for len(c.entries) > maxDirectorySizeCacheEntries {
		for target := range c.entries {
			delete(c.entries, target)
			break
		}
	}
}

func copyDir(source string, target string) error {
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !sourceInfo.IsDir() {
		return errors.New("source is not a directory")
	}
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(destination, info.Mode().Perm())
		}
		return copyFile(path, destination)
	})
}

func copyFile(source string, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()

	info, err := input.Stat()
	if err != nil {
		return err
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer output.Close()

	_, err = io.Copy(output, input)
	return err
}

func writeZipArchive(w http.ResponseWriter, fileName string, sourcePath string, rootName string) {
	info, err := os.Stat(sourcePath)
	if err != nil || !info.IsDir() {
		writeError(w, http.StatusBadRequest, "Archive source folder does not exist")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(fileName, `"`, "")+`"`)
	writer := zip.NewWriter(w)
	defer writer.Close()

	err = filepath.WalkDir(sourcePath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(sourcePath, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		if rootName != "" {
			name = filepath.ToSlash(filepath.Join(rootName, relative))
		}
		fileInfo, err := entry.Info()
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(fileInfo)
		if err != nil {
			return err
		}
		header.Name = name
		header.Method = zip.Deflate
		output, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		// The response may already have started; this log-free fallback still closes the zip writer.
		return
	}
}

func safeArchiveName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "server"
	}
	var builder strings.Builder
	lastDash := false
	for _, char := range value {
		ok := (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9')
		if ok {
			builder.WriteRune(char)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		return "server"
	}
	return result
}
