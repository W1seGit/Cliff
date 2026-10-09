package httpserver

import (
	"errors"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func worldArchiveTarget(server store.Server, worldName string) (string, string, string, error) {
	safeWorld, err := safePathSegment(worldName, "World name")
	if err != nil {
		return "", "", "", err
	}
	worldPath, err := resolveInside(server.Path, safeWorld)
	if err != nil {
		return "", "", "", err
	}
	if info, err := os.Stat(worldPath); err != nil || !info.IsDir() {
		return "", "", "", errors.New("World folder not found")
	}
	if !fileExists(filepath.Join(worldPath, "level.dat")) {
		return "", "", "", errors.New("Only Minecraft world folders can be downloaded here")
	}
	return worldPath, safeArchiveName(server.Name) + "-" + safeArchiveName(safeWorld) + ".zip", safeWorld, nil
}

func importWorldZipFromPart(server store.Server, part *multipart.Part, worldName string) error {
	tempFile, err := os.CreateTemp("", "cliff-world-upload-*.zip")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	if _, err := io.Copy(tempFile, part); err != nil {
		_ = tempFile.Close()
		return err
	}
	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
		_ = tempFile.Close()
		return errors.New("World archive is not a valid zip file")
	}
	defer tempFile.Close()
	return importWorldZip(server, tempFile, part.FileName(), worldName)
}

func importWorldZip(server store.Server, file multipart.File, archiveName string, worldName string) error {
	safeArchive, err := safePathSegment(archiveName, "Archive name")
	if err != nil {
		return err
	}
	if !strings.HasSuffix(strings.ToLower(safeArchive), ".zip") {
		return errors.New("World imports must be .zip files")
	}
	reader, err := zipReaderFromMultipart(file, "World archive is not a valid zip file")
	if err != nil {
		return err
	}
	targetName := strings.TrimSpace(worldName)
	if targetName == "" {
		targetName = strings.TrimSuffix(safeArchive, filepath.Ext(safeArchive))
	}
	safeWorld, target, err := ensureAvailableWorldTarget(server, targetName)
	if err != nil {
		return err
	}

	files := []string{}
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		normalized, err := normalizedZipName(entry.Name)
		if err != nil {
			return err
		}
		files = append(files, normalized)
	}
	if len(files) == 0 {
		return errors.New("World archive is empty")
	}
	prefix := ""
	if !containsString(files, "level.dat") {
		for _, name := range files {
			if strings.Count(name, "/") == 1 && strings.HasSuffix(name, "/level.dat") {
				prefix = strings.TrimSuffix(name, "level.dat")
				break
			}
		}
		if prefix == "" {
			return errors.New("World archive must contain level.dat at the root or inside one top-level folder")
		}
	}

	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	for _, entry := range reader.File {
		normalized, err := normalizedZipName(entry.Name)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(normalized, prefix) {
			continue
		}
		relative := strings.TrimPrefix(normalized, prefix)
		if relative == "" {
			continue
		}
		destination, err := resolveInside(target, filepath.FromSlash(relative))
		if err != nil {
			return err
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(destination, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		source, err := entry.Open()
		if err != nil {
			return err
		}
		if err := writeZipEntry(source, destination); err != nil {
			_ = source.Close()
			return err
		}
		_ = source.Close()
	}
	_ = safeWorld
	return nil
}

func safePathSegment(value string, label string) (string, error) {
	segment := strings.TrimSpace(value)
	if segment == "" || segment == "." || segment == ".." || filepath.Base(segment) != segment {
		return "", errors.New(label + " is invalid")
	}
	return segment, nil
}

func normalizedZipName(entryName string) (string, error) {
	normalized := strings.TrimLeft(strings.ReplaceAll(entryName, "\\", "/"), "/")
	if normalized == "" || strings.Contains(normalized, "\x00") || strings.HasPrefix(normalized, "/") {
		return "", errors.New("World archive contains an invalid path")
	}
	for _, part := range strings.Split(normalized, "/") {
		if part == ".." {
			return "", errors.New("World archive contains an unsafe path")
		}
	}
	return normalized, nil
}

func writeZipEntry(source io.Reader, target string) error {
	output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer output.Close()
	_, err = io.Copy(output, source)
	return err
}
