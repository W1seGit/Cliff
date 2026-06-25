package httpserver

import (
	"bufio"
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

//go:embed scripts/install-playit-macos.sh
var playitMacOSBuildScript string

//go:embed scripts/install-playit-deps-macos.sh
var playitMacOSDepsScript string

// playitDepStatus represents one build prerequisite checked on macOS.
type playitDepStatus struct {
	Name           string `json:"name"`
	Label          string `json:"label"`
	Installed      bool   `json:"installed"`
	Checking       bool   `json:"checking"`
	InstallPath    string `json:"installPath"`
	InstallCommand string `json:"installCommand"`
}

// playitJobState tracks an async shell subprocess (dep install or cargo build)
// with a live log ring buffer and step markers the dashboard can poll.
type playitJobState struct {
	Running bool     `json:"running"`
	Done    bool     `json:"done"`
	Step    string   `json:"step"`
	Logs    []string `json:"logs"`
	Error   string   `json:"error"`
}

// playitBuildManager owns the macOS build-from-source lifecycle for the
// playit agent. It mirrors playitAgentManager: a mutex-guarded subprocess
// whose stdout/stderr are scanned into a ring buffer.
type playitBuildManager struct {
	mu       sync.Mutex
	deps     []playitDepStatus
	depsJob  *playitSubprocessJob
	buildJob *playitSubprocessJob
}

// playitSubprocessJob is the shared state for a single async shell job.
type playitSubprocessJob struct {
	cmd        *exec.Cmd
	step       string
	logs       []string
	lastError  string
	startedAt  time.Time
	done       bool
	onComplete func(success bool)
}

func newPlayitBuildManager() *playitBuildManager {
	return &playitBuildManager{}
}

// isMacOSPlayitBuildSupported reports whether the build-from-source path
// should be used. Only darwin qualifies — Windows and Linux use prebuilt
// release binaries from the playit-cloud GitHub release.
func isMacOSPlayitBuildSupported() bool {
	return runtime.GOOS == "darwin"
}

// checkPlayitDeps synchronously probes for each build prerequisite and
// returns a per-dep status list. On non-darwin platforms it returns nil.
func (m *playitBuildManager) checkPlayitDeps() []playitDepStatus {
	if !isMacOSPlayitBuildSupported() {
		return nil
	}
	deps := []playitDepStatus{
		{
			Name:           "xcode-clt",
			Label:          "Xcode Command Line Tools",
			Installed:      commandAvailable("xcode-select", "-p"),
			InstallPath:    "/Library/Developer/CommandLineTools",
			InstallCommand: "xcode-select --install",
		},
		{
			Name:           "git",
			Label:          "Git",
			Installed:      commandAvailable("git", "--version"),
			InstallPath:    "/usr/bin/git",
			InstallCommand: "xcode-select --install  #  or  brew install git",
		},
		{
			Name:           "rust",
			Label:          "Rust (cargo + rustc)",
			Installed:      commandAvailable("cargo", "--version") && commandAvailable("rustc", "--version"),
			InstallPath:    filepath.Join(os.Getenv("HOME"), ".cargo"),
			InstallCommand: "curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --profile minimal",
		},
	}
	m.mu.Lock()
	m.deps = deps
	m.mu.Unlock()
	return deps
}

// depsMissing returns the subset of deps that are not installed.
func depsMissing(deps []playitDepStatus) []playitDepStatus {
	missing := make([]playitDepStatus, 0, len(deps))
	for _, dep := range deps {
		if !dep.Installed {
			missing = append(missing, dep)
		}
	}
	return missing
}

// startDepsInstall writes the embedded deps script to disk and runs it
// asynchronously. It returns an error if a job is already running.
func (m *playitBuildManager) startDepsInstall(scriptDir string) error {
	if !isMacOSPlayitBuildSupported() {
		return fmt.Errorf("dependency install is only supported on macOS")
	}
	m.mu.Lock()
	if m.depsJob != nil && m.depsJob.cmd != nil && m.depsJob.cmd.Process != nil {
		m.mu.Unlock()
		return fmt.Errorf("dependency install is already running")
	}
	m.mu.Unlock()

	scriptPath, err := writeEmbeddedScript(scriptDir, "install-playit-deps-macos.sh", playitMacOSDepsScript)
	if err != nil {
		return err
	}
	job := &playitSubprocessJob{startedAt: time.Now().UTC()}
	cmd := exec.Command("/bin/bash", scriptPath)
	cmd.Dir = scriptDir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	job.cmd = cmd

	m.mu.Lock()
	m.depsJob = job
	m.mu.Unlock()

	if err := cmd.Start(); err != nil {
		m.mu.Lock()
		if m.depsJob == job {
			m.depsJob = nil
		}
		m.mu.Unlock()
		return err
	}
	go m.scanJob(stdout, job, m.depsJobPtr)
	go m.scanJob(stderr, job, m.depsJobPtr)
	go m.waitJob(cmd, job, m.depsJobPtr)
	return nil
}

// startBuild writes the embedded build script to disk and runs it
// asynchronously. The destination dir is the playit tools directory where
// the built binary will be installed. onComplete is called when the build
// finishes (success=true) or fails (success=false).
func (m *playitBuildManager) startBuild(scriptDir string, destDir string, onComplete func(success bool)) error {
	if !isMacOSPlayitBuildSupported() {
		return fmt.Errorf("building from source is only supported on macOS")
	}
	m.mu.Lock()
	if m.buildJob != nil && m.buildJob.cmd != nil && m.buildJob.cmd.Process != nil {
		m.mu.Unlock()
		return fmt.Errorf("a build is already running")
	}
	m.mu.Unlock()

	scriptPath, err := writeEmbeddedScript(scriptDir, "install-playit-macos.sh", playitMacOSBuildScript)
	if err != nil {
		return err
	}
	job := &playitSubprocessJob{startedAt: time.Now().UTC(), onComplete: onComplete}
	cmd := exec.Command("/bin/bash", scriptPath, destDir)
	cmd.Dir = scriptDir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	job.cmd = cmd

	m.mu.Lock()
	m.buildJob = job
	m.mu.Unlock()

	if err := cmd.Start(); err != nil {
		m.mu.Lock()
		if m.buildJob == job {
			m.buildJob = nil
		}
		m.mu.Unlock()
		return err
	}
	go m.scanJob(stdout, job, m.buildJobPtr)
	go m.scanJob(stderr, job, m.buildJobPtr)
	go m.waitJob(cmd, job, m.buildJobPtr)
	return nil
}

// depsJobPtr / buildJobPtr return the current job pointer under the lock,
// used by scan/wait goroutines to check ownership.
func (m *playitBuildManager) depsJobPtr() *playitSubprocessJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.depsJob
}

func (m *playitBuildManager) buildJobPtr() *playitSubprocessJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buildJob
}

// mergeDepsState copies dep + job state into a playitStatus for the API.
func (m *playitBuildManager) mergeDepsState(status playitStatus) playitStatus {
	if !isMacOSPlayitBuildSupported() {
		return status
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	status.Platform = runtime.GOOS
	status.Deps = append([]playitDepStatus(nil), m.deps...)
	status.DepsChecked = len(m.deps) > 0
	if m.depsJob != nil {
		status.DepsInstall = m.jobState(m.depsJob)
	}
	if m.buildJob != nil {
		status.Build = m.jobState(m.buildJob)
	}
	return status
}

func (m *playitBuildManager) jobState(job *playitSubprocessJob) *playitJobState {
	state := &playitJobState{
		Running: job.cmd != nil && job.cmd.Process != nil,
		Done:    job.done,
		Step:    job.step,
		Logs:    append([]string(nil), job.logs...),
		Error:   job.lastError,
	}
	return state
}

var playitStepMarkerPattern = regexp.MustCompile(`^\[cliff:step\]\s+(.+)$`)
var playitDepMarkerPattern = regexp.MustCompile(`^\[cliff:dep\]\s+(\S+)\s+(installing|done|skipped.*)$`)
var playitDoneMarkerPattern = regexp.MustCompile(`^\[cliff:done\]\s*$`)
var playitErrorMarkerPattern = regexp.MustCompile(`^\[cliff:error\]\s+(.+)$`)

// scanJob reads subprocess output line-by-line, parses step markers, and
// appends to the job's log ring buffer.
func (m *playitBuildManager) scanJob(reader io.Reader, job *playitSubprocessJob, current func() *playitSubprocessJob) {
	scanner := bufio.NewScanner(reader)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		m.mu.Lock()
		if m.parseMarkersLocked(job, line) {
			m.mu.Unlock()
			continue
		}
		m.appendJobLogLocked(job, line)
		m.mu.Unlock()
	}
}

// parseMarkersLocked updates job state from [cliff:*] markers. Returns true
// if the line was a marker (and should not also be added as a plain log line).
func (m *playitBuildManager) parseMarkersLocked(job *playitSubprocessJob, line string) bool {
	if match := playitStepMarkerPattern.FindStringSubmatch(line); match != nil {
		job.step = match[1]
		m.appendJobLogLocked(job, line)
		return true
	}
	if match := playitDepMarkerPattern.FindStringSubmatch(line); match != nil {
		m.appendJobLogLocked(job, line)
		return true
	}
	if playitDoneMarkerPattern.MatchString(line) {
		job.done = true
		m.appendJobLogLocked(job, line)
		return true
	}
	if match := playitErrorMarkerPattern.FindStringSubmatch(line); match != nil {
		job.lastError = match[1]
		m.appendJobLogLocked(job, line)
		return true
	}
	return false
}

func (m *playitBuildManager) appendJobLogLocked(job *playitSubprocessJob, line string) {
	job.logs = append(job.logs, line)
	if len(job.logs) > maxPlayitLogLines {
		job.logs = job.logs[len(job.logs)-maxPlayitLogLines:]
	}
}

// waitJob waits for the subprocess to exit and finalizes job state.
func (m *playitBuildManager) waitJob(cmd *exec.Cmd, job *playitSubprocessJob, current func() *playitSubprocessJob) {
	err := cmd.Wait()
	m.mu.Lock()
	if current() != job {
		m.mu.Unlock()
		return
	}
	job.done = true
	success := err == nil && job.lastError == ""
	if err != nil && job.lastError == "" {
		job.lastError = err.Error()
	}
	callback := job.onComplete
	m.mu.Unlock()
	if callback != nil {
		callback(success)
	}
}

// writeEmbeddedScript writes an embedded shell script to scriptDir/name,
// makes it executable, and returns its path.
func writeEmbeddedScript(scriptDir string, name string, content string) (string, error) {
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(scriptDir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o755); err != nil {
		return "", err
	}
	return path, nil
}

// commandAvailable runs a quick command and reports whether it succeeded.
func commandAvailable(name string, args ...string) bool {
	cmd := exec.Command(name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// buildScriptDir returns where embedded build scripts are written at runtime.
func (h apiHandler) playitBuildScriptDir() string {
	return filepath.Join(h.config.DataDir, "tools", "playit", "scripts")
}
