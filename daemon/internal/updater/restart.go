package updater

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

// Restart re-executes the daemon binary with the same arguments.
// On Unix, it uses syscall.Exec to replace the current process.
// On Windows, it spawns a new process and exits the current one.
// The caller should ensure graceful shutdown is complete before calling this.
func Restart(binaryPath string, args []string) error {
	if binaryPath == "" {
		var err error
		binaryPath, err = os.Executable()
		if err != nil {
			return fmt.Errorf("resolve executable path: %w", err)
		}
	}

	slog.Info("restarting daemon after update", "binary", binaryPath)

	if runtime.GOOS != "windows" {
		// On Unix, syscall.Exec replaces the process in-place.
		// The caller's deferred cleanup (e.g. server.Shutdown) should
		// already be done before we reach here.
		return syscall.Exec(binaryPath, append([]string{binaryPath}, args...), os.Environ())
	}

	// On Windows, spawn a new process and signal the current one to exit.
	helperArgs := append([]string{"__restart-child", "--delay-ms", "1500", "--"}, args...)
	cmd := exec.Command(binaryPath, helperArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Dir = ""
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start new process: %w", err)
	}

	// Give the new process a moment to start, then exit.
	go func() {
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	}()
	return nil
}

// RestartAsync schedules a restart after a short delay, allowing the
// HTTP response to be sent to the client first.
func RestartAsync(binaryPath string, args []string, delay time.Duration) {
	go func() {
		time.Sleep(delay)
		if err := Restart(binaryPath, args); err != nil {
			slog.Error("restart failed", "error", err)
		}
	}()
}
