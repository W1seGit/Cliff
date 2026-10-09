package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type User struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Role        string `json:"role"`
	TotpEnabled bool   `json:"totpEnabled"`
}

type Settings struct {
	ServerRoot       string `json:"serverRoot"`
	CurseForgeAPIKey string `json:"curseForgeApiKey"`
}

func Open(path string, defaultServerRoot string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	store := &Store{db: db}
	if err := store.migrate(defaultServerRoot); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// BackupTo writes a consistent copy of the database to dest while it is in use.
func (s *Store) BackupTo(ctx context.Context, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	_ = os.Remove(dest)
	_, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, dest)
	return err
}

func (s *Store) migrate(defaultServerRoot string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	statements := []string{
		`PRAGMA busy_timeout = 5000`,
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS servers (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			path TEXT NOT NULL,
			type TEXT NOT NULL,
			minecraft_version TEXT NOT NULL,
			loader_version TEXT NOT NULL,
			java_path TEXT NOT NULL,
			min_memory_mb INTEGER NOT NULL,
			max_memory_mb INTEGER NOT NULL,
			port INTEGER NOT NULL,
			launch_jar TEXT NOT NULL,
			extra_args TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS backups (
			id TEXT PRIMARY KEY,
			server_id TEXT NOT NULL,
			reason TEXT NOT NULL,
			snapshot_path TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS command_presets (
			id TEXT PRIMARY KEY,
			server_id TEXT NOT NULL,
			command TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS user_permissions (
			user_id TEXT NOT NULL,
			server_id TEXT NOT NULL,
			permissions TEXT NOT NULL,
			PRIMARY KEY (user_id, server_id)
		)`,
		`CREATE TABLE IF NOT EXISTS webhooks (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			url TEXT NOT NULL,
			kind TEXT NOT NULL,
			events TEXT NOT NULL,
			enabled TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS public_access (
			server_id TEXT PRIMARY KEY,
			provider TEXT NOT NULL,
			public_address TEXT NOT NULL,
			local_host TEXT NOT NULL,
			local_port INTEGER NOT NULL,
			agent_path TEXT NOT NULL,
			claimed TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
	}

	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if err := s.ensureColumn(ctx, "servers", "scheduled_snapshots_enabled", "TEXT NOT NULL DEFAULT 'false'"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "servers", "snapshot_interval_minutes", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "servers", "last_scheduled_snapshot_at", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	for _, column := range []struct{ table, name, definition string }{
		{"users", "role", "TEXT NOT NULL DEFAULT 'admin'"},
		{"users", "totp_secret", "TEXT NOT NULL DEFAULT ''"},
		{"users", "totp_enabled", "TEXT NOT NULL DEFAULT 'false'"},
		{"users", "totp_last_step", "INTEGER NOT NULL DEFAULT 0"},
		{"users", "totp_recovery", "TEXT NOT NULL DEFAULT ''"},
		{"servers", "jvm_preset", "TEXT NOT NULL DEFAULT ''"},
		{"servers", "restart_policy", "TEXT NOT NULL DEFAULT 'off'"},
		{"servers", "restart_max_attempts", "INTEGER NOT NULL DEFAULT 3"},
		{"servers", "restart_window_minutes", "INTEGER NOT NULL DEFAULT 10"},
	} {
		if err := s.ensureColumn(ctx, column.table, column.name, column.definition); err != nil {
			return err
		}
	}

	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO settings (key, value) VALUES ('serverRoot', ?)`, defaultServerRoot); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO settings (key, value) VALUES ('curseForgeApiKey', '')`); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureColumn(ctx context.Context, table string, column string, definition string) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name string
		var dataType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+column+` `+definition)
	return err
}

func (s *Store) Settings(ctx context.Context) (Settings, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return Settings{}, err
	}
	defer rows.Close()

	values := map[string]string{}
	for rows.Next() {
		var key string
		var value string
		if err := rows.Scan(&key, &value); err != nil {
			return Settings{}, err
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return Settings{}, err
	}

	return Settings{
		ServerRoot:       values["serverRoot"],
		CurseForgeAPIKey: values["curseForgeApiKey"],
	}, nil
}

func (s *Store) UpdateSettings(ctx context.Context, input Settings) error {
	if input.CurseForgeAPIKey != "configured" {
		if _, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO settings (key, value) VALUES ('curseForgeApiKey', ?)`, input.CurseForgeAPIKey); err != nil {
			return err
		}
	}
	return nil
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func serverTypeNeedsLoader(serverType string) bool {
	switch serverType {
	case "fabric", "forge", "neoforge":
		return true
	default:
		return false
	}
}
