package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Server struct {
	ID                        string `json:"id"`
	Name                      string `json:"name"`
	Path                      string `json:"path"`
	Type                      string `json:"type"`
	MinecraftVersion          string `json:"minecraftVersion"`
	LoaderVersion             string `json:"loaderVersion"`
	JavaPath                  string `json:"javaPath"`
	MinMemoryMB               int    `json:"minMemoryMb"`
	MaxMemoryMB               int    `json:"maxMemoryMb"`
	Port                      int    `json:"port"`
	LaunchJar                 string `json:"launchJar"`
	ExtraArgs                 string `json:"extraArgs"`
	ScheduledSnapshotsEnabled bool   `json:"scheduledSnapshotsEnabled"`
	SnapshotIntervalMinutes   int    `json:"snapshotIntervalMinutes"`
	LastScheduledSnapshotAt   string `json:"lastScheduledSnapshotAt"`
	JvmPreset                 string `json:"jvmPreset"`
	RestartPolicy             string `json:"restartPolicy"`
	RestartMaxAttempts        int    `json:"restartMaxAttempts"`
	RestartWindowMinutes      int    `json:"restartWindowMinutes"`
	CreatedAt                 string `json:"createdAt"`
	UpdatedAt                 string `json:"updatedAt"`
}

const (
	RestartPolicyOff     = "off"
	RestartPolicyOnCrash = "on-crash"

	DefaultRestartMaxAttempts   = 3
	DefaultRestartWindowMinutes = 10
)

const serverColumns = `id, name, path, type, minecraft_version, loader_version, java_path,
		min_memory_mb, max_memory_mb, port, launch_jar, extra_args,
		scheduled_snapshots_enabled, snapshot_interval_minutes, last_scheduled_snapshot_at,
		jvm_preset, restart_policy, restart_max_attempts, restart_window_minutes, created_at, updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanServer(row rowScanner) (Server, error) {
	var server Server
	var scheduledSnapshotsEnabled string
	if err := row.Scan(
		&server.ID,
		&server.Name,
		&server.Path,
		&server.Type,
		&server.MinecraftVersion,
		&server.LoaderVersion,
		&server.JavaPath,
		&server.MinMemoryMB,
		&server.MaxMemoryMB,
		&server.Port,
		&server.LaunchJar,
		&server.ExtraArgs,
		&scheduledSnapshotsEnabled,
		&server.SnapshotIntervalMinutes,
		&server.LastScheduledSnapshotAt,
		&server.JvmPreset,
		&server.RestartPolicy,
		&server.RestartMaxAttempts,
		&server.RestartWindowMinutes,
		&server.CreatedAt,
		&server.UpdatedAt,
	); err != nil {
		return Server{}, err
	}
	server.ScheduledSnapshotsEnabled = scheduledSnapshotsEnabled == "true"
	return server, nil
}

// normalizeRestartPolicy fills in defaults and clamps the restart settings so
// the supervisor never sees an unusable policy.
func normalizeRestartPolicy(server *Server) {
	if server.RestartPolicy != RestartPolicyOnCrash {
		server.RestartPolicy = RestartPolicyOff
	}
	if server.RestartMaxAttempts < 1 {
		server.RestartMaxAttempts = DefaultRestartMaxAttempts
	}
	if server.RestartMaxAttempts > 20 {
		server.RestartMaxAttempts = 20
	}
	if server.RestartWindowMinutes < 1 {
		server.RestartWindowMinutes = DefaultRestartWindowMinutes
	}
	if server.RestartWindowMinutes > 1440 {
		server.RestartWindowMinutes = 1440
	}
}

func (s *Store) ListServers(ctx context.Context) ([]Server, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+serverColumns+` FROM servers ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	servers := []Server{}
	for rows.Next() {
		server, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		servers = append(servers, server)
	}
	return servers, rows.Err()
}

func (s *Store) CreateServer(ctx context.Context, input Server) (Server, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	server := input
	if strings.TrimSpace(server.ID) == "" {
		id, err := randomHex(8)
		if err != nil {
			return Server{}, err
		}
		server.ID = "srv_" + id
	}
	server.Name = strings.TrimSpace(server.Name)
	if server.Name == "" {
		return Server{}, errors.New("Server name is required")
	}
	if strings.TrimSpace(server.Path) == "" {
		return Server{}, errors.New("Server path is required")
	}
	if server.Type == "" {
		server.Type = "vanilla"
	}
	if !serverTypeNeedsLoader(server.Type) {
		server.LoaderVersion = ""
	}
	if strings.TrimSpace(server.JavaPath) == "" {
		server.JavaPath = "java"
	}
	if server.MinMemoryMB < 512 {
		return Server{}, errors.New("Min memory must be at least 512 MB")
	}
	if server.MaxMemoryMB < server.MinMemoryMB {
		return Server{}, errors.New("Max memory must be greater than or equal to min memory")
	}
	if server.Port < 1 || server.Port > 65535 {
		return Server{}, errors.New("Server port must be between 1 and 65535")
	}
	if server.CreatedAt == "" {
		server.CreatedAt = now
	}
	server.UpdatedAt = now
	if server.SnapshotIntervalMinutes < 0 {
		server.SnapshotIntervalMinutes = 0
	}
	if server.SnapshotIntervalMinutes == 0 {
		server.ScheduledSnapshotsEnabled = false
	}
	normalizeRestartPolicy(&server)
	_, err := s.db.ExecContext(ctx, `INSERT INTO servers (`+serverColumns+`)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		server.ID,
		server.Name,
		server.Path,
		server.Type,
		server.MinecraftVersion,
		server.LoaderVersion,
		server.JavaPath,
		server.MinMemoryMB,
		server.MaxMemoryMB,
		server.Port,
		server.LaunchJar,
		server.ExtraArgs,
		boolText(server.ScheduledSnapshotsEnabled),
		server.SnapshotIntervalMinutes,
		server.LastScheduledSnapshotAt,
		server.JvmPreset,
		server.RestartPolicy,
		server.RestartMaxAttempts,
		server.RestartWindowMinutes,
		server.CreatedAt,
		server.UpdatedAt,
	)
	if err != nil {
		return Server{}, err
	}
	return server, nil
}

func (s *Store) GetServer(ctx context.Context, id string) (Server, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+serverColumns+` FROM servers WHERE id = ?`, id)
	server, err := scanServer(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return Server{}, false, nil
		}
		return Server{}, false, err
	}
	return server, true, nil
}

func (s *Store) UpdateServer(ctx context.Context, id string, input Server) (Server, error) {
	current, ok, err := s.GetServer(ctx, id)
	if err != nil {
		return Server{}, err
	}
	if !ok {
		return Server{}, errors.New("Server not found")
	}
	next := current
	if strings.TrimSpace(input.Name) != "" {
		next.Name = strings.TrimSpace(input.Name)
	}
	if input.Type != "" {
		next.Type = input.Type
	}
	if input.MinecraftVersion != "" {
		next.MinecraftVersion = input.MinecraftVersion
	}
	if !serverTypeNeedsLoader(input.Type) {
		next.LoaderVersion = ""
	} else if input.LoaderVersion != "" {
		next.LoaderVersion = input.LoaderVersion
	}
	if strings.TrimSpace(input.JavaPath) != "" {
		next.JavaPath = strings.TrimSpace(input.JavaPath)
	}
	if input.MinMemoryMB > 0 {
		next.MinMemoryMB = input.MinMemoryMB
	}
	if input.MaxMemoryMB > 0 {
		next.MaxMemoryMB = input.MaxMemoryMB
	}
	if input.Port > 0 {
		next.Port = input.Port
	}
	if strings.TrimSpace(input.LaunchJar) != "" {
		next.LaunchJar = strings.TrimSpace(input.LaunchJar)
	}
	if input.ExtraArgs != "" {
		next.ExtraArgs = input.ExtraArgs
	}
	next.ScheduledSnapshotsEnabled = input.ScheduledSnapshotsEnabled
	next.SnapshotIntervalMinutes = input.SnapshotIntervalMinutes
	next.LastScheduledSnapshotAt = input.LastScheduledSnapshotAt
	if next.SnapshotIntervalMinutes < 0 {
		next.SnapshotIntervalMinutes = 0
	}
	if next.SnapshotIntervalMinutes == 0 {
		next.ScheduledSnapshotsEnabled = false
	}
	// The profile editor sends the restart settings together, but internal
	// callers (launch-target fixes, port changes) send a sparse Server, so the
	// policy and JVM preset are only replaced when a policy is given.
	if input.RestartPolicy != "" {
		next.RestartPolicy = input.RestartPolicy
		next.RestartMaxAttempts = input.RestartMaxAttempts
		next.RestartWindowMinutes = input.RestartWindowMinutes
		next.JvmPreset = input.JvmPreset
	}
	normalizeRestartPolicy(&next)
	next.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	_, err = s.db.ExecContext(ctx, `UPDATE servers SET
		name = ?, type = ?, minecraft_version = ?, loader_version = ?, java_path = ?,
		min_memory_mb = ?, max_memory_mb = ?, port = ?, launch_jar = ?, extra_args = ?,
		scheduled_snapshots_enabled = ?, snapshot_interval_minutes = ?, last_scheduled_snapshot_at = ?,
		jvm_preset = ?, restart_policy = ?, restart_max_attempts = ?, restart_window_minutes = ?, updated_at = ?
		WHERE id = ?`,
		next.Name,
		next.Type,
		next.MinecraftVersion,
		next.LoaderVersion,
		next.JavaPath,
		next.MinMemoryMB,
		next.MaxMemoryMB,
		next.Port,
		next.LaunchJar,
		next.ExtraArgs,
		boolText(next.ScheduledSnapshotsEnabled),
		next.SnapshotIntervalMinutes,
		next.LastScheduledSnapshotAt,
		next.JvmPreset,
		next.RestartPolicy,
		next.RestartMaxAttempts,
		next.RestartWindowMinutes,
		next.UpdatedAt,
		id,
	)
	if err != nil {
		return Server{}, err
	}
	return next, nil
}

func (s *Store) UpdateServerPort(ctx context.Context, id string, port int) error {
	if port < 1 || port > 65535 {
		return errors.New("Server port must be between 1 and 65535")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE servers SET port = ?, updated_at = ? WHERE id = ?`, port, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (s *Store) MarkScheduledSnapshot(ctx context.Context, id string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE servers SET last_scheduled_snapshot_at = ?, updated_at = ? WHERE id = ?`, at.UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (s *Store) DeleteServerRecord(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM servers WHERE id = ?`, id); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM backups WHERE server_id = ?`, id); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM command_presets WHERE server_id = ?`, id); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM public_access WHERE server_id = ?`, id); err != nil {
		return err
	}
	return nil
}
