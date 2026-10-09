package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func datapackMetadataPath(server store.Server) string {
	return filepath.Join(server.Path, ".dashboard-datapacks.json")
}

func readDatapackMetadata(server store.Server) map[string]*modMetadata {
	metadata := map[string]*modMetadata{}
	data, err := os.ReadFile(datapackMetadataPath(server))
	if err != nil {
		return metadata
	}
	_ = json.Unmarshal(data, &metadata)
	if metadata == nil {
		metadata = map[string]*modMetadata{}
	}
	return metadata
}

func writeDatapackMetadata(server store.Server, metadata map[string]*modMetadata) error {
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(datapackMetadataPath(server), append(data, '\n'), 0o644)
}

func saveDatapackMetadata(server store.Server, worldName string, fileName string, metadata modMetadata) error {
	index := readDatapackMetadata(server)
	index[worldName+"/"+filepath.Base(fileName)] = &metadata
	return writeDatapackMetadata(server, index)
}

func listDatapacks(path string, server store.Server, worldName string) []datapackInfo {
	entries, err := os.ReadDir(path)
	if err != nil {
		return []datapackInfo{}
	}
	metadataIndex := readDatapackMetadata(server)
	datapacks := []datapackInfo{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		lower := strings.ToLower(entry.Name())
		if !strings.HasSuffix(lower, ".zip") && !strings.HasSuffix(lower, ".zip.disabled") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		datapacks = append(datapacks, datapackInfo{
			Name:      entry.Name(),
			Size:      info.Size(),
			UpdatedAt: info.ModTime().UTC().Format(time.RFC3339),
			Enabled:   !strings.HasSuffix(lower, ".disabled"),
			Metadata:  metadataIndex[worldName+"/"+entry.Name()],
		})
	}
	sort.Slice(datapacks, func(left int, right int) bool {
		return datapacks[left].Name < datapacks[right].Name
	})
	return datapacks
}

func writeDatapack(server store.Server, worldName string, fileName string, file io.Reader) error {
	safeWorld, err := safePathSegment(worldName, "World name")
	if err != nil {
		return err
	}
	safeName, err := safePathSegment(fileName, "Datapack name")
	if err != nil {
		return err
	}
	if !strings.HasSuffix(strings.ToLower(safeName), ".zip") {
		return errors.New("Datapacks must be .zip files")
	}
	targetDir, err := resolveInside(server.Path, filepath.Join(safeWorld, "datapacks"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}
	target, err := resolveInside(targetDir, safeName)
	if err != nil {
		return err
	}
	return writeUploadedFile(file, target)
}

func uniqueDatapackFileName(server store.Server, worldName string, fileName string) (string, error) {
	safeWorld, err := safePathSegment(worldName, "World name")
	if err != nil {
		return "", err
	}
	safeName := safeBaseName(fileName)
	if !strings.HasSuffix(strings.ToLower(safeName), ".zip") {
		return "", errors.New("Only .zip datapack files are supported")
	}
	extension := filepath.Ext(safeName)
	base := strings.TrimSuffix(safeName, extension)
	candidate := safeName
	index := 2
	for {
		target, err := resolveInside(server.Path, filepath.Join(safeWorld, "datapacks", candidate))
		if err != nil {
			return "", err
		}
		if !fileExists(target) {
			return candidate, nil
		}
		candidate = base + "-" + strconv.Itoa(index) + extension
		index++
	}
}

func datapackDownloadTarget(server store.Server, worldName string, fileName string) (string, string, error) {
	target, safeName, err := datapackPath(server, worldName, fileName)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() {
		return "", "", errors.New("Datapack not found")
	}
	return target, removeDisabledSuffix(safeName), nil
}

func deleteDatapack(server store.Server, worldName string, fileName string) error {
	target, _, err := datapackPath(server, worldName, fileName)
	if err != nil {
		return err
	}
	if _, err := os.Stat(target); err != nil {
		return errors.New("Datapack not found")
	}
	return os.Remove(target)
}

func deleteSelectedDatapacks(server store.Server, worldName string, fileNames []string) error {
	for _, fileName := range fileNames {
		target, _, err := datapackPath(server, worldName, fileName)
		if err != nil {
			return err
		}
		if fileExists(target) {
			if err := os.Remove(target); err != nil {
				return err
			}
		}
	}
	return nil
}

func setDatapackEnabled(server store.Server, worldName string, fileName string, enabled bool) error {
	source, safeName, err := datapackPath(server, worldName, fileName)
	if err != nil {
		return err
	}
	if !fileExists(source) {
		return errors.New("Datapack not found")
	}
	lowerName := strings.ToLower(safeName)
	nextName := safeName
	if enabled {
		nextName = removeDisabledSuffix(safeName)
	} else if !strings.HasSuffix(lowerName, ".disabled") {
		nextName = safeName + ".disabled"
	}
	if nextName == safeName {
		return nil
	}
	datapackDir := filepath.Dir(source)
	destination, err := resolveInside(datapackDir, nextName)
	if err != nil {
		return err
	}
	if fileExists(destination) {
		return errors.New("A datapack with the target enabled state already exists")
	}
	return os.Rename(source, destination)
}

func setSelectedDatapacksEnabled(server store.Server, worldName string, fileNames []string, enabled bool) error {
	for _, fileName := range fileNames {
		target, safeName, err := datapackPath(server, worldName, fileName)
		if err != nil {
			return err
		}
		lowerName := strings.ToLower(safeName)
		if enabled && !strings.HasSuffix(lowerName, ".disabled") {
			continue
		}
		if !enabled && strings.HasSuffix(lowerName, ".disabled") {
			continue
		}
		if !fileExists(target) {
			continue
		}
		if err := setDatapackEnabled(server, worldName, safeName, enabled); err != nil {
			return err
		}
	}
	return nil
}

func datapackPath(server store.Server, worldName string, fileName string) (string, string, error) {
	safeWorld, err := safePathSegment(worldName, "World name")
	if err != nil {
		return "", "", err
	}
	safeName, err := safePathSegment(fileName, "Datapack name")
	if err != nil {
		return "", "", err
	}
	lowerName := strings.ToLower(safeName)
	if !strings.HasSuffix(lowerName, ".zip") && !strings.HasSuffix(lowerName, ".zip.disabled") {
		return "", "", errors.New("Only datapack zip files are supported")
	}
	datapackDir, err := resolveInside(server.Path, filepath.Join(safeWorld, "datapacks"))
	if err != nil {
		return "", "", err
	}
	target, err := resolveInside(datapackDir, safeName)
	return target, safeName, err
}
