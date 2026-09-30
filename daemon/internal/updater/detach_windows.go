//go:build windows

package updater

import (
	"os/exec"
	"syscall"
)

// detach starts cmd detached from the console so it outlives the daemon that launched it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x00000008 | 0x00000200 | 0x08000000, // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP | CREATE_NO_WINDOW
	}
}
