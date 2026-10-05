package winproc

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestHideSetsNoWindowFlags(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "exit", "0")
	Hide(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatalf("Hide did not set the no-window flags: %+v", cmd.SysProcAttr)
	}
}

func TestHideKeepsExistingFlags(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "exit", "0")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200} // CREATE_NEW_PROCESS_GROUP
	Hide(cmd)
	if cmd.SysProcAttr.CreationFlags != 0x00000200|createNoWindow {
		t.Fatalf("Hide dropped an existing creation flag: %#x", cmd.SysProcAttr.CreationFlags)
	}
}
