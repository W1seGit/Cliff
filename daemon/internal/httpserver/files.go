package httpserver

import (
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

const maxTextBytes int64 = 512 * 1024
const maxMultipartFieldBytes int64 = 8 * 1024

type fileEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Type      string `json:"type"`
	Size      int64  `json:"size"`
	UpdatedAt string `json:"updatedAt"`
	Editable  bool   `json:"editable"`
}

type filePayload struct {
	File fileContent `json:"file"`
}

type fileContent struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Editable bool   `json:"editable"`
	Content  string `json:"content"`
}

type fileListing struct {
	CWD     string      `json:"cwd"`
	Parent  string      `json:"parent"`
	Entries []fileEntry `json:"entries"`
}

func (h apiHandler) files(w http.ResponseWriter, r *http.Request) {
	server, ok, err := h.store.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	target, err := resolveInside(server.Path, r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	info, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "File or folder not found")
		} else {
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	if info.IsDir() {
		listing, err := readDirectoryListing(server.Path, target)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, listing)
		return
	}
	if !info.Mode().IsRegular() {
		writeError(w, http.StatusBadRequest, "Unsupported file type")
		return
	}
	if r.URL.Query().Get("raw") == "1" {
		http.ServeFile(w, r, target)
		return
	}
	editable := info.Size() <= maxTextBytes && isTextLike(target)
	content := ""
	if editable {
		bytes, err := os.ReadFile(target)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		content = string(bytes)
	}
	writeJSON(w, http.StatusOK, filePayload{File: fileContent{Name: filepath.Base(target), Path: toRelative(server.Path, target), Size: info.Size(), Editable: editable, Content: content}})
}

func (h apiHandler) fileAction(w http.ResponseWriter, r *http.Request) {
	server, ok, err := h.store.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "multipart/form-data") {
		h.uploadFile(w, r, server)
		return
	}
	var input struct {
		Action  string   `json:"action"`
		Path    string   `json:"path"`
		Content string   `json:"content"`
		Paths   []string `json:"paths"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid file action body")
		return
	}
	target, err := resolveInside(server.Path, input.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch input.Action {
	case "mkdir":
		if err := os.MkdirAll(target, 0o755); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case "create-file":
		if err := createManagedFile(target); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "fileName": filepath.Base(target), "path": toRelative(server.Path, target)})
	case "write":
		if err := writeManagedFile(target, input.Content); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case "delete":
		if err := deleteManagedPath(server.Path, target); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case "delete-selected":
		deleted, err := deleteSelectedManagedPaths(server.Path, input.Paths)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": deleted})
	default:
		writeError(w, http.StatusBadRequest, "Unsupported file action")
	}
}

func (h apiHandler) uploadFile(w http.ResponseWriter, r *http.Request, server store.Server) {
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "Upload form could not be read")
		return
	}

	action := ""
	uploadPath := ""
	pathSeen := false
	uploadedName := ""
	uploadedRelativePath := ""
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "Upload form could not be read")
			return
		}
		switch part.FormName() {
		case "action":
			value, err := readMultipartTextPart(part)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			action = value
			if action != "upload" {
				writeError(w, http.StatusBadRequest, "Unsupported file upload action")
				return
			}
		case "path":
			value, err := readMultipartTextPart(part)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			uploadPath = value
			pathSeen = true
		case "file":
			if action != "upload" {
				writeError(w, http.StatusBadRequest, "Upload action must be sent before the file")
				return
			}
			if !pathSeen {
				writeError(w, http.StatusBadRequest, "Upload path must be sent before the file")
				return
			}
			directory, err := resolveInside(server.Path, uploadPath)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			info, err := os.Stat(directory)
			if err != nil || !info.IsDir() {
				writeError(w, http.StatusBadRequest, "Uploads must target a folder")
				return
			}
			safeName := filepath.Base(part.FileName())
			if safeName == "" || safeName == "." || safeName == ".." {
				writeError(w, http.StatusBadRequest, "Upload file name is invalid")
				return
			}
			target, err := resolveInside(server.Path, filepath.Join(toRelative(server.Path, directory), safeName))
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if err := writeUploadedFile(part, target); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			uploadedName = safeName
			uploadedRelativePath = toRelative(server.Path, target)
		}
	}
	if action != "upload" {
		writeError(w, http.StatusBadRequest, "Unsupported file upload action")
		return
	}
	if uploadedName == "" {
		writeError(w, http.StatusBadRequest, "Upload file is required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "fileName": uploadedName, "path": uploadedRelativePath})
}

func readDirectoryListing(root string, target string) (fileListing, error) {
	entries, err := os.ReadDir(target)
	if err != nil {
		return fileListing{}, err
	}
	files := []fileEntry{}
	for _, entry := range entries {
		fullPath := filepath.Join(target, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		entryType := "file"
		if info.IsDir() {
			entryType = "directory"
		}
		files = append(files, fileEntry{
			Name:      entry.Name(),
			Path:      toRelative(root, fullPath),
			Type:      entryType,
			Size:      info.Size(),
			UpdatedAt: info.ModTime().UTC().Format(time.RFC3339),
			Editable:  info.Mode().IsRegular() && info.Size() <= maxTextBytes && isTextLike(entry.Name()),
		})
	}
	sort.Slice(files, func(left int, right int) bool {
		if files[left].Type == files[right].Type {
			return files[left].Name < files[right].Name
		}
		return files[left].Type == "directory"
	})
	parent := ""
	cwd := toRelative(root, target)
	if filepath.Clean(target) != filepath.Clean(root) {
		parent = toRelative(root, filepath.Dir(target))
	}
	return fileListing{CWD: cwd, Parent: parent, Entries: files}, nil
}

func createManagedFile(target string) error {
	fileName := filepath.Base(target)
	if fileName == "" || fileName == "." || fileName == ".." {
		return errors.New("File name is invalid")
	}
	if !isTextLike(target) {
		return errors.New("Only text-like files can be created from the file manager")
	}
	if _, err := os.Stat(target); err == nil {
		return errors.New("File or folder already exists")
	}
	parentInfo, err := os.Stat(filepath.Dir(target))
	if err != nil || !parentInfo.IsDir() {
		return errors.New("New files must be created inside an existing folder")
	}
	return os.WriteFile(target, []byte(""), 0o644)
}

func writeManagedFile(target string, content string) error {
	if info, err := os.Stat(target); err == nil && !info.IsDir() && !isTextLike(target) {
		return errors.New("Only text-like files can be edited")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, []byte(content), 0o644)
}

func deleteManagedPath(root string, target string) error {
	if filepath.Clean(target) == filepath.Clean(root) {
		return errors.New("The server root cannot be deleted from the file manager")
	}
	if _, err := os.Stat(target); err != nil {
		return errors.New("File or folder not found")
	}
	return os.RemoveAll(target)
}

func deleteSelectedManagedPaths(root string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, errors.New("Select at least one file or folder to delete")
	}
	targets := []string{}
	for _, item := range paths {
		target, err := resolveInside(root, item)
		if err != nil {
			return nil, err
		}
		if filepath.Clean(target) == filepath.Clean(root) {
			return nil, errors.New("The server root cannot be deleted from the file manager")
		}
		if _, err := os.Stat(target); err != nil {
			return nil, errors.New("File or folder not found: " + toRelative(root, target))
		}
		targets = append(targets, target)
	}
	deleted := []string{}
	for _, target := range targets {
		if err := os.RemoveAll(target); err != nil {
			return deleted, err
		}
		deleted = append(deleted, toRelative(root, target))
	}
	return deleted, nil
}

func readMultipartTextPart(part *multipart.Part) (string, error) {
	data, err := io.ReadAll(io.LimitReader(part, maxMultipartFieldBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxMultipartFieldBytes {
		return "", errors.New("Upload field is too large")
	}
	return string(data), nil
}

func writeUploadedFile(file io.Reader, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer output.Close()
	_, err = io.Copy(output, file)
	return err
}

func resolveInside(root string, relativePath string) (string, error) {
	resolved, err := filepath.Abs(filepath.Join(root, relativePath))
	if err != nil {
		return "", err
	}
	normalizedRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if resolved != normalizedRoot && !strings.HasPrefix(resolved, normalizedRoot+string(os.PathSeparator)) {
		return "", errors.New("Path escapes server folder")
	}
	return resolved, nil
}

func toRelative(root string, fullPath string) string {
	relative, err := filepath.Rel(root, fullPath)
	if err != nil || relative == "." {
		return ""
	}
	return filepath.ToSlash(relative)
}

func isTextLike(filePath string) bool {
	lower := strings.ToLower(filePath)
	for _, extension := range []string{".txt", ".properties", ".json", ".json5", ".toml", ".yml", ".yaml", ".cfg", ".conf", ".log", ".md", ".mcmeta", ".datapack", ".disabled"} {
		if strings.HasSuffix(lower, extension) {
			return true
		}
	}
	return false
}
