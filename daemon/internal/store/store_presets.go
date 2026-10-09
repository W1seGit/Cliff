package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type CommandPreset struct {
	ID        string `json:"id"`
	ServerID  string `json:"serverId"`
	Command   string `json:"command"`
	CreatedAt string `json:"createdAt"`
}

func (s *Store) ListCommandPresets(ctx context.Context, serverID string) ([]CommandPreset, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, server_id, command, created_at FROM command_presets WHERE server_id = ? ORDER BY created_at DESC`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	presets := []CommandPreset{}
	for rows.Next() {
		var preset CommandPreset
		if err := rows.Scan(&preset.ID, &preset.ServerID, &preset.Command, &preset.CreatedAt); err != nil {
			return nil, err
		}
		presets = append(presets, preset)
	}
	return presets, rows.Err()
}

func (s *Store) SaveCommandPreset(ctx context.Context, serverID string, command string) (string, error) {
	nextCommand := strings.TrimSpace(command)
	if nextCommand == "" {
		return "", errors.New("Command preset is required")
	}
	if len(nextCommand) > 200 {
		return "", errors.New("Command presets must be 200 characters or fewer")
	}
	var existingID string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM command_presets WHERE server_id = ? AND lower(command) = lower(?)`, serverID, nextCommand).Scan(&existingID)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if existingID != "" {
		_, err := s.db.ExecContext(ctx, `UPDATE command_presets SET command = ?, created_at = ? WHERE id = ?`, nextCommand, now, existingID)
		return existingID, err
	}
	id, err := randomHex(8)
	if err != nil {
		return "", err
	}
	presetID := "cmd_" + id
	_, err = s.db.ExecContext(ctx, `INSERT INTO command_presets (id, server_id, command, created_at) VALUES (?, ?, ?, ?)`, presetID, serverID, nextCommand, now)
	return presetID, err
}

func (s *Store) DeleteCommandPreset(ctx context.Context, serverID string, presetID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM command_presets WHERE server_id = ? AND id = ?`, serverID, presetID)
	return err
}
