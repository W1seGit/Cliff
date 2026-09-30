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
