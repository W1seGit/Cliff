package httpserver

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
)

func extractServerZip(file multipart.File, header *multipart.FileHeader, targetRoot string) error {
	if header == nil || !strings.HasSuffix(strings.ToLower(header.Filename), ".zip") {
		return errors.New("Upload a .zip server archive")
	}
	archive, err := zipReaderFromMultipart(file, "Server ZIP could not be read")
	if err != nil {
		return err
	}
	stripPrefix, err := archiveStripPrefix(archive.File)
	if err != nil {
		return err
	}
	filesWritten := 0
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		return err
	}
	for _, entry := range archive.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		parts, err := normalizedArchiveParts(entry.Name)
		if err != nil {
			return err
		}
		if len(parts) == 0 || parts[0] == "__MACOSX" {
			continue
		}
		if stripPrefix != "" && parts[0] == stripPrefix {
			parts = parts[1:]
		}
		if len(parts) == 0 {
			continue
		}
		target := filepath.Join(append([]string{targetRoot}, parts...)...)
		if err := assertInsidePath(targetRoot, target); err != nil {
			return err
		}
		reader, err := entry.Open()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			_ = reader.Close()
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, entry.Mode())
		if err != nil {
			_ = reader.Close()
			return err
		}
		_, copyErr := io.Copy(output, reader)
		closeErr := output.Close()
		readCloseErr := reader.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if readCloseErr != nil {
			return readCloseErr
		}
		filesWritten++
	}
	if filesWritten == 0 {
		return errors.New("Server ZIP does not contain server files")
	}
	return nil
}

func extractStagedServerZip(path string, filename string, targetRoot string) error {
	if !strings.HasSuffix(strings.ToLower(filename), ".zip") {
		return errors.New("Upload a .zip server archive")
	}
	file, err := os.Open(path)
	if err != nil {
		return errors.New("Server ZIP could not be read")
	}
	defer file.Close()
	archive, err := zipReaderFromMultipart(file, "Server ZIP could not be read")
	if err != nil {
		return err
	}
	return extractServerZipArchive(archive, targetRoot)
}

func extractServerZipArchive(archive *zip.Reader, targetRoot string) error {
	stripPrefix, err := archiveStripPrefix(archive.File)
	if err != nil {
		return err
	}
	filesWritten := 0
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		return err
	}
	for _, entry := range archive.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		parts, err := normalizedArchiveParts(entry.Name)
		if err != nil {
			return err
		}
		if len(parts) == 0 || parts[0] == "__MACOSX" {
			continue
		}
		if stripPrefix != "" && parts[0] == stripPrefix {
			parts = parts[1:]
		}
		if len(parts) == 0 {
			continue
		}
		target := filepath.Join(append([]string{targetRoot}, parts...)...)
		if err := assertInsidePath(targetRoot, target); err != nil {
			return err
		}
		reader, err := entry.Open()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			_ = reader.Close()
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, entry.Mode())
		if err != nil {
			_ = reader.Close()
			return err
		}
		_, copyErr := io.Copy(output, reader)
		closeErr := output.Close()
		readCloseErr := reader.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if readCloseErr != nil {
			return readCloseErr
		}
		filesWritten++
	}
	if filesWritten == 0 {
		return errors.New("Server ZIP does not contain server files")
	}
	return nil
}

func zipReaderFromMultipart(file multipart.File, message string) (*zip.Reader, error) {
	size, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, errors.New(message)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, errors.New(message)
	}
	archive, err := zip.NewReader(file, size)
	if err != nil {
		return nil, errors.New(message)
	}
	return archive, nil
}

func archiveStripPrefix(files []*zip.File) (string, error) {
	roots := map[string]bool{}
	hasRootFile := false
	fileCount := 0
	for _, entry := range files {
		if entry.FileInfo().IsDir() {
			continue
		}
		parts, err := normalizedArchiveParts(entry.Name)
		if err != nil {
			return "", err
		}
		if len(parts) == 0 || parts[0] == "__MACOSX" {
			continue
		}
		fileCount++
		if len(parts) == 1 {
			hasRootFile = true
		}
		roots[parts[0]] = true
	}
	if fileCount == 0 {
		return "", errors.New("Server ZIP is empty")
	}
	if !hasRootFile && len(roots) == 1 {
		for root := range roots {
			return root, nil
		}
	}
	return "", nil
}

func writeUploadedFolder(files []*multipart.FileHeader, relativePaths []string, targetRoot string) error {
	if len(files) == 0 {
		return errors.New("Choose a server folder to import")
	}
	entries := make([]struct {
		header *multipart.FileHeader
		parts  []string
	}, 0, len(files))
	roots := map[string]bool{}
	for index, header := range files {
		relativePath := header.Filename
		if index < len(relativePaths) && relativePaths[index] != "" {
			relativePath = relativePaths[index]
		}
		parts, err := relativeUploadParts(relativePath)
		if err != nil {
			return err
		}
		entries = append(entries, struct {
			header *multipart.FileHeader
			parts  []string
		}{header: header, parts: parts})
		roots[parts[0]] = true
	}
	stripPrefix := ""
	if len(roots) == 1 {
		for root := range roots {
			stripPrefix = root
		}
	}
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		parts := entry.parts
		if stripPrefix != "" && parts[0] == stripPrefix {
			parts = parts[1:]
		}
		if len(parts) == 0 {
			continue
		}
		target := filepath.Join(append([]string{targetRoot}, parts...)...)
		if err := assertInsidePath(targetRoot, target); err != nil {
			return err
		}
		input, err := entry.header.Open()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			_ = input.Close()
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		inputCloseErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if inputCloseErr != nil {
			return inputCloseErr
		}
	}
	return nil
}

func writeStagedFolder(files []stagedImportFile, relativePaths []string, targetRoot string) error {
	if len(files) == 0 {
		return errors.New("Choose a server folder to import")
	}
	entries := make([]struct {
		file  stagedImportFile
		parts []string
	}, 0, len(files))
	roots := map[string]bool{}
	for index, file := range files {
		relativePath := file.Name
		if index < len(relativePaths) && relativePaths[index] != "" {
			relativePath = relativePaths[index]
		}
		parts, err := relativeUploadParts(relativePath)
		if err != nil {
			return err
		}
		entries = append(entries, struct {
			file  stagedImportFile
			parts []string
		}{file: file, parts: parts})
		roots[parts[0]] = true
	}
	stripPrefix := ""
	if len(roots) == 1 {
		for root := range roots {
			stripPrefix = root
		}
	}
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		parts := entry.parts
		if stripPrefix != "" && parts[0] == stripPrefix {
			parts = parts[1:]
		}
		if len(parts) == 0 {
			continue
		}
		target := filepath.Join(append([]string{targetRoot}, parts...)...)
		if err := assertInsidePath(targetRoot, target); err != nil {
			return err
		}
		input, err := os.Open(entry.file.Path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			_ = input.Close()
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		inputCloseErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if inputCloseErr != nil {
			return inputCloseErr
		}
	}
	return nil
}

func normalizedArchiveParts(value string) ([]string, error) {
	return safeRelativeParts(value, "Archive")
}

func relativeUploadParts(value string) ([]string, error) {
	return safeRelativeParts(value, "Uploaded folder")
}

func safeRelativeParts(value string, label string) ([]string, error) {
	normalized := strings.TrimLeft(strings.ReplaceAll(value, "\\", "/"), "/")
	if normalized == "" || strings.Contains(normalized, "\x00") {
		return nil, errors.New(label + " contains an invalid path")
	}
	parts := []string{}
	for _, part := range strings.Split(normalized, "/") {
		if part == "" {
			continue
		}
		if part == "." || part == ".." {
			return nil, errors.New(label + " contains an unsafe path")
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return nil, errors.New(label + " contains an invalid path")
	}
	return parts, nil
}

func assertInsidePath(rootPath string, targetPath string) error {
	root, err := filepath.Abs(rootPath)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(targetPath)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if relative == "." || (!strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && relative != ".." && !filepath.IsAbs(relative)) {
		return nil
	}
	return errors.New("Archive contains an unsafe path")
}

func formPaths(form *multipart.Form) ([]string, error) {
	if form == nil {
		return []string{}, nil
	}
	values := form.Value["paths"]
	if len(values) == 0 || strings.TrimSpace(values[0]) == "" {
		return []string{}, nil
	}
	paths := []string{}
	if err := json.Unmarshal([]byte(values[0]), &paths); err != nil {
		return nil, errors.New("Uploaded folder paths could not be read")
	}
	return paths, nil
}
