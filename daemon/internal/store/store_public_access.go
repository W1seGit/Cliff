package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type PublicAccess struct {
	ServerID      string `json:"serverId"`
	Provider      string `json:"provider"`
	PublicAddress string `json:"publicAddress"`
	LocalHost     string `json:"localHost"`
	LocalPort     int    `json:"localPort"`
	AgentPath     string `json:"agentPath"`
	Claimed       bool   `json:"claimed"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

func (s *Store) PublicAccess(ctx context.Context, serverID string) (PublicAccess, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT server_id, provider, public_address, local_host, local_port, agent_path, claimed, created_at, updated_at FROM public_access WHERE server_id = ?`, serverID)
	var access PublicAccess
	var claimed string
	if err := row.Scan(&access.ServerID, &access.Provider, &access.PublicAddress, &access.LocalHost, &access.LocalPort, &access.AgentPath, &claimed, &access.CreatedAt, &access.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return PublicAccess{}, false, nil
		}
		return PublicAccess{}, false, err
	}
	access.Claimed = claimed == "true"
	return access, true, nil
}

func (s *Store) SavePublicAccess(ctx context.Context, input PublicAccess) (PublicAccess, error) {
	access := input
	access.ServerID = strings.TrimSpace(access.ServerID)
	if access.ServerID == "" {
		return PublicAccess{}, errors.New("Server is required")
	}
	access.Provider = strings.TrimSpace(access.Provider)
	if access.Provider == "" {
		access.Provider = "Playit"
	}
	access.PublicAddress = strings.TrimSpace(access.PublicAddress)
	access.LocalHost = strings.TrimSpace(access.LocalHost)
	if access.LocalHost == "" {
		access.LocalHost = "localhost"
	}
	if access.LocalPort < 1 || access.LocalPort > 65535 {
		return PublicAccess{}, errors.New("Local port must be between 1 and 65535")
	}
	access.AgentPath = strings.TrimSpace(access.AgentPath)
	now := time.Now().UTC().Format(time.RFC3339)
	current, ok, err := s.PublicAccess(ctx, access.ServerID)
	if err != nil {
		return PublicAccess{}, err
	}
	if ok {
		access.CreatedAt = current.CreatedAt
	} else {
		access.CreatedAt = now
	}
	access.UpdatedAt = now
	_, err = s.db.ExecContext(ctx, `INSERT INTO public_access (
		server_id, provider, public_address, local_host, local_port, agent_path, claimed, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(server_id) DO UPDATE SET
		provider = excluded.provider,
		public_address = excluded.public_address,
		local_host = excluded.local_host,
		local_port = excluded.local_port,
		agent_path = excluded.agent_path,
		claimed = excluded.claimed,
		updated_at = excluded.updated_at`,
		access.ServerID,
		access.Provider,
		access.PublicAddress,
		access.LocalHost,
		access.LocalPort,
		access.AgentPath,
		boolText(access.Claimed),
		access.CreatedAt,
		access.UpdatedAt,
	)
	if err != nil {
		return PublicAccess{}, err
	}
	return access, nil
}

func (s *Store) DeletePublicAccess(ctx context.Context, serverID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM public_access WHERE server_id = ?`, serverID)
	return err
}
