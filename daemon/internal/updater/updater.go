package updater

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/buildinfo"
)

const (
	// ManifestURL is the stable URL for the latest release manifest.
	ManifestURL = "https://github.com/W1seGit/Cliff/releases/latest/download/cliff-release.json"
	// CheckInterval is how often the background checker polls for updates.
	CheckInterval = 6 * time.Hour
	// HTTPTimeout for manifest fetches and archive downloads.
	HTTPTimeout = 10 * time.Minute
)

// ReleaseManifest mirrors the cliff-release.json written by ci-package.mjs.
type ReleaseManifest struct {
	SchemaVersion int              `json:"schemaVersion"`
	Name          string           `json:"name"`
	Version       string           `json:"version"`
	Commit        string           `json:"commit"`
	BuiltAt       string           `json:"builtAt"`
	Platforms     []PlatformAsset  `json:"platforms"`
	Commands      *ReleaseCommands `json:"commands,omitempty"`
}

type PlatformAsset struct {
	Platform  string `json:"platform"`
	Archive   string `json:"archive"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
}

type ReleaseCommands struct {
	Install struct {
		Windows string `json:"windows"`
		Unix    string `json:"unix"`
	} `json:"install"`
}

// CheckResult is what the API returns to the frontend.
type CheckResult struct {
	CurrentVersion string         `json:"currentVersion"`
	CurrentCommit  string         `json:"currentCommit"`
	LatestVersion  string         `json:"latestVersion"`
	LatestCommit   string         `json:"latestCommit"`
	UpdateAvailable bool          `json:"updateAvailable"`
	ReleaseURL     string         `json:"releaseUrl,omitempty"`
	ArchiveName    string         `json:"archiveName,omitempty"`
	ArchiveSize    int64          `json:"archiveSize,omitempty"`
	BuiltAt        string         `json:"builtAt,omitempty"`
	CheckedAt      string         `json:"checkedAt"`
	Error          string         `json:"error,omitempty"`
}

// ApplyResult is returned after applying an update.
type ApplyResult struct {
	Success      bool   `json:"success"`
	Message      string `json:"message"`
	NewVersion   string `json:"newVersion,omitempty"`
	Restarting   bool   `json:"restarting"`
}

// Manager coordinates update checks and application.
type Manager struct {
	mu         sync.RWMutex
	manifest   *ReleaseManifest
	lastCheck  time.Time
	lastError  string
	checking   bool
	applying   bool
	dataDir    string
	webDir     string
	binaryPath string
	client     *http.Client
}

// NewManager creates an updater manager.
// binaryPath is the path to the currently running daemon binary (os.Executable()).
// webDir is the path to the static web assets directory.
// dataDir is the daemon's data directory for staging downloads.
func NewManager(binaryPath, webDir, dataDir string) *Manager {
	return &Manager{
		binaryPath: binaryPath,
		webDir:     webDir,
		dataDir:    dataDir,
		client: &http.Client{
			Timeout: HTTPTimeout,
		},
	}
}

// currentPlatform returns the platform identifier matching the release manifest (e.g. "linux-amd64").
func currentPlatform() string {
	return fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)
}

// StartBackgroundChecker runs a goroutine that periodically checks for updates.
func (m *Manager) StartBackgroundChecker(ctx context.Context) {
	go func() {
		// Check shortly after startup.
		m.CheckNow(ctx)
		ticker := time.NewTicker(CheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.CheckNow(ctx)
			}
		}
	}()
}

// CheckNow fetches the latest manifest and caches the result.
func (m *Manager) CheckNow(ctx context.Context) CheckResult {
	m.mu.Lock()
	m.checking = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.checking = false
		m.mu.Unlock()
	}()

	result := CheckResult{
		CurrentVersion: buildinfo.Version,
		CurrentCommit:  buildinfo.Commit,
		CheckedAt:      time.Now().UTC().Format(time.RFC3339),
	}

	manifest, err := m.fetchManifest(ctx)
	if err != nil {
		slog.Warn("update check failed", "error", err)
		result.Error = err.Error()
		m.mu.Lock()
		m.lastError = err.Error()
		m.lastCheck = time.Now()
		m.mu.Unlock()
		return result
	}

	m.mu.Lock()
	m.manifest = manifest
	m.lastCheck = time.Now()
	m.lastError = ""
	m.mu.Unlock()

	result.LatestVersion = manifest.Version
	result.LatestCommit = manifest.Commit
	result.BuiltAt = manifest.BuiltAt
	result.UpdateAvailable = isNewer(buildinfo.Version, manifest.Version)
	result.ReleaseURL = fmt.Sprintf("https://github.com/W1seGit/Cliff/releases/tag/v%s", manifest.Version)

	if asset := findPlatformAsset(manifest, currentPlatform()); asset != nil {
		result.ArchiveName = asset.Archive
		result.ArchiveSize = asset.SizeBytes
	}

	return result
}

// CachedCheck returns the last check result without fetching.
func (m *Manager) CachedCheck() CheckResult {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := CheckResult{
		CurrentVersion: buildinfo.Version,
		CurrentCommit:  buildinfo.Commit,
		CheckedAt:      m.lastCheck.UTC().Format(time.RFC3339),
		Error:          m.lastError,
	}
	if m.manifest != nil {
		result.LatestVersion = m.manifest.Version
		result.LatestCommit = m.manifest.Commit
		result.BuiltAt = m.manifest.BuiltAt
		result.UpdateAvailable = isNewer(buildinfo.Version, m.manifest.Version)
		result.ReleaseURL = fmt.Sprintf("https://github.com/W1seGit/Cliff/releases/tag/v%s", m.manifest.Version)
		if asset := findPlatformAsset(m.manifest, currentPlatform()); asset != nil {
			result.ArchiveName = asset.Archive
			result.ArchiveSize = asset.SizeBytes
		}
	}
	return result
}

// IsChecking returns whether a check is in progress.
func (m *Manager) IsChecking() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.checking
}

// IsApplying returns whether an update application is in progress.
func (m *Manager) IsApplying() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.applying
}

// Apply downloads the latest release, verifies it, and swaps the binary + web assets.
// On success, the daemon will restart itself.
func (m *Manager) Apply(ctx context.Context) (ApplyResult, error) {
	m.mu.Lock()
	if m.applying {
		m.mu.Unlock()
		return ApplyResult{}, errors.New("an update is already being applied")
	}
	m.applying = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.applying = false
		m.mu.Unlock()
	}()

	// Ensure we have a fresh manifest.
	manifest := m.cachedManifest()
	if manifest == nil {
		check := m.CheckNow(ctx)
		if check.Error != "" {
			return ApplyResult{Message: check.Error}, errors.New(check.Error)
		}
		manifest = m.cachedManifest()
	}
	if manifest == nil {
		return ApplyResult{}, errors.New("no release manifest available")
	}

	asset := findPlatformAsset(manifest, currentPlatform())
	if asset == nil {
		return ApplyResult{}, fmt.Errorf("no release archive for platform %s", currentPlatform())
	}

	slog.Info("applying update", "current", buildinfo.Version, "target", manifest.Version, "archive", asset.Archive)

	// Stage the download in dataDir/updates.
	stagingDir := filepath.Join(m.dataDir, "updates")
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return ApplyResult{}, fmt.Errorf("create staging dir: %w", err)
	}
	archivePath := filepath.Join(stagingDir, asset.Archive)

	// Download the archive.
	archiveURL := fmt.Sprintf("https://github.com/W1seGit/Cliff/releases/latest/download/%s", asset.Archive)
	slog.Info("downloading update archive", "url", archiveURL)
	if err := m.downloadFile(ctx, archiveURL, archivePath); err != nil {
		return ApplyResult{}, fmt.Errorf("download archive: %w", err)
	}

	// Verify SHA256.
	if err := verifySHA256(archivePath, asset.SHA256); err != nil {
		os.Remove(archivePath)
		return ApplyResult{}, fmt.Errorf("checksum verification failed: %w", err)
	}
	slog.Info("archive verified", "sha256", asset.SHA256)

	// Extract to a temp directory.
	extractDir := filepath.Join(stagingDir, "extracted")
	os.RemoveAll(extractDir)
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return ApplyResult{}, fmt.Errorf("create extract dir: %w", err)
	}
	if err := extractZip(archivePath, extractDir); err != nil {
		os.RemoveAll(extractDir)
		return ApplyResult{}, fmt.Errorf("extract archive: %w", err)
	}

	// Find the extracted binary and web directory.
	// The archive contains a top-level directory like cliff-{platform}/.
	newBinary, newWebDir, err := findExtractedAssets(extractDir)
	if err != nil {
		os.RemoveAll(extractDir)
		return ApplyResult{}, fmt.Errorf("find extracted assets: %w", err)
	}

	// Swap the binary and web assets.
	if err := m.swapAssets(newBinary, newWebDir); err != nil {
		os.RemoveAll(extractDir)
		return ApplyResult{}, fmt.Errorf("swap assets: %w", err)
	}

	slog.Info("update applied successfully, scheduling restart", "version", manifest.Version)

	// Clean up staging.
	os.RemoveAll(extractDir)
	os.Remove(archivePath)

	return ApplyResult{
		Success:    true,
		Message:    fmt.Sprintf("Updated to version %s. The daemon will restart momentarily.", manifest.Version),
		NewVersion: manifest.Version,
		Restarting: true,
	}, nil
}

func (m *Manager) cachedManifest() *ReleaseManifest {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.manifest
}

func (m *Manager) fetchManifest(ctx context.Context) (*ReleaseManifest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ManifestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", fmt.Sprintf("cliff/%s updater", buildinfo.Version))

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("manifest fetch failed: HTTP %d", resp.StatusCode)
	}

	var manifest ReleaseManifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if manifest.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported manifest schema version %d", manifest.SchemaVersion)
	}
	return &manifest, nil
}

func (m *Manager) downloadFile(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", fmt.Sprintf("cliff/%s updater", buildinfo.Version))

	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func (m *Manager) swapAssets(newBinary, newWebDir string) error {
	// Swap the binary.
	if newBinary != "" && m.binaryPath != "" {
		if runtime.GOOS == "windows" {
			// On Windows, we can't overwrite a running .exe.
			// Move the old binary aside, then move the new one in.
			oldBackup := m.binaryPath + ".old"
			os.Remove(oldBackup)
			if err := os.Rename(m.binaryPath, oldBackup); err != nil {
				return fmt.Errorf("rename old binary: %w", err)
			}
			if err := copyFile(newBinary, m.binaryPath); err != nil {
				os.Rename(oldBackup, m.binaryPath) // attempt rollback
				return fmt.Errorf("copy new binary: %w", err)
			}
		} else {
			// On Unix, we can rename over a running binary.
			if err := os.Rename(newBinary, m.binaryPath); err != nil {
				return fmt.Errorf("rename new binary: %w", err)
			}
			if err := os.Chmod(m.binaryPath, 0o755); err != nil {
				return fmt.Errorf("chmod binary: %w", err)
			}
		}
	}

	// Swap the web directory.
	if newWebDir != "" && m.webDir != "" {
		webBackup := m.webDir + ".old"
		os.RemoveAll(webBackup)
		if err := os.Rename(m.webDir, webBackup); err != nil {
			slog.Warn("could not backup old web dir", "error", err)
		}
		if err := os.Rename(newWebDir, m.webDir); err != nil {
			// Try copy as fallback (cross-device).
			if err := copyDir(newWebDir, m.webDir); err != nil {
				return fmt.Errorf("move new web dir: %w", err)
			}
		}
		os.RemoveAll(webBackup)
	}

	return nil
}

// findPlatformAsset finds the asset matching the current platform.
func findPlatformAsset(manifest *ReleaseManifest, platform string) *PlatformAsset {
	for i := range manifest.Platforms {
		if manifest.Platforms[i].Platform == platform {
			return &manifest.Platforms[i]
		}
	}
	return nil
}

// isNewer compares semantic versions. Returns true if latest > current.
// Handles versions like "0.1.0", "v0.1.0", "dev".
func isNewer(current, latest string) bool {
	current = strings.TrimPrefix(current, "v")
	latest = strings.TrimPrefix(latest, "v")
	if current == "dev" || current == "" {
		return latest != "" && latest != "dev"
	}
	if latest == "" || latest == "dev" {
		return false
	}
	return compareVersions(current, latest) < 0
}

// compareVersions returns -1 if a < b, 0 if a == b, 1 if a > b.
func compareVersions(a, b string) int {
	partsA := strings.Split(a, ".")
	partsB := strings.Split(b, ".")
	maxLen := len(partsA)
	if len(partsB) > maxLen {
		maxLen = len(partsB)
	}
	for i := 0; i < maxLen; i++ {
		var na, nb int
		if i < len(partsA) {
			fmt.Sscanf(partsA[i], "%d", &na)
		}
		if i < len(partsB) {
			fmt.Sscanf(partsB[i], "%d", &nb)
		}
		if na < nb {
			return -1
		}
		if na > nb {
			return 1
		}
	}
	return 0
}

func verifySHA256(path, expectedHex string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return err
	}

	actual := hex.EncodeToString(hasher.Sum(nil))
	expected := strings.TrimSpace(strings.ToLower(expectedHex))
	if actual != expected {
		return fmt.Errorf("expected %s, got %s", expected, actual)
	}
	return nil
}

func extractZip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		target := filepath.Join(dest, f.Name)
		// Prevent zip slip.
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("zip entry outside dest dir: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			out.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
		if runtime.GOOS != "windows" {
			os.Chmod(target, f.Mode())
		}
	}
	return nil
}

// findExtractedAssets locates the binary and web directory inside the extracted archive.
// The archive has a top-level dir like cliff-{platform}/ containing:
//   - cliff (or cliff.exe on Windows)
//   - web/ directory
func findExtractedAssets(root string) (binary, webDir string, err error) {
	binaryName := "cliff"
	if runtime.GOOS == "windows" {
		binaryName = "cliff.exe"
	}

	// Look for the binary in the top-level dir and one level deep.
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", "", err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			// Check if this is the top-level package dir.
			candidate := filepath.Join(root, entry.Name(), binaryName)
			if fileExists(candidate) {
				binary = candidate
				webCandidate := filepath.Join(root, entry.Name(), "web")
				if dirExists(webCandidate) {
					webDir = webCandidate
				}
				return binary, webDir, nil
			}
			// Also check for web dir directly under this subdirectory.
		} else if entry.Name() == binaryName {
			binary = filepath.Join(root, entry.Name())
		}
	}

	// If we found the binary at root level, look for web at root level too.
	if binary != "" {
		webCandidate := filepath.Join(root, "web")
		if dirExists(webCandidate) {
			webDir = webCandidate
		}
		return binary, webDir, nil
	}

	return "", "", fmt.Errorf("binary %s not found in extracted archive", binaryName)
}

func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	destination, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
