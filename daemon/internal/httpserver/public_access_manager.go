package httpserver

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/winproc"
)

const maxPlayitLogLines = 200

var playitClaimURLPattern = regexp.MustCompile(`https?://playit\.gg/claim/[A-Za-z0-9_-]+/?`)

var playitSecretPattern = regexp.MustCompile(`\b[A-Fa-f0-9]{32,}\b`)

type playitAgentManager struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	logs      []string
	claimURL  string
	claimCode string
	claiming  bool
	startedAt time.Time
	lastError string
}

func newPlayitAgentManager() *playitAgentManager {
	return &playitAgentManager{}
}

func (m *playitAgentManager) start(path string) error {
	return m.prepareManagedClaim(path)
}

func (m *playitAgentManager) stop() error {
	m.mu.Lock()
	cmd := m.cmd
	m.cmd = nil
	m.claiming = false
	m.startedAt = time.Time{}
	m.lastError = ""
	m.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			m.pushLog("Playit agent stop failed: " + err.Error())
			return err
		}
		m.pushLog("Playit agent stopped.")
	}
	return nil
}

func (m *playitAgentManager) reset(path string) error {
	m.mu.Lock()
	cmd := m.cmd
	m.cmd = nil
	m.claimURL = ""
	m.claimCode = ""
	m.claiming = false
	m.startedAt = time.Time{}
	m.lastError = ""
	m.logs = nil
	m.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			m.pushLog("Playit agent stop failed: " + err.Error())
			return err
		}
	}
	if err := os.Remove(playitManagedSecretPath(path)); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.pushLog("Playit secret reset failed: " + err.Error())
		return err
	}
	m.pushLog("Playit setup reset. Start the agent to get a fresh claim link.")
	return nil
}

func (m *playitAgentManager) prepareManagedClaim(path string) error {
	m.mu.Lock()
	if m.cmd != nil && m.cmd.Process != nil {
		m.mu.Unlock()
		return nil
	}
	m.claimURL = ""
	m.claimCode = ""
	m.claiming = false
	m.lastError = ""
	m.logs = nil
	m.startedAt = time.Now().UTC()
	m.mu.Unlock()

	return m.startManagedAgent(path)
}

func (m *playitAgentManager) exchangeManagedClaimAndStart(path string, code string) {
	m.pushLog("Waiting for Playit account approval")
	output, err := runPlayitCommand(path, "claim", "exchange", "--wait", "0", code)
	if err != nil {
		m.mu.Lock()
		m.claiming = false
		m.lastError = err.Error()
		m.mu.Unlock()
		m.pushLog("Playit claim exchange failed: " + err.Error())
		if strings.TrimSpace(output) != "" {
			m.pushLog("Playit claim exchange returned output; see Playit and try again.")
		}
		return
	}
	secret := parsePlayitSecret(output)
	if secret == "" {
		m.mu.Lock()
		m.claiming = false
		m.lastError = "Playit approved the agent but did not return a usable secret."
		m.mu.Unlock()
		m.pushLog("Playit approved the agent but did not return a usable secret.")
		return
	}
	secretPath, err := playitSecretPath(path)
	if err != nil {
		m.mu.Lock()
		m.claiming = false
		m.lastError = err.Error()
		m.mu.Unlock()
		m.pushLog("Playit secret path could not be found: " + err.Error())
		return
	}
	if err := writePlayitSecret(secretPath, secret); err != nil {
		m.mu.Lock()
		m.claiming = false
		m.lastError = err.Error()
		m.mu.Unlock()
		m.pushLog("Playit secret could not be saved: " + err.Error())
		return
	}
	m.mu.Lock()
	m.claiming = false
	m.mu.Unlock()
	m.pushLog("Playit account approved")
	if err := m.startManagedAgent(path); err != nil {
		m.pushLog("Playit agent could not start: " + err.Error())
	}
}

func (m *playitAgentManager) startManagedAgent(path string) error {
	m.mu.Lock()
	if m.cmd != nil && m.cmd.Process != nil {
		m.mu.Unlock()
		return nil
	}
	secretPath := playitManagedSecretPath(path)
	if err := os.MkdirAll(filepath.Dir(secretPath), 0o755); err != nil {
		m.mu.Unlock()
		return err
	}
	cmd := exec.Command(path, "--secret_path", secretPath, "-s", "start")
	winproc.Hide(cmd)
	cmd.Dir = filepath.Dir(path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		m.mu.Unlock()
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		m.mu.Unlock()
		return err
	}
	m.cmd = cmd
	m.startedAt = time.Now().UTC()
	m.lastError = ""
	m.mu.Unlock()

	if err := cmd.Start(); err != nil {
		m.mu.Lock()
		if m.cmd == cmd {
			m.cmd = nil
		}
		m.lastError = err.Error()
		m.mu.Unlock()
		return err
	}
	go m.scan(stdout)
	go m.scan(stderr)
	go m.wait(cmd)
	return nil
}

func (m *playitAgentManager) mergeStatus(status playitStatus) playitStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	status.Running = m.cmd != nil && m.cmd.Process != nil
	if status.Running {
		status.PID = m.cmd.Process.Pid
	}
	status.ClaimURL = m.claimURL
	status.Claiming = m.claiming
	if !m.startedAt.IsZero() {
		status.StartedAt = m.startedAt.Format(time.RFC3339)
	}
	status.Logs = append([]string(nil), m.logs...)
	status.Error = m.lastError
	return status
}

func (m *playitAgentManager) scan(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		m.pushLog(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		m.pushLog("Playit log stream failed: " + err.Error())
	}
}

func (m *playitAgentManager) wait(cmd *exec.Cmd) {
	err := cmd.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == cmd {
		m.cmd = nil
		if err != nil {
			m.lastError = err.Error()
			m.appendLogLocked("Playit agent exited: " + err.Error())
		} else {
			m.appendLogLocked("Playit agent exited")
		}
	}
}

func (m *playitAgentManager) pushLog(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if match := playitClaimURLPattern.FindString(line); match != "" {
		m.claimURL = match
	}
	if strings.Contains(line, "CodeNotFound") || strings.Contains(line, "CodeExpired") || strings.Contains(line, "UserRejected") {
		m.lastError = "Playit accepted the claim page action, but the local agent could not finish claiming. Reset Playit setup and try a fresh claim link."
	}
	m.appendLogLocked(line)
}

func (m *playitAgentManager) appendLogLocked(line string) {
	m.logs = append(m.logs, line)
	if len(m.logs) > maxPlayitLogLines {
		m.logs = m.logs[len(m.logs)-maxPlayitLogLines:]
	}
}

func runPlayitCommand(path string, args ...string) (string, error) {
	cmd := exec.Command(path, args...)
	winproc.Hide(cmd)
	cmd.Dir = filepath.Dir(path)
	output, err := cmd.CombinedOutput()
	return stripANSI(string(output)), err
}

func parsePlayitClaimCode(output string) string {
	matches := regexp.MustCompile(`\b[A-Fa-f0-9]{10}\b`).FindAllString(output, -1)
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1]
}

func parsePlayitSecret(output string) string {
	matches := playitSecretPattern.FindAllString(stripANSI(output), -1)
	if len(matches) == 0 {
		return ""
	}
	return strings.ToLower(matches[len(matches)-1])
}

func playitSecretPath(path string) (string, error) {
	output, err := runPlayitCommand(path, "secret-path")
	if err != nil {
		return "", err
	}
	secretPath := strings.TrimSpace(output)
	if secretPath == "" {
		return "", errors.New("Playit did not return a secret path")
	}
	return filepath.Clean(secretPath), nil
}

func playitManagedSecretPath(path string) string {
	return filepath.Join(filepath.Dir(path), "playit.toml")
}

func writePlayitSecret(secretPath string, secret string) error {
	if secret == "" {
		return errors.New("Playit secret is empty")
	}
	if err := os.MkdirAll(filepath.Dir(secretPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(secretPath, []byte(secret+"\n"), 0o600)
}

func stripANSI(input string) string {
	csiPattern := regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	oscPattern := regexp.MustCompile(`\x1b\][^\x07]*(\x07|\x1b\\)`)
	input = oscPattern.ReplaceAllString(input, "")
	return csiPattern.ReplaceAllString(input, "")
}
