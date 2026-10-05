//go:build !windows

package winproc

import "os/exec"

// Hide does nothing outside Windows, where child processes never open windows.
func Hide(cmd *exec.Cmd) {}
