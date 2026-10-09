package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Backup struct {
	ID               string         `json:"id"`
	ServerID         string         `json:"serverId,omitempty"`
	Reason           string         `json:"reason"`
	SnapshotPath     string         `json:"snapshotPath"`
	CreatedAt        string         `json:"createdAt"`
	SizeBytes        int64          `json:"sizeBytes"`
	LogicalSizeBytes int64          `json:"logicalSizeBytes,omitempty"`
	Scope            string         `json:"scope,omitempty"`
	Stats            BackupStats    `json:"stats,omitempty"`
	Changes          []BackupChange `json:"changes,omitempty"`
	Summary          string         `json:"summary,omitempty"`
}

type BackupStats struct {
	FilesAdded     int   `json:"filesAdded"`
	FilesModified  int   `json:"filesModified"`
	FilesRemoved   int   `json:"filesRemoved"`
	FilesUnchanged int   `json:"filesUnchanged"`
	BytesStored    int64 `json:"bytesStored"`
	LogicalBytes   int64 `json:"logicalBytes"`
	ConfigChanges  int   `json:"configChanges"`
	ContentChanges int   `json:"contentChanges"`
	WorldChanges   int   `json:"worldChanges"`
	OtherChanges   int   `json:"otherChanges"`
	IgnoredFiles   int   `json:"ignoredFiles"`
}

type BackupChange struct {
	Path        string `json:"path"`
	Type        string `json:"type"`
	Category    string `json:"category"`
	Size        int64  `json:"size,omitempty"`
	OldHash     string `json:"oldHash,omitempty"`
	NewHash     string `json:"newHash,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	Version     string `json:"version,omitempty"`
	OldVersion  string `json:"oldVersion,omitempty"`
	NewVersion  string `json:"newVersion,omitempty"`
}

func (s *Store) ListBackups(ctx context.Context, serverID string) ([]Backup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, server_id, reason, snapshot_path, created_at FROM backups WHERE server_id = ? ORDER BY created_at DESC`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	backups := []Backup{}
	for rows.Next() {
		var backup Backup
		if err := rows.Scan(&backup.ID, &backup.ServerID, &backup.Reason, &backup.SnapshotPath, &backup.CreatedAt); err != nil {
			return nil, err
		}
		backups = append(backups, backup)
	}
	return backups, rows.Err()
}

func (s *Store) CountBackups(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM backups`).Scan(&count)
	return count, err
}

func (s *Store) CreateBackupRecord(ctx context.Context, serverID string, reason string, snapshotPath string) (string, error) {
	nextReason := strings.TrimSpace(reason)
	if nextReason == "" {
		nextReason = "manual snapshot"
	}
	id, err := randomHex(8)
	if err != nil {
		return "", err
	}
	backupID := "snap_" + id
	_, err = s.db.ExecContext(ctx, `INSERT INTO backups (id, server_id, reason, snapshot_path, created_at) VALUES (?, ?, ?, ?, ?)`, backupID, serverID, nextReason, snapshotPath, time.Now().UTC().Format(time.RFC3339))
	return backupID, err
}

func (s *Store) Backup(ctx context.Context, serverID string, backupID string) (Backup, bool, error) {
	var backup Backup
	err := s.db.QueryRowContext(ctx, `SELECT id, server_id, reason, snapshot_path, created_at FROM backups WHERE server_id = ? AND id = ?`, serverID, backupID).
		Scan(&backup.ID, &backup.ServerID, &backup.Reason, &backup.SnapshotPath, &backup.CreatedAt)
	if err == sql.ErrNoRows {
		return Backup{}, false, nil
	}
	if err != nil {
		return Backup{}, false, err
	}
	return backup, true, nil
}

func (s *Store) RenameBackup(ctx context.Context, serverID string, backupID string, reason string) error {
	nextReason := strings.TrimSpace(reason)
	if nextReason == "" {
		return errors.New("Snapshot label is required")
	}
	if len(nextReason) > 160 {
		return errors.New("Snapshot label must be 160 characters or fewer")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE backups SET reason = ? WHERE server_id = ? AND id = ?`, nextReason, serverID, backupID)
	if err != nil {
		return err
	}
	changes, err := result.RowsAffected()
	if err == nil && changes == 0 {
		return errors.New("Snapshot not found")
	}
	return nil
}

func (s *Store) RenameBackupPath(ctx context.Context, serverID string, backupID string, snapshotPath string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE backups SET snapshot_path = ? WHERE server_id = ? AND id = ?`, snapshotPath, serverID, backupID)
	if err != nil {
		return err
	}
	changes, err := result.RowsAffected()
	if err == nil && changes == 0 {
		return errors.New("Snapshot not found")
	}
	return nil
}

func (s *Store) DeleteBackupRecord(ctx context.Context, serverID string, backupID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM backups WHERE server_id = ? AND id = ?`, serverID, backupID)
	return err
}
