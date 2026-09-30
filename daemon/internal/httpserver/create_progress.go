package httpserver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// createProgress follows one create, clone or import so the dashboard can show
// what Cliff is doing while it works. Every method is safe on a nil receiver,
// so the work runs the same when nobody is watching.
type createProgress struct {
	mu      sync.Mutex
	steps   []createStep
	detail  string
	failure string
	done    bool
	updated time.Time
}

type createStep struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// State is "pending", "active", "done" or "failed".
	State string `json:"state"`
}

type createProgressSnapshot struct {
	Steps  []createStep `json:"steps"`
	Detail string       `json:"detail"`
	Error  string       `json:"error,omitempty"`
	Done   bool         `json:"done"`
}

type progressContextKey struct{}

var progressIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

type createProgressStore struct {
	mu    sync.Mutex
	items map[string]*createProgress
}

func newCreateProgressStore() *createProgressStore {
	return &createProgressStore{items: map[string]*createProgress{}}
}

// begin registers a new operation under the caller's id. A missing or
// malformed id means nobody is watching, and it returns nil.
func (s *createProgressStore) begin(id string, steps []createStep) *createProgress {
	if s == nil || !progressIDPattern.MatchString(id) {
		return nil
	}
	progress := &createProgress{steps: steps, updated: time.Now()}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, item := range s.items {
		if time.Since(item.updatedAt()) > 15*time.Minute {
			delete(s.items, key)
		}
	}
	s.items[id] = progress
	return progress
}

func (s *createProgressStore) snapshot(id string) (createProgressSnapshot, bool) {
	if s == nil {
		return createProgressSnapshot{}, false
	}
	s.mu.Lock()
	progress := s.items[id]
	s.mu.Unlock()
	if progress == nil {
		return createProgressSnapshot{}, false
	}
	return progress.snapshot(), true
}

func (p *createProgress) updatedAt() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.updated
}

func (p *createProgress) snapshot() createProgressSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	steps := make([]createStep, len(p.steps))
	copy(steps, p.steps)
	return createProgressSnapshot{Steps: steps, Detail: p.detail, Error: p.failure, Done: p.done}
}

// start marks the named step as running and every step before it as done.
// A step that is not part of this operation is ignored.
func (p *createProgress) start(id string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	index := -1
	for position, step := range p.steps {
		if step.ID == id {
			index = position
			break
		}
	}
	if index < 0 {
		return
	}
	for position := range p.steps {
		switch {
		case position < index:
			p.steps[position].State = "done"
		case position == index:
			p.steps[position].State = "active"
		}
	}
	p.detail = ""
	p.updated = time.Now()
}

// setDetail adds a line under the running step, such as how much has downloaded.
func (p *createProgress) setDetail(text string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.detail = text
	p.updated = time.Now()
	p.mu.Unlock()
}

// finish closes the operation: every step done, or the running one failed.
func (p *createProgress) finish(err error) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done = true
	p.updated = time.Now()
	if err == nil {
		for position := range p.steps {
			p.steps[position].State = "done"
		}
		p.detail = ""
		return
	}
	p.failure = err.Error()
	failed := false
	for position := range p.steps {
		if p.steps[position].State == "active" {
			p.steps[position].State = "failed"
			failed = true
		}
	}
	if !failed && len(p.steps) > 0 {
		p.steps[0].State = "failed"
	}
}

func withProgress(r *http.Request, progress *createProgress) *http.Request {
	if progress == nil {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), progressContextKey{}, progress))
}

func progressFrom(r *http.Request) *createProgress {
	if r == nil {
		return nil
	}
	progress, _ := r.Context().Value(progressContextKey{}).(*createProgress)
	return progress
}

// createSteps lists what an operation will do, in order, in words the user
// recognises. The ids are the ones the code reports as it goes.
func createSteps(input serverCreateInput) []createStep {
	step := func(id string, label string) createStep { return createStep{ID: id, Label: label, State: "pending"} }
	switch strings.TrimSpace(input.Mode) {
	case "clone":
		return []createStep{step("copy", "Copy the server files"), step("save", "Save the new server"), step("properties", "Update server.properties")}
	case "import", "import-staged":
		return []createStep{step("check", "Check the folder"), step("copy", "Copy the files into Cliff"), step("detect", "Detect the server type and version"), step("save", "Save the server")}
	}
	version := strings.TrimSpace(input.MinecraftVersion)
	loader := map[string]string{"forge": "Forge", "neoforge": "NeoForge"}[input.Type]
	steps := []createStep{step("prepare", "Prepare the server folder")}
	switch input.Type {
	case "forge", "neoforge":
		steps = append(steps,
			step("download", fmt.Sprintf("Download the %s installer", loader)),
			step("settings", "Write the default settings"),
			step("java", "Set up Java (first time only)"),
			step("install", fmt.Sprintf("Run the %s installer. This can take a few minutes", loader)),
		)
	case "fabric":
		steps = append(steps,
			step("lookup", "Find the Fabric installer"),
			step("download", "Download the Fabric launcher"),
			step("settings", "Write the default settings"),
		)
	default:
		label := "Find the server download"
		if version != "" {
			label = fmt.Sprintf("Find the download for Minecraft %s", version)
		}
		steps = append(steps, step("lookup", label), step("download", "Download the server"), step("settings", "Write the default settings"))
	}
	return append(steps, step("save", "Save the server"))
}

// progressReader reports how much of a download has arrived.
type progressReader struct {
	reader   io.Reader
	progress *createProgress
	total    int64
	read     int64
	reported time.Time
}

func (p *progressReader) Read(buffer []byte) (int, error) {
	n, err := p.reader.Read(buffer)
	p.read += int64(n)
	if p.progress != nil && time.Since(p.reported) > 250*time.Millisecond {
		p.reported = time.Now()
		if p.total > 0 {
			p.progress.setDetail(fmt.Sprintf("%s of %s", megabytes(p.read), megabytes(p.total)))
		} else {
			p.progress.setDetail(megabytes(p.read))
		}
	}
	return n, err
}

func megabytes(bytes int64) string {
	return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
}

// createProgressHandler lets the dashboard poll an operation it started.
func (h apiHandler) createProgressHandler(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := h.createProgress.snapshot(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "No such operation")
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}
