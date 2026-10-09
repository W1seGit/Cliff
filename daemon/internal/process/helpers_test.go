package process

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

// fakeServer describes a managed server whose launch target lives in dir.
func fakeServer(dir string, launchTarget string) store.Server {
	return fakeServerWithID("srv_test", dir, launchTarget)
}

func fakeServerWithID(id string, dir string, launchTarget string) store.Server {
	return store.Server{
		ID:          id,
		Name:        "Shutdown Test",
		Path:        dir,
		Type:        "vanilla",
		JavaPath:    "java",
		MinMemoryMB: 512,
		MaxMemoryMB: 512,
		Port:        25565,
		LaunchJar:   launchTarget,
	}
}

// writeLaunchScript writes a launch script into dir and returns its file name.
// The Unix body is written as <base>.sh and the Windows body as <base>.bat;
// only the one for the current OS is used.
func writeLaunchScript(t *testing.T, dir string, base string, unixBody string, windowsBody string) string {
	t.Helper()
	name, body := base+".sh", unixBody
	if runtime.GOOS == "windows" {
		name, body = base+".bat", windowsBody
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return name
}

// writeFakeServerScript writes a server that prints its ready line, then
// prints "stopped" when it reads "stop" on stdin.
func writeFakeServerScript(t *testing.T, dir string) string {
	t.Helper()
	return writeLaunchScript(t, dir, "run",
		"#!/bin/sh\necho 'Done (0.1s)! For help, type \"help\"'\nread line\nif [ \"$line\" = \"stop\" ]; then echo stopped; fi\n",
		"@echo off\r\necho Done (0.1s)! For help, type \"help\"\r\nset /p cmd=\r\nif \"%cmd%\"==\"stop\" echo stopped\r\n")
}

// writeFailingServerScript writes a server that prints "boot failed" and exits 2.
func writeFailingServerScript(t *testing.T, dir string) string {
	t.Helper()
	return writeLaunchScript(t, dir, "fail",
		"#!/bin/sh\necho boot failed\nexit 2\n",
		"@echo off\r\necho boot failed\r\nexit /b 2\r\n")
}

// writeChattyFailingServerScript writes a server that fills both stdout and
// stderr, then exits 2, to check that output is drained before the exit is
// reported.
func writeChattyFailingServerScript(t *testing.T, dir string) string {
	t.Helper()
	return writeLaunchScript(t, dir, "chatty-fail",
		"#!/bin/sh\nfor i in 1 2 3 4 5; do echo stdout-$i; echo stderr-$i >&2; done\necho stdout-final-line\necho stderr-final-line >&2\nexit 2\n",
		"@echo off\r\nfor %%i in (1 2 3 4 5) do echo stdout-%%i\r\nfor %%i in (1 2 3 4 5) do echo stderr-%%i 1>&2\r\necho stdout-final-line\r\necho stderr-final-line 1>&2\r\nexit /b 2\r\n")
}

// writeSilentServerScript writes a server that never prints a ready line.
func writeSilentServerScript(t *testing.T, dir string) string {
	t.Helper()
	return writeLaunchScript(t, dir, "quiet",
		"#!/bin/sh\necho 'Loading libraries, please wait...'\nread line\n",
		"@echo off\r\necho Loading libraries, please wait...\r\nset /p cmd=\r\n")
}

// startFakeServer starts the standard fake server in a fresh manager, waits for
// it to be running, and stops it when the test ends.
func startFakeServer(t *testing.T) (*Manager, store.Server) {
	t.Helper()
	dir := t.TempDir()
	manager := NewManager(t.TempDir())
	t.Cleanup(func() { manager.Shutdown(2 * time.Second) })
	server := fakeServer(dir, writeFakeServerScript(t, dir))
	if _, err := manager.Start(server); err != nil {
		t.Fatal(err)
	}
	waitForLifecycle(t, manager, server.ID, LifecycleRunning)
	return manager, server
}

// waitFor polls cond until it returns true, failing the test with what() once
// the timeout passes.
func waitFor(t *testing.T, timeout time.Duration, what func() string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, what())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForLifecycle(t *testing.T, manager *Manager, serverID string, lifecycle Lifecycle) {
	t.Helper()
	waitFor(t, 5*time.Second,
		func() string {
			return fmt.Sprintf("server %s to reach lifecycle %s; current=%s logs=%v", serverID, lifecycle, manager.StatusFor(serverID).Lifecycle, manager.Logs(serverID))
		},
		func() bool { return manager.StatusFor(serverID).Lifecycle == lifecycle })
}

func waitForStatusEvent(t *testing.T, events <-chan Event, serverID string) Event {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if event.Type == "status" && event.ServerID == serverID {
				return event
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for status event for %s", serverID)
		}
	}
}
