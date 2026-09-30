//go:build !windows

package main

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// setDetachFlags configures the exec.Cmd to detach from the terminal on Unix.
func setDetachFlags(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// syscallKill sends a signal to a process on Unix.
func syscallKill(pid int, sig syscall.Signal) error {
	return syscall.Kill(pid, sig)
}

// pidExists reports whether a process with this PID exists. EPERM means it
// exists but belongs to another user (for example a daemon started with sudo),
// which still counts as running.
func pidExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// pidIsZombie reports whether the process has exited but was not reaped yet.
func pidIsZombie(pid int) bool {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output()
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
}

// pidCommandName returns the executable name of a running process, or "".
func pidCommandName(pid int) string {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// scheduleSelfDelete is only needed on Windows, where a running exe is locked.
func scheduleSelfDelete(root string, keep []string) bool {
	return false
}

// removeInstallFromUserPath is only needed on Windows.
func removeInstallFromUserPath(root string) error {
	return nil
}
