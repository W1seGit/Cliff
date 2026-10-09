package httpserver

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

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
