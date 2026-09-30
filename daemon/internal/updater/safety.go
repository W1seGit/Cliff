package updater

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// ApplyHooks lets the caller run work at the safe point in an update: after
// the new version is downloaded, verified and unpacked, and before any file is
// replaced. Stopping servers and backing up data belong here, so a failed
// download never costs the user a running server.
type ApplyHooks struct {
	BeforeSwap func(ctx context.Context) error
}

const previousSuffix = ".previous"

// PreUpdateBackupDir is where database copies taken before an update are kept.
func PreUpdateBackupDir(dataDir string) string {
	return filepath.Join(dataDir, "updates", "pre-update-backups")
}

// PreUpdateBackupPath names a new database backup for the version being replaced.
func PreUpdateBackupPath(dataDir string, currentVersion string) string {
	version := strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(strings.TrimPrefix(currentVersion, "v"))
	if version == "" {
		version = "unknown"
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	return filepath.Join(PreUpdateBackupDir(dataDir), fmt.Sprintf("dashboard-v%s-%s.sqlite", version, stamp))
}

// PruneBackups keeps only the newest `keep` files in dir.
func PruneBackups(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type item struct {
		path string
		mod  time.Time
	}
	files := []item{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, item{filepath.Join(dir, entry.Name()), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for index := keep; index < len(files); index++ {
		_ = os.Remove(files[index].path)
	}
}

// CopyDatabase copies a database file that nothing has open, such as after the
// daemon has stopped and checkpointed it.
func CopyDatabase(src string, dest string) error {
	if _, err := os.Stat(src); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return copyFile(src, dest)
}

// Rollback puts the program files from before the last update back in place.
// It returns what it restored. Servers, worlds, settings and the database are
// never touched.
func Rollback(binaryPath string, webDir string) ([]string, error) {
	restored := []string{}
	previousBinary := binaryPath + previousSuffix
	previousWeb := webDir + previousSuffix

	if !fileExists(previousBinary) && !dirExists(previousWeb) {
		return nil, errors.New("there is no previous version to go back to; it is kept only after an update")
	}

	if fileExists(previousBinary) {
		if runtime.GOOS == "windows" {
			// A running exe can be renamed but not overwritten.
			aside := binaryPath + ".rolledback"
			_ = os.Remove(aside)
			if err := os.Rename(binaryPath, aside); err != nil {
				return restored, fmt.Errorf("move the current program aside: %w", err)
			}
			if err := copyFile(previousBinary, binaryPath); err != nil {
				_ = os.Rename(aside, binaryPath)
				return restored, fmt.Errorf("restore the previous program: %w", err)
			}
			// The program that was replaced is no longer needed; ignore it if it is still locked.
			_ = os.Remove(aside)
		} else {
			temp := binaryPath + ".rollback-tmp"
			if err := copyFile(previousBinary, temp); err != nil {
				return restored, fmt.Errorf("restore the previous program: %w", err)
			}
			if err := os.Chmod(temp, 0o755); err != nil {
				_ = os.Remove(temp)
				return restored, err
			}
			if err := os.Rename(temp, binaryPath); err != nil {
				_ = os.Remove(temp)
				return restored, fmt.Errorf("restore the previous program: %w", err)
			}
		}
		restored = append(restored, binaryPath)
	}

	if dirExists(previousWeb) {
		failed := webDir + ".rolledback"
		_ = os.RemoveAll(failed)
		if dirExists(webDir) {
			if err := os.Rename(webDir, failed); err != nil {
				return restored, fmt.Errorf("move the current dashboard files aside: %w", err)
			}
		}
		if err := copyDir(previousWeb, webDir); err != nil {
			_ = os.RemoveAll(webDir)
			_ = os.Rename(failed, webDir)
			return restored, fmt.Errorf("restore the previous dashboard files: %w", err)
		}
		_ = os.RemoveAll(failed)
		restored = append(restored, webDir)
	}
	return restored, nil
}

// SafetyInfo describes the copies Cliff keeps so an update can be undone.
type SafetyInfo struct {
	// CanRollback is true while the previous version is still on disk.
	CanRollback          bool   `json:"canRollback"`
	PreviousVersionBytes int64  `json:"previousVersionBytes"`
	BackupCount          int    `json:"backupCount"`
	BackupBytes          int64  `json:"backupBytes"`
	TotalBytes           int64  `json:"totalBytes"`
	BackupDir            string `json:"backupDir"`
}

func pathSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	if !info.IsDir() {
		return info.Size()
	}
	var total int64
	_ = filepath.WalkDir(path, func(_ string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if info, err := entry.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// SafetyInfo reports what the update safety copies use on disk.
func (m *Manager) SafetyInfo() SafetyInfo {
	info := SafetyInfo{BackupDir: PreUpdateBackupDir(m.dataDir)}
	info.PreviousVersionBytes = pathSize(m.binaryPath+previousSuffix) + pathSize(m.webDir+previousSuffix)
	info.CanRollback = fileExists(m.binaryPath+previousSuffix) || dirExists(m.webDir+previousSuffix)
	if entries, err := os.ReadDir(info.BackupDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			info.BackupCount++
			if fileInfo, err := entry.Info(); err == nil {
				info.BackupBytes += fileInfo.Size()
			}
		}
	}
	info.TotalBytes = info.PreviousVersionBytes + info.BackupBytes
	return info
}

// EstimateSafetyCopyBytes is roughly how much disk the next update will keep:
// a copy of the current program and dashboard files.
func (m *Manager) EstimateSafetyCopyBytes() int64 {
	return pathSize(m.binaryPath) + pathSize(m.webDir)
}

// ClearSafetyCopies deletes the previous version and the database copies taken
// before updates. After this, `cliff rollback` has nothing to go back to. It
// returns how many bytes were freed. It refuses while an update is running.
func (m *Manager) ClearSafetyCopies() (int64, error) {
	m.mu.RLock()
	applying := m.applying
	m.mu.RUnlock()
	if applying {
		return 0, errors.New("an update is running; try again when it has finished")
	}
	before := m.SafetyInfo().TotalBytes
	var failures []string
	for _, target := range []string{
		m.binaryPath + previousSuffix,
		m.binaryPath + ".rolledback",
		m.webDir + previousSuffix,
		m.webDir + ".rolledback",
		PreUpdateBackupDir(m.dataDir),
	} {
		if _, err := os.Stat(target); err != nil {
			continue
		}
		if err := os.RemoveAll(target); err != nil {
			failures = append(failures, filepath.Base(target))
		}
	}
	freed := before - m.SafetyInfo().TotalBytes
	if len(failures) > 0 {
		return freed, fmt.Errorf("could not remove %s (it may still be in use)", strings.Join(failures, ", "))
	}
	return freed, nil
}
