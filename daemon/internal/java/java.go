package java

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Resolver struct {
	DataDir string
	Client  *http.Client
}

type RuntimeInfo struct {
	Major     int      `json:"major"`
	Installed bool     `json:"installed"`
	Path      string   `json:"path"`
	Required  bool     `json:"required"`
	Label     string   `json:"label"`
	UsedBy    []string `json:"usedBy"`
}

func (r Resolver) List(requiredMajors ...int) []RuntimeInfo {
	required := map[int]bool{}
	for _, major := range requiredMajors {
		if major > 0 {
			required[major] = true
		}
	}
	seen := map[int]bool{}
	majors := []int{8, 16, 17, 21, 25}
	for _, major := range requiredMajors {
		if major > 0 {
			majors = append(majors, major)
		}
	}
	runtimes := []RuntimeInfo{}
	for _, major := range majors {
		if seen[major] {
			continue
		}
		seen[major] = true
		root := filepath.Join(r.DataDir, "java", fmt.Sprintf("temurin-%d", major))
		javaPath := javaExecutable(root)
		runtimes = append(runtimes, RuntimeInfo{
			Major:     major,
			Installed: fileExists(javaPath),
			Path:      javaPath,
			Required:  required[major],
			Label:     fmt.Sprintf("Temurin %d", major),
			UsedBy:    []string{},
		})
	}
	return runtimes
}

func (r Resolver) Resolve(ctx context.Context, configured string, minecraftVersion string) (string, error) {
	value := strings.TrimSpace(configured)
	if value != "" && value != "auto" && !strings.HasPrefix(value, "managed:") {
		return value, nil
	}
	major := RequiredMajor(minecraftVersion)
	if strings.HasPrefix(value, "managed:") {
		parsed, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(value, "managed:")))
		if err != nil || parsed <= 0 {
			return "", fmt.Errorf("invalid managed Java version %q", value)
		}
		major = parsed
	}
	return r.Ensure(ctx, major)
}

func (r Resolver) Ensure(ctx context.Context, major int) (string, error) {
	if major <= 0 {
		return "", errors.New("Java major version is required")
	}
	root := filepath.Join(r.DataDir, "java", fmt.Sprintf("temurin-%d", major))
	javaPath := javaExecutable(root)
	if fileExists(javaPath) {
		return javaPath, nil
	}
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		return "", err
	}
	tempDir, err := os.MkdirTemp(filepath.Dir(root), fmt.Sprintf("temurin-%d-*", major))
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tempDir)

	archivePath := filepath.Join(tempDir, "jdk"+archiveExt())
	if err := r.download(ctx, major, archivePath); err != nil {
		return "", err
	}
	extractDir := filepath.Join(tempDir, "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return "", err
	}
	if err := extractArchive(archivePath, extractDir); err != nil {
		return "", err
	}
	jdkRoot, err := findJDKRoot(extractDir)
	if err != nil {
		return "", err
	}
	_ = os.RemoveAll(root)
	if err := os.Rename(jdkRoot, root); err != nil {
		if copyErr := copyDir(jdkRoot, root); copyErr != nil {
			return "", err
		}
	}
	if !fileExists(javaPath) {
		return "", fmt.Errorf("downloaded Java %d did not contain %s", major, javaPath)
	}
	return javaPath, nil
}

func (r Resolver) Uninstall(major int) error {
	if major <= 0 {
		return errors.New("Java major version is required")
	}
	root := filepath.Join(r.DataDir, "java", fmt.Sprintf("temurin-%d", major))
	javaPath := javaExecutable(root)
	if !fileExists(javaPath) {
		return fmt.Errorf("Java %d is not installed", major)
	}
	return os.RemoveAll(root)
}

func (r Resolver) RuntimePath(major int) string {
	return filepath.Join(r.DataDir, "java", fmt.Sprintf("temurin-%d", major))
}

func RequiredMajor(version string) int {
	minor := minecraftMinor(version)
	switch {
	case minor >= 26 || strings.HasPrefix(version, "26."):
		return 25
	case minor >= 21:
		return 21
	case minor >= 18:
		return 17
	case minor >= 17:
		return 16
	default:
		return 8
	}
}

func (r Resolver) download(ctx context.Context, major int, target string) error {
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Minute}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, adoptiumURL(major), nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "cliff/0.1 managed-java")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Temurin Java %d download failed: HTTP %d", major, response.StatusCode)
	}
	file, err := os.Create(target)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(file, response.Body)
	return err
}

func adoptiumURL(major int) string {
	return fmt.Sprintf("https://api.adoptium.net/v3/binary/latest/%d/ga/%s/%s/jdk/hotspot/normal/eclipse?project=jdk", major, adoptiumOS(), adoptiumArch())
}

func adoptiumOS() string {
	switch runtime.GOOS {
	case "darwin":
		return "mac"
	case "windows":
		return "windows"
	default:
		return "linux"
	}
}

func adoptiumArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "arm64":
		return "aarch64"
	default:
		return runtime.GOARCH
	}
}

func archiveExt() string {
	if runtime.GOOS == "windows" {
		return ".zip"
	}
	return ".tar.gz"
}

func javaExecutable(root string) string {
	name := "java"
	if runtime.GOOS == "windows" {
		name = "java.exe"
	}
	return filepath.Join(root, "bin", name)
}

func extractArchive(archivePath string, target string) error {
	if strings.HasSuffix(archivePath, ".zip") {
		return extractZip(archivePath, target)
	}
	return extractTarGz(archivePath, target)
}

func extractZip(archivePath string, target string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, item := range reader.File {
		destination, err := safeJoin(target, item.Name)
		if err != nil {
			return err
		}
		if item.FileInfo().IsDir() {
			if err := os.MkdirAll(destination, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		input, err := item.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, item.FileInfo().Mode())
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		_ = input.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func extractTarGz(archivePath string, target string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		destination, err := safeJoin(target, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destination, os.FileMode(header.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
				return err
			}
			output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(output, tarReader)
			closeErr := output.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
}

func findJDKRoot(root string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || found != "" {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Base(path) == filepath.Base(javaExecutable("")) && strings.EqualFold(filepath.Base(filepath.Dir(path)), "bin") {
			found = filepath.Dir(filepath.Dir(path))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", errors.New("downloaded Java archive did not contain bin/java")
	}
	return found, nil
}

func safeJoin(root string, item string) (string, error) {
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	cleanTarget, err := filepath.Abs(filepath.Join(cleanRoot, filepath.Clean(item)))
	if err != nil {
		return "", err
	}
	if cleanTarget != cleanRoot && !strings.HasPrefix(cleanTarget, cleanRoot+string(os.PathSeparator)) {
		return "", errors.New("archive entry escapes destination")
	}
	return cleanTarget, nil
}

func minecraftMinor(version string) int {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return 0
	}
	minor, _ := strconv.Atoi(parts[1])
	return minor
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func copyDir(source string, target string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer output.Close()
		_, err = io.Copy(output, input)
		return err
	})
}
