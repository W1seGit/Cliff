//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess    = kernel32.NewProc("OpenProcess")
	procCloseHandle    = kernel32.NewProc("CloseHandle")
	procGetExitCodePro = kernel32.NewProc("GetExitCodeProcess")
)

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
)

// setDetachFlags configures the exec.Cmd to detach from the terminal on Windows.
func setDetachFlags(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x00000008 | 0x00000200, // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	}
}

// syscallKill sends a signal to a process on Windows.
// Windows doesn't have Unix signals, so we use the process API.
// sig=0 checks if alive, anything else terminates the process.
func syscallKill(pid int, sig syscall.Signal) error {
	if sig == 0 {
		return checkProcessAlive(pid)
	}
	return killProcessWindows(pid)
}

func checkProcessAlive(pid int) error {
	handle, _, _ := procOpenProcess.Call(uintptr(processQueryLimitedInformation), 0, uintptr(pid))
	if handle == 0 {
		return fmt.Errorf("OpenProcess failed for PID %d", pid)
	}
	defer procCloseHandle.Call(handle)

	var exitCode uint32
	ret, _, _ := procGetExitCodePro.Call(handle, uintptr(unsafe.Pointer(&exitCode)))
	if ret == 0 {
		return fmt.Errorf("GetExitCodeProcess failed for PID %d", pid)
	}
	if exitCode == stillActive {
		return nil
	}
	return fmt.Errorf("process %d exited with code %d", pid, exitCode)
}

func killProcessWindows(pid int) error {
	handle, _, _ := procOpenProcess.Call(uintptr(processQueryLimitedInformation|1), 0, uintptr(pid))
	if handle == 0 {
		return fmt.Errorf("OpenProcess failed for PID %d", pid)
	}
	defer procCloseHandle.Call(handle)

	// Use TerminateProcess
	procTerminateProcess := kernel32.NewProc("TerminateProcess")
	ret, _, _ := procTerminateProcess.Call(handle, 1)
	if ret == 0 {
		return fmt.Errorf("TerminateProcess failed for PID %d", pid)
	}
	return nil
}
