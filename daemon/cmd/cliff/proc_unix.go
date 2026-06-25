//go:build !windows

package main

import (
	"os/exec"
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
