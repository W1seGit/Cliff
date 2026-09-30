package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// legacySchemas are databases as older releases created them. An update must
// open each one, keep every row, and keep working afterwards.
var legacySchemas = map[string][]string{
	"before scheduled snapshots": {
		`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT UNIQUE NOT NULL, password_hash TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, user_id TEXT NOT NULL, expires_at TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE servers (
			id TEXT PRIMARY KEY, name TEXT NOT NULL, path TEXT NOT NULL, type TEXT NOT NULL,
			minecraft_version TEXT NOT NULL, loader_version TEXT NOT NULL, java_path TEXT NOT NULL,
			min_memory_mb INTEGER NOT NULL, max_memory_mb INTEGER NOT NULL, port INTEGER NOT NULL,
			launch_jar TEXT NOT NULL, extra_args TEXT NOT NULL,
			snapshots_enabled TEXT NOT NULL DEFAULT 'true',
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE backups (id TEXT PRIMARY KEY, server_id TEXT NOT NULL, reason TEXT NOT NULL, snapshot_path TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE command_presets (id TEXT PRIMARY KEY, server_id TEXT NOT NULL, command TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE public_access (
			server_id TEXT PRIMARY KEY, provider TEXT NOT NULL, public_address TEXT NOT NULL, local_host TEXT NOT NULL,
			local_port INTEGER NOT NULL, agent_path TEXT NOT NULL, claimed TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		)`,
		`INSERT INTO settings (key, value) VALUES ('serverRoot', 'C:/old/servers'), ('snapshotsEnabled', 'false')`,
	},
	"with scheduled snapshots": {
		`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT UNIQUE NOT NULL, password_hash TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, user_id TEXT NOT NULL, expires_at TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE servers (
			id TEXT PRIMARY KEY, name TEXT NOT NULL, path TEXT NOT NULL, type TEXT NOT NULL,
			minecraft_version TEXT NOT NULL, loader_version TEXT NOT NULL, java_path TEXT NOT NULL,
			min_memory_mb INTEGER NOT NULL, max_memory_mb INTEGER NOT NULL, port INTEGER NOT NULL,
			launch_jar TEXT NOT NULL, extra_args TEXT NOT NULL,
			snapshots_enabled TEXT NOT NULL DEFAULT 'true',
			scheduled_snapshots_enabled TEXT NOT NULL DEFAULT 'false',
			snapshot_interval_minutes INTEGER NOT NULL DEFAULT 0,
			last_scheduled_snapshot_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE backups (id TEXT PRIMARY KEY, server_id TEXT NOT NULL, reason TEXT NOT NULL, snapshot_path TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE command_presets (id TEXT PRIMARY KEY, server_id TEXT NOT NULL, command TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE public_access (
			server_id TEXT PRIMARY KEY, provider TEXT NOT NULL, public_address TEXT NOT NULL, local_host TEXT NOT NULL,
			local_port INTEGER NOT NULL, agent_path TEXT NOT NULL, claimed TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		)`,
		`INSERT INTO settings (key, value) VALUES ('serverRoot', 'C:/old/servers'), ('snapshotsEnabled', 'true')`,
	},
}

func writeLegacyDatabase(t *testing.T, path string, statements []string, scheduled bool) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("legacy setup failed: %v\n%s", err, statement)
		}
	}
	if _, err := db.Exec(`INSERT INTO servers (id, name, path, type, minecraft_version, loader_version, java_path,
		min_memory_mb, max_memory_mb, port, launch_jar, extra_args, created_at, updated_at)
		VALUES ('srv_old', 'Old World', 'C:/old/servers/old', 'fabric', '1.20.1', '0.15.0', 'java', 512, 2048, 25565, 'fabric.jar', '', '2025-01-01T00:00:00Z', '2025-01-02T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if scheduled {
		if _, err := db.Exec(`UPDATE servers SET scheduled_snapshots_enabled = 'true', snapshot_interval_minutes = 120 WHERE id = 'srv_old'`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO backups (id, server_id, reason, snapshot_path, created_at) VALUES ('bak_1', 'srv_old', 'manual snapshot', 'C:/old/backups/1', '2025-01-03T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, username, password_hash, created_at) VALUES ('usr_1', 'leo', 'hash', '2025-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
}

func TestOpenUpgradesOlderDatabases(t *testing.T) {
	for name, statements := range legacySchemas {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "dashboard.sqlite")
			writeLegacyDatabase(t, path, statements, name == "with scheduled snapshots")

			db, err := Open(path, "C:/new/servers")
			if err != nil {
				t.Fatalf("the current version could not open an older database: %v", err)
			}
			defer db.Close()
			ctx := context.Background()

			servers, err := db.ListServers(ctx)
			if err != nil || len(servers) != 1 || servers[0].Name != "Old World" || servers[0].MaxMemoryMB != 2048 {
				t.Fatalf("existing servers must survive the upgrade, got %#v (err=%v)", servers, err)
			}
			if name == "with scheduled snapshots" && (!servers[0].ScheduledSnapshotsEnabled || servers[0].SnapshotIntervalMinutes != 120) {
				t.Fatalf("the snapshot schedule must survive, got %#v", servers[0])
			}
			if name == "before scheduled snapshots" && servers[0].ScheduledSnapshotsEnabled {
				t.Fatalf("no schedule existed, none should appear: %#v", servers[0])
			}

			backups, err := db.ListBackups(ctx, "srv_old")
			if err != nil || len(backups) != 1 || backups[0].Reason != "manual snapshot" {
				t.Fatalf("existing snapshots must survive, got %#v (err=%v)", backups, err)
			}
			if has, err := db.HasUser(ctx); err != nil || !has {
				t.Fatalf("existing users must survive (has=%v err=%v)", has, err)
			}
			settings, err := db.Settings(ctx)
			if err != nil || settings.ServerRoot != "C:/old/servers" {
				t.Fatalf("existing settings must survive, got %#v (err=%v)", settings, err)
			}

			// Writes still work against the old table layout.
			created, err := db.CreateServer(ctx, Server{
				Name: "New World", Path: filepath.Join(dir, "new"), Type: "vanilla", MinecraftVersion: "1.21.1",
				JavaPath: "java", MinMemoryMB: 512, MaxMemoryMB: 1024, Port: 25570, LaunchJar: "server.jar",
			})
			if err != nil {
				t.Fatalf("creating a server on an upgraded database failed: %v", err)
			}
			updated, err := db.UpdateServer(ctx, created.ID, Server{
				Name: "Renamed", ScheduledSnapshotsEnabled: true, SnapshotIntervalMinutes: 60,
			})
			if err != nil || updated.Name != "Renamed" || !updated.ScheduledSnapshotsEnabled {
				t.Fatalf("updating a server on an upgraded database failed: %#v (err=%v)", updated, err)
			}
			reloaded, ok, err := db.GetServer(ctx, created.ID)
			if err != nil || !ok || reloaded.Name != "Renamed" || reloaded.SnapshotIntervalMinutes != 60 {
				t.Fatalf("changes must be stored, got %#v (ok=%v err=%v)", reloaded, ok, err)
			}
		})
	}
}

func TestBackupToProducesAnOpenableCopy(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "dashboard.sqlite"), "C:/servers")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.CreateServer(ctx, Server{
		Name: "Keep Me", Path: filepath.Join(dir, "s"), Type: "vanilla", MinecraftVersion: "1.21.1",
		JavaPath: "java", MinMemoryMB: 512, MaxMemoryMB: 1024, Port: 25565, LaunchJar: "server.jar",
	}); err != nil {
		t.Fatal(err)
	}

	backupPath := filepath.Join(dir, "updates", "nested", "copy.sqlite")
	if err := db.BackupTo(ctx, backupPath); err != nil {
		t.Fatalf("backup failed: %v", err)
	}
	// A second backup to the same name must replace the first, not fail.
	if err := db.BackupTo(ctx, backupPath); err != nil {
		t.Fatalf("repeat backup failed: %v", err)
	}

	copyDB, err := Open(backupPath, "C:/servers")
	if err != nil {
		t.Fatalf("the backup must open: %v", err)
	}
	defer copyDB.Close()
	servers, err := copyDB.ListServers(ctx)
	if err != nil || len(servers) != 1 || servers[0].Name != "Keep Me" {
		t.Fatalf("the backup must hold the data, got %#v (err=%v)", servers, err)
	}
}
