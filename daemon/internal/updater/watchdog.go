package updater

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// WatchdogParams describes the update a watchdog process supervises.
type WatchdogParams struct {
	Host           string
	Port           int
	DataDir        string
	ServerRoot     string
	WebDir         string
	ExpectVersion  string
	FromVersion    string
	TimeoutSeconds int
	// ResumeServerIDs are the servers that were running before the update, to start again afterwards.
	ResumeServerIDs []string
}

// WatchdogCommand is the hidden subcommand the previous version runs to watch a restart.
const WatchdogCommand = "__update-watchdog"

// Args builds the watchdog command line. binary is the live program path,
// which the watchdog restores if the new version does not come up.
func (p WatchdogParams) Args(binary string) []string {
	return []string{
		WatchdogCommand,
		"--binary", binary,
		"--host", p.Host,
		"--port", fmt.Sprint(p.Port),
		"--data-dir", p.DataDir,
		"--server-root", p.ServerRoot,
		"--web-dir", p.WebDir,
		"--expect-version", p.ExpectVersion,
		"--from-version", p.FromVersion,
		"--timeout-seconds", fmt.Sprint(p.TimeoutSeconds),
		"--resume-servers", strings.Join(p.ResumeServerIDs, ","),
	}
}

// StartWatchdog launches the previous version as a detached supervisor. It
// waits for the restarted daemon to report the new version, and if that does
// not happen in time it restores the previous version and starts it again.
// It must be called after the swap and before the restart.
func (m *Manager) StartWatchdog(params WatchdogParams) error {
	previous := m.binaryPath + previousSuffix
	if !fileExists(previous) {
		return errors.New("the previous version was not kept, so an automatic rollback is not possible")
	}
	cmd := exec.Command(previous, params.Args(m.binaryPath)...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Env = append(os.Environ(), "CLIFF_DETACHED=1")
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start the update watchdog: %w", err)
	}
	return cmd.Process.Release()
}
