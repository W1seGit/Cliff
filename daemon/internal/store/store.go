package store

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type Settings struct {
	ServerRoot       string `json:"serverRoot"`
	SnapshotsEnabled bool   `json:"snapshotsEnabled"`
	CurseForgeAPIKey string `json:"curseForgeApiKey"`
}

const passwordIterations = 210_000
const sessionDays = 14

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
	SnapshotsEnabled          bool   `json:"snapshotsEnabled"`
	ScheduledSnapshotsEnabled bool   `json:"scheduledSnapshotsEnabled"`
	SnapshotIntervalMinutes   int    `json:"snapshotIntervalMinutes"`
	LastScheduledSnapshotAt   string `json:"lastScheduledSnapshotAt"`
	CreatedAt                 string `json:"createdAt"`
	UpdatedAt                 string `json:"updatedAt"`
}

type CommandPreset struct {
	ID        string `json:"id"`
	ServerID  string `json:"serverId"`
	Command   string `json:"command"`
	CreatedAt string `json:"createdAt"`
}

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

type Backup struct {
	ID           string `json:"id"`
	ServerID     string `json:"serverId,omitempty"`
	Reason       string `json:"reason"`
	SnapshotPath string `json:"snapshotPath"`
	CreatedAt    string `json:"createdAt"`
	SizeBytes    int64  `json:"sizeBytes"`
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
			snapshots_enabled TEXT NOT NULL DEFAULT 'true',
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

	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO settings (key, value) VALUES ('serverRoot', ?)`, defaultServerRoot); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO settings (key, value) VALUES ('snapshotsEnabled', 'true')`); err != nil {
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
		SnapshotsEnabled: values["snapshotsEnabled"] != "false",
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

func (s *Store) HasUser(ctx context.Context) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Store) CreateUser(ctx context.Context, username string, password string) (User, error) {
	hasUser, err := s.HasUser(ctx)
	if err != nil {
		return User{}, err
	}
	if hasUser {
		return User{}, errors.New("Initial user already exists")
	}
	if len(stringsTrim(username)) < 3 {
		return User{}, errors.New("Username must be at least 3 characters")
	}
	if len(password) < 10 {
		return User{}, errors.New("Password must be at least 10 characters")
	}

	id, err := randomHex(16)
	if err != nil {
		return User{}, err
	}
	passwordHash, err := hashPassword(password, "")
	if err != nil {
		return User{}, err
	}
	user := User{ID: id, Username: stringsTrim(username)}
	_, err = s.db.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, created_at) VALUES (?, ?, ?, ?)`, user.ID, user.Username, passwordHash, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return User{}, err
	}
	return user, nil
}

func (s *Store) Authenticate(ctx context.Context, username string, password string) (User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, username, password_hash FROM users WHERE username = ?`, stringsTrim(username))
	var user User
	var passwordHash string
	if err := row.Scan(&user.ID, &user.Username, &passwordHash); err != nil {
		if err == sql.ErrNoRows {
			return User{}, errors.New("Invalid username or password")
		}
		return User{}, err
	}
	if !verifyPassword(password, passwordHash) {
		return User{}, errors.New("Invalid username or password")
	}
	return user, nil
}

func (s *Store) UpdateUserAccount(ctx context.Context, userID string, username string, currentPassword string, newPassword string) (User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, username, password_hash FROM users WHERE id = ?`, userID)
	var user User
	var passwordHash string
	if err := row.Scan(&user.ID, &user.Username, &passwordHash); err != nil {
		if err == sql.ErrNoRows {
			return User{}, errors.New("Account not found")
		}
		return User{}, err
	}

	nextUsername := strings.TrimSpace(username)
	if nextUsername == "" {
		nextUsername = user.Username
	}
	if len(nextUsername) < 3 {
		return User{}, errors.New("Username must be at least 3 characters")
	}
	if newPassword != "" {
		if len(newPassword) < 10 {
			return User{}, errors.New("Password must be at least 10 characters")
		}
		if !verifyPassword(currentPassword, passwordHash) {
			return User{}, errors.New("Current password is incorrect")
		}
	}

	var existingID string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE username = ? AND id <> ?`, nextUsername, user.ID).Scan(&existingID)
	if err != nil && err != sql.ErrNoRows {
		return User{}, err
	}
	if existingID != "" {
		return User{}, errors.New("Username is already in use")
	}

	if newPassword != "" {
		nextHash, err := hashPassword(newPassword, "")
		if err != nil {
			return User{}, err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET username = ?, password_hash = ? WHERE id = ?`, nextUsername, nextHash, user.ID); err != nil {
			return User{}, err
		}
	} else if _, err := s.db.ExecContext(ctx, `UPDATE users SET username = ? WHERE id = ?`, nextUsername, user.ID); err != nil {
		return User{}, err
	}

	return User{ID: user.ID, Username: nextUsername}, nil
}

func (s *Store) CreateSession(ctx context.Context, userID string) (string, time.Time, error) {
	sessionID, err := randomHex(32)
	if err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().UTC().Add(sessionDays * 24 * time.Hour)
	_, err = s.db.ExecContext(ctx, `INSERT INTO sessions (id, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`, sessionID, userID, expires.Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return "", time.Time{}, err
	}
	return sessionID, expires, nil
}

func (s *Store) UserBySession(ctx context.Context, sessionID string) (User, bool, error) {
	if sessionID == "" {
		return User{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `SELECT users.id, users.username FROM sessions JOIN users ON users.id = sessions.user_id WHERE sessions.id = ? AND sessions.expires_at > ?`, sessionID, time.Now().UTC().Format(time.RFC3339))
	var user User
	if err := row.Scan(&user.ID, &user.Username); err != nil {
		if err == sql.ErrNoRows {
			return User{}, false, nil
		}
		return User{}, false, err
	}
	return user, true, nil
}

func (s *Store) DeleteSession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID)
	return err
}

func (s *Store) ListServers(ctx context.Context) ([]Server, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		id, name, path, type, minecraft_version, loader_version, java_path,
		min_memory_mb, max_memory_mb, port, launch_jar, extra_args,
		snapshots_enabled, scheduled_snapshots_enabled, snapshot_interval_minutes, last_scheduled_snapshot_at, created_at, updated_at
		FROM servers ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	servers := []Server{}
	for rows.Next() {
		var server Server
		var snapshotsEnabled string
		var scheduledSnapshotsEnabled string
		if err := rows.Scan(
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
			&snapshotsEnabled,
			&scheduledSnapshotsEnabled,
			&server.SnapshotIntervalMinutes,
			&server.LastScheduledSnapshotAt,
			&server.CreatedAt,
			&server.UpdatedAt,
		); err != nil {
			return nil, err
		}
		server.SnapshotsEnabled = snapshotsEnabled != "false"
		server.ScheduledSnapshotsEnabled = scheduledSnapshotsEnabled == "true"
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
	if !server.SnapshotsEnabled {
		server.SnapshotsEnabled = true
	}
	if server.SnapshotIntervalMinutes < 0 {
		server.SnapshotIntervalMinutes = 0
	}
	if server.SnapshotIntervalMinutes == 0 {
		server.ScheduledSnapshotsEnabled = false
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO servers (
		id, name, path, type, minecraft_version, loader_version, java_path,
		min_memory_mb, max_memory_mb, port, launch_jar, extra_args,
		snapshots_enabled, scheduled_snapshots_enabled, snapshot_interval_minutes, last_scheduled_snapshot_at, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
		boolText(server.SnapshotsEnabled),
		boolText(server.ScheduledSnapshotsEnabled),
		server.SnapshotIntervalMinutes,
		server.LastScheduledSnapshotAt,
		server.CreatedAt,
		server.UpdatedAt,
	)
	if err != nil {
		return Server{}, err
	}
	return server, nil
}

func randomHex(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func hashPassword(password string, salt string) (string, error) {
	if salt == "" {
		nextSalt, err := randomHex(16)
		if err != nil {
			return "", err
		}
		salt = nextSalt
	}
	saltBytes := []byte(salt)
	hash := pbkdf2SHA256([]byte(password), saltBytes, passwordIterations, 32)
	return fmt.Sprintf("%s:%s", salt, hex.EncodeToString(hash)), nil
}

func verifyPassword(password string, stored string) bool {
	salt, expectedHex, ok := stringsCut(stored, ":")
	if !ok {
		return false
	}
	candidate, err := hashPassword(password, salt)
	if err != nil {
		return false
	}
	_, candidateHex, ok := stringsCut(candidate, ":")
	if !ok {
		return false
	}
	expected, err := hex.DecodeString(expectedHex)
	if err != nil {
		return false
	}
	actual, err := hex.DecodeString(candidateHex)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(expected, actual) == 1
}

func pbkdf2SHA256(password []byte, salt []byte, iterations int, keyLength int) []byte {
	hashLength := sha256.Size
	blocks := (keyLength + hashLength - 1) / hashLength
	output := make([]byte, 0, blocks*hashLength)
	for block := 1; block <= blocks; block++ {
		mac := hmac.New(sha256.New, password)
		mac.Write(salt)
		mac.Write([]byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)})
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iterations; i++ {
			mac = hmac.New(sha256.New, password)
			mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		output = append(output, t...)
	}
	return output[:keyLength]
}

func stringsTrim(value string) string {
	start := 0
	end := len(value)
	for start < end && (value[start] == ' ' || value[start] == '\t' || value[start] == '\n' || value[start] == '\r') {
		start++
	}
	for end > start && (value[end-1] == ' ' || value[end-1] == '\t' || value[end-1] == '\n' || value[end-1] == '\r') {
		end--
	}
	return value[start:end]
}

func stringsCut(value string, separator string) (string, string, bool) {
	for i := 0; i+len(separator) <= len(value); i++ {
		if value[i:i+len(separator)] == separator {
			return value[:i], value[i+len(separator):], true
		}
	}
	return value, "", false
}

func (s *Store) GetServer(ctx context.Context, id string) (Server, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT
		id, name, path, type, minecraft_version, loader_version, java_path,
		min_memory_mb, max_memory_mb, port, launch_jar, extra_args,
		snapshots_enabled, scheduled_snapshots_enabled, snapshot_interval_minutes, last_scheduled_snapshot_at, created_at, updated_at
		FROM servers WHERE id = ?`, id)

	var server Server
	var snapshotsEnabled string
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
		&snapshotsEnabled,
		&scheduledSnapshotsEnabled,
		&server.SnapshotIntervalMinutes,
		&server.LastScheduledSnapshotAt,
		&server.CreatedAt,
		&server.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return Server{}, false, nil
		}
		return Server{}, false, err
	}
	server.SnapshotsEnabled = snapshotsEnabled != "false"
	server.ScheduledSnapshotsEnabled = scheduledSnapshotsEnabled == "true"
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
	next.SnapshotsEnabled = input.SnapshotsEnabled
	next.ScheduledSnapshotsEnabled = input.ScheduledSnapshotsEnabled
	next.SnapshotIntervalMinutes = input.SnapshotIntervalMinutes
	next.LastScheduledSnapshotAt = input.LastScheduledSnapshotAt
	if next.SnapshotIntervalMinutes < 0 {
		next.SnapshotIntervalMinutes = 0
	}
	if next.SnapshotIntervalMinutes == 0 {
		next.ScheduledSnapshotsEnabled = false
	}
	next.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	_, err = s.db.ExecContext(ctx, `UPDATE servers SET
		name = ?, type = ?, minecraft_version = ?, loader_version = ?, java_path = ?,
		min_memory_mb = ?, max_memory_mb = ?, port = ?, launch_jar = ?, extra_args = ?,
		snapshots_enabled = ?, scheduled_snapshots_enabled = ?, snapshot_interval_minutes = ?, last_scheduled_snapshot_at = ?, updated_at = ?
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
		boolText(next.SnapshotsEnabled),
		boolText(next.ScheduledSnapshotsEnabled),
		next.SnapshotIntervalMinutes,
		next.LastScheduledSnapshotAt,
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
