//go:build windows

package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
		CreationFlags: 0x00000008 | 0x00000200 | 0x08000000, // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP | CREATE_NO_WINDOW
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

func pidExists(pid int) bool {
	return pid > 0 && checkProcessAlive(pid) == nil
}

func pidIsZombie(pid int) bool { return false }

// pidCommandName returns the image name of a running process, or "".
func pidCommandName(pid int) string {
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
	if err != nil {
		return ""
	}
	records, err := csv.NewReader(strings.NewReader(string(out))).ReadAll()
	if err != nil || len(records) == 0 || len(records[0]) == 0 {
		return ""
	}
	return records[0][0]
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

// scheduleSelfDelete removes what the running cliff.exe could not delete. The
// helper waits a moment for this process to exit, then deletes the whole
// install folder, or only the executable when the user's data must stay.
func scheduleSelfDelete(root string, keep []string) bool {
	self, err := os.Executable()
	if err != nil {
		return false
	}
	target := fmt.Sprintf(`rmdir /s /q "%s"`, root)
	keptInside := false
	for _, path := range keep {
		if pathInside(path, root) {
			keptInside = true
		}
	}
	if keptInside {
		// Something the user wants to keep lives here, so only the program goes.
		target = fmt.Sprintf(`del /f /q "%s"`, self)
	}
	command := fmt.Sprintf(`/d /c ping 127.0.0.1 -n 3 >nul & %s`, target)
	cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"))
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       `cmd.exe ` + command,
		CreationFlags: 0x00000008 | 0x00000200 | 0x08000000,
	}
	if err := cmd.Start(); err != nil {
		return false
	}
	_ = cmd.Process.Release()
	return true
}

// removeInstallFromUserPath drops the install folder from the user's PATH.
func removeInstallFromUserPath(root string) error {
	quoted := strings.ReplaceAll(root, "'", "''")
	script := fmt.Sprintf(`$root='%s'.TrimEnd('\'); `+
		`$path=[Environment]::GetEnvironmentVariable('Path','User'); if(-not $path){exit 0}; `+
		`$all=@($path -split ';' | Where-Object { $_ }); `+
		`$parts=@($all | Where-Object { $_.TrimEnd('\') -ine $root }); `+
		`if($parts.Count -eq $all.Count){exit 0}; `+
		`[Environment]::SetEnvironmentVariable('Path', ($parts -join ';'), 'User')`, quoted)
	return exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Run()
}
