package process

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func TestManagerShutdownStopsRunningServer(t *testing.T) {
	dir := t.TempDir()
	launchTarget := writeFakeServerScript(t, dir)

	manager := NewManager(t.TempDir())
	status, err := manager.Start(fakeServer(dir, launchTarget))
	if err != nil {
		t.Fatal(err)
	}
	if status.Lifecycle != LifecycleRunning {
		t.Fatalf("expected running lifecycle after startup wait, got %s", status.Lifecycle)
	}

	waitForLifecycle(t, manager, "srv_test", LifecycleRunning)
	manager.Shutdown(2 * time.Second)
	waitForLifecycle(t, manager, "srv_test", LifecycleStopped)

	logs := strings.Join(manager.Logs("srv_test"), "\n")
	if !strings.Contains(logs, "Stop requested") {
		t.Fatalf("expected shutdown to request a clean stop, logs:\n%s", logs)
	}
	if !strings.Contains(logs, "stopped") {
		t.Fatalf("expected child process to receive stop command, logs:\n%s", logs)
	}
}

func TestManagerRestartWaitsForStopBeforeStart(t *testing.T) {
	dir := t.TempDir()
	launchTarget := writeFakeServerScript(t, dir)

	manager := NewManager(t.TempDir())
	server := fakeServer(dir, launchTarget)
	if _, err := manager.Start(server); err != nil {
		t.Fatal(err)
	}
	waitForLifecycle(t, manager, server.ID, LifecycleRunning)
	firstPID := manager.StatusFor(server.ID).PID

	status, err := manager.Restart(server, false, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if status.Lifecycle != LifecycleRunning {
		t.Fatalf("expected restarted process to be running after startup wait, got %s", status.Lifecycle)
	}
	waitForLifecycle(t, manager, server.ID, LifecycleRunning)
	restartedPID := manager.StatusFor(server.ID).PID
	if restartedPID == 0 || restartedPID == firstPID {
		t.Fatalf("expected restart to launch a new process, first pid=%d restarted pid=%d", firstPID, restartedPID)
	}
	manager.Shutdown(2 * time.Second)
	waitForLifecycle(t, manager, server.ID, LifecycleStopped)

	logs := strings.Join(manager.Logs(server.ID), "\n")
	if !strings.Contains(logs, "Starting Shutdown Test") {
		t.Fatalf("expected restarted process logs, logs:\n%s", logs)
	}
	if !strings.Contains(logs, "Stop requested") {
		t.Fatalf("expected final shutdown to request a clean stop, logs:\n%s", logs)
	}
}

func TestManagerStopAndWaitReturnsStoppedLifecycle(t *testing.T) {
	dir := t.TempDir()
	launchTarget := writeFakeServerScript(t, dir)

	manager := NewManager(t.TempDir())
	server := fakeServer(dir, launchTarget)
	if _, err := manager.Start(server); err != nil {
		t.Fatal(err)
	}
	waitForLifecycle(t, manager, server.ID, LifecycleRunning)

	status, err := manager.StopAndWait(server.ID, false, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if status.Lifecycle != LifecycleStopped {
		t.Fatalf("expected stopped lifecycle after stop wait, got %s", status.Lifecycle)
	}
	if manager.IsRunning(server.ID) {
		t.Fatal("server should not remain running after successful stop wait")
	}
}

func TestManagerStartFailsWhenProcessExitsDuringStartup(t *testing.T) {
	dir := t.TempDir()
	launchTarget := writeFailingServerScript(t, dir)

	manager := NewManager(t.TempDir())
	status, err := manager.Start(fakeServer(dir, launchTarget))
	if err == nil {
		t.Fatal("expected startup failure")
	}
	if status.Lifecycle != LifecycleStopped {
		t.Fatalf("expected stopped lifecycle after failed startup, got %s", status.Lifecycle)
	}
	if manager.IsRunning("srv_test") {
		t.Fatal("failed startup should not leave server marked running")
	}

	logs := strings.Join(manager.Logs("srv_test"), "\n")
	if !strings.Contains(logs, "boot failed") {
		t.Fatalf("expected startup failure logs to be retained, logs:\n%s", logs)
	}
}

func TestManagerDrainsOutputBeforeStoppedStatus(t *testing.T) {
	dir := t.TempDir()
	launchTarget := writeChattyFailingServerScript(t, dir)

	manager := NewManager(t.TempDir())
	status, err := manager.Start(fakeServer(dir, launchTarget))
	if err == nil {
		t.Fatal("expected startup failure")
	}
	if status.Lifecycle != LifecycleStopped {
		t.Fatalf("expected stopped lifecycle after failed startup, got %s", status.Lifecycle)
	}

	logs := strings.Join(manager.Logs("srv_test"), "\n")
	for _, expected := range []string{"stdout-final-line", "stderr-final-line", "Server exited"} {
		if !strings.Contains(logs, expected) {
			t.Fatalf("expected retained log %q, logs:\n%s", expected, logs)
		}
	}
}

func TestManagerStatusEventsIncludeFleetStatus(t *testing.T) {
	firstDir := t.TempDir()
	secondDir := t.TempDir()
	firstLaunch := writeFakeServerScript(t, firstDir)
	secondLaunch := writeFakeServerScript(t, secondDir)

	manager := NewManager(t.TempDir())
	events, unsubscribe := manager.Subscribe()
	defer unsubscribe()

	first := fakeServerWithID("srv_first", firstDir, firstLaunch)
	second := fakeServerWithID("srv_second", secondDir, secondLaunch)
	if _, err := manager.Start(first); err != nil {
		t.Fatal(err)
	}
	waitForLifecycle(t, manager, first.ID, LifecycleRunning)
	if _, err := manager.Start(second); err != nil {
		t.Fatal(err)
	}
	waitForLifecycle(t, manager, second.ID, LifecycleRunning)

	event := waitForStatusEvent(t, events, second.ID)
	if event.Status.Servers[first.ID].RunningServerID != first.ID {
		t.Fatalf("status event did not include first server runtime: %#v", event.Status.Servers)
	}
	if event.Status.Servers[second.ID].RunningServerID != second.ID {
		t.Fatalf("status event did not include second server runtime: %#v", event.Status.Servers)
	}
	if event.Status.Servers[second.ID].Usage != nil {
		t.Fatalf("status events should not collect usage by default: %#v", event.Status.Servers[second.ID].Usage)
	}

	manager.Shutdown(2 * time.Second)
}

func TestManagerStatusLightSkipsUsageCollection(t *testing.T) {
	dir := t.TempDir()
	launchTarget := writeFakeServerScript(t, dir)

	manager := NewManager(t.TempDir())
	server := fakeServer(dir, launchTarget)
	if _, err := manager.Start(server); err != nil {
		t.Fatal(err)
	}
	waitForLifecycle(t, manager, server.ID, LifecycleRunning)

	light := manager.StatusLight()
	if light.Usage != nil {
		t.Fatalf("light fleet status should not include usage: %#v", light.Usage)
	}
	if light.Servers[server.ID].Usage != nil {
		t.Fatalf("light server status should not include usage: %#v", light.Servers[server.ID].Usage)
	}

	full := manager.Status()
	if full.Servers[server.ID].Usage == nil {
		t.Fatal("full server status should include usage")
	}

	manager.Shutdown(2 * time.Second)
}

func TestManagerRetainsBoundedLogs(t *testing.T) {
	manager := NewManager(t.TempDir())
	proc := &managedProcess{serverID: "srv_logs"}
	manager.running[proc.serverID] = proc

	for i := 0; i < maxRetainedLogLines+25; i++ {
		manager.pushLog(proc, "line-"+strconv.Itoa(i))
	}

	logs := manager.Logs(proc.serverID)
	if len(logs) != maxRetainedLogLines {
		t.Fatalf("expected %d retained logs, got %d", maxRetainedLogLines, len(logs))
	}
	if logs[0] != "line-25" {
		t.Fatalf("expected oldest retained line to be line-25, got %q", logs[0])
	}
	if logs[len(logs)-1] != "line-1024" {
		t.Fatalf("expected newest retained line to be line-1024, got %q", logs[len(logs)-1])
	}

	if history := manager.history[proc.serverID]; len(history) != 0 {
		t.Fatalf("running log updates should not copy retained history on every line, got %d history lines", len(history))
	}

	manager.mu.Lock()
	manager.rememberLocked(proc.serverID, proc.logs)
	manager.mu.Unlock()

	history := manager.history[proc.serverID]
	if len(history) != maxRetainedLogLines {
		t.Fatalf("expected %d retained history lines, got %d", maxRetainedLogLines, len(history))
	}
	if history[0] != logs[0] || history[len(history)-1] != logs[len(logs)-1] {
		t.Fatalf("history does not match retained logs: first=%q/%q last=%q/%q", history[0], logs[0], history[len(history)-1], logs[len(logs)-1])
	}
}

func TestManagerTruncatesOversizedLogLines(t *testing.T) {
	manager := NewManager(t.TempDir())
	proc := &managedProcess{serverID: "srv_long_logs"}
	manager.running[proc.serverID] = proc

	longLine := strings.Repeat("x", maxRetainedLogLineBytes+500)
	manager.pushLog(proc, longLine)

	logs := manager.Logs(proc.serverID)
	if len(logs) != 1 {
		t.Fatalf("expected one retained log, got %#v", logs)
	}
	if len(logs[0]) != maxRetainedLogLineBytes {
		t.Fatalf("expected retained line to be capped at %d bytes, got %d", maxRetainedLogLineBytes, len(logs[0]))
	}
	if !strings.HasSuffix(logs[0], truncatedLogSuffix) {
		t.Fatalf("expected truncation suffix, got %q", logs[0])
	}
}

func TestManagerPublishesTruncatedLogLines(t *testing.T) {
	manager := NewManager(t.TempDir())
	proc := &managedProcess{serverID: "srv_long_events"}
	manager.running[proc.serverID] = proc
	events, unsubscribe := manager.SubscribeFor(proc.serverID, true)
	defer unsubscribe()

	manager.pushLog(proc, strings.Repeat("x", maxRetainedLogLineBytes+500))

	select {
	case event := <-events:
		if len(event.Line) != maxRetainedLogLineBytes {
			t.Fatalf("expected event line to be capped at %d bytes, got %d", maxRetainedLogLineBytes, len(event.Line))
		}
		if !strings.HasSuffix(event.Line, truncatedLogSuffix) {
			t.Fatalf("expected truncation suffix, got %q", event.Line)
		}
	default:
		t.Fatal("expected log event")
	}
}

func TestManagerForgetClearsRetainedLogs(t *testing.T) {
	manager := NewManager(t.TempDir())
	manager.history["srv_removed"] = []string{"old line"}

	manager.Forget("srv_removed")

	if logs := manager.Logs("srv_removed"); len(logs) != 0 {
		t.Fatalf("expected removed server logs to be forgotten, got %#v", logs)
	}
}

func TestManagerPublishDoesNotBlockSlowSubscriber(t *testing.T) {
	manager := NewManager(t.TempDir())
	events, unsubscribe := manager.Subscribe()
	defer unsubscribe()

	for i := 0; i < subscriberQueueSize; i++ {
		manager.publish(Event{Type: "log", ServerID: "srv_slow", Line: fmt.Sprintf("line-%d", i)})
	}

	done := make(chan struct{})
	go func() {
		manager.publish(Event{Type: "log", ServerID: "srv_slow", Line: "dropped-if-full"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("publish blocked behind a slow console subscriber")
	}

	if len(events) != subscriberQueueSize {
		t.Fatalf("expected full subscriber queue to remain bounded at %d, got %d", subscriberQueueSize, len(events))
	}
}

func TestManagerSubscribeForFiltersLogsAndServerEvents(t *testing.T) {
	manager := NewManager(t.TempDir())
	events, unsubscribe := manager.SubscribeFor("srv_selected", false)
	defer unsubscribe()

	manager.publish(Event{Type: "log", ServerID: "srv_selected", Line: "hidden"})
	manager.publish(Event{Type: "status", ServerID: "srv_other", Status: Status{RunningServerID: "srv_other", Lifecycle: LifecycleRunning}})
	manager.publish(Event{Type: "status", ServerID: "srv_selected", Status: Status{RunningServerID: "srv_selected", Lifecycle: LifecycleRunning}})

	select {
	case event := <-events:
		if event.Type != "status" || event.ServerID != "srv_selected" {
			t.Fatalf("expected selected status event, got %#v", event)
		}
	default:
		t.Fatal("expected selected status event")
	}

	select {
	case event := <-events:
		t.Fatalf("unexpected filtered event: %#v", event)
	default:
	}
}

func TestRememberUsageSampleIsBounded(t *testing.T) {
	proc := &managedProcess{}
	for i := 0; i < maxUsageSamples+7; i++ {
		rememberUsageSample(proc, UsageSample{At: strconv.Itoa(i)})
	}

	if len(proc.usageSamples) != maxUsageSamples {
		t.Fatalf("expected %d usage samples, got %d", maxUsageSamples, len(proc.usageSamples))
	}
	if proc.usageSamples[0].At != "7" {
		t.Fatalf("expected oldest retained sample to be 7, got %q", proc.usageSamples[0].At)
	}
	if proc.usageSamples[len(proc.usageSamples)-1].At != "42" {
		t.Fatalf("expected newest retained sample to be 42, got %q", proc.usageSamples[len(proc.usageSamples)-1].At)
	}
}

func TestSplitArgsMatchesDashboardParser(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{"", []string{}},
		{"  --nogui  ", []string{"--nogui"}},
		{"--world \"My World\" --flag=value", []string{"--world", "My World", "--flag=value"}},
		{"--path 'C:/Minecraft Servers/Fabric' --safe", []string{"--path", "C:/Minecraft Servers/Fabric", "--safe"}},
		{"--escaped one\\ two --quote \\\"literal\\\"", []string{"--escaped", "one two", "--quote", "\"literal\""}},
		{"--dangling slash\\", []string{"--dangling", "slash\\"}},
	}

	for _, test := range cases {
		got := splitArgs(test.input)
		if len(got) != len(test.want) {
			t.Fatalf("splitArgs(%q) returned %#v, expected %#v", test.input, got, test.want)
		}
		for index := range got {
			if got[index] != test.want[index] {
				t.Fatalf("splitArgs(%q) returned %#v, expected %#v", test.input, got, test.want)
			}
		}
	}
}

func TestLaunchCommandPreservesQuotedExtraArgs(t *testing.T) {
	dir := t.TempDir()
	jarPath := filepath.Join(dir, "server.jar")
	if err := os.WriteFile(jarPath, []byte("placeholder"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, args, commandText, err := launchCommand(store.Server{
		Name:        "Quoted Args",
		Path:        dir,
		JavaPath:    "java",
		MinMemoryMB: 512,
		MaxMemoryMB: 1024,
		LaunchJar:   "server.jar",
		ExtraArgs:   `--world "My World" --path C:\Servers\One`,
	})
	if err != nil {
		t.Fatal(err)
	}

	wantArgs := []string{"-Xms512M", "-Xmx1024M", "-jar", "server.jar", "--world", "My World", "--path", "C:ServersOne", "nogui"}
	if len(args) != len(wantArgs) {
		t.Fatalf("launch args returned %#v, expected %#v", args, wantArgs)
	}
	for index := range args {
		if args[index] != wantArgs[index] {
			t.Fatalf("launch args returned %#v, expected %#v", args, wantArgs)
		}
	}
	if !strings.Contains(commandText, "My World") {
		t.Fatalf("command text did not include quoted arg value: %s", commandText)
	}
}

func TestLaunchCommandDoesNotDuplicateNoGUI(t *testing.T) {
	dir := t.TempDir()
	jarPath := filepath.Join(dir, "server.jar")
	if err := os.WriteFile(jarPath, []byte("placeholder"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, args, _, err := launchCommand(store.Server{
		Name:        "No GUI Args",
		Path:        dir,
		JavaPath:    "java",
		MinMemoryMB: 512,
		MaxMemoryMB: 1024,
		LaunchJar:   "server.jar",
		ExtraArgs:   "nogui",
	})
	if err != nil {
		t.Fatal(err)
	}

	count := 0
	for _, arg := range args {
		if strings.TrimLeft(strings.ToLower(arg), "-") == "nogui" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected one nogui arg, got %d in %#v", count, args)
	}
}

func TestLaunchCommandRejectsInstallerJars(t *testing.T) {
	installerJars := []string{
		"fabric-installer-1.1.1.jar",
		"forge-1.20.1-47.2.0-installer.jar",
		"neoforge-47.1.0-installer.jar",
	}
	for _, jar := range installerJars {
		t.Run(jar, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, jar), []byte("placeholder"), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, _, err := launchCommand(store.Server{
				Name:        "Installer Test",
				Path:        dir,
				JavaPath:    "java",
				MinMemoryMB: 512,
				MaxMemoryMB: 1024,
				LaunchJar:   jar,
			})
			if err == nil || !strings.Contains(err.Error(), "installer jar") {
				t.Fatalf("expected installer jar error for %s, got %v", jar, err)
			}
		})
	}
}

func TestLaunchCommandSuggestsBetterTargetForInstallerJar(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fabric-installer-1.1.1.jar"), []byte("placeholder"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fabric-server-launch.jar"), []byte("placeholder"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, _, err := launchCommand(store.Server{
		Name:        "Installer Test",
		Path:        dir,
		JavaPath:    "java",
		MinMemoryMB: 512,
		MaxMemoryMB: 1024,
		LaunchJar:   "fabric-installer-1.1.1.jar",
	})
	if err == nil || !strings.Contains(err.Error(), "fabric-server-launch.jar") {
		t.Fatalf("expected better launch target suggestion, got %v", err)
	}
}

func TestCollectUsageFromRowsIncludesProcessTree(t *testing.T) {
	usage := collectUsageFromRows(10, []processUsageRow{
		{pid: 1, parentPID: 0, rssKB: 500, cpuPercent: 99},
		{pid: 10, parentPID: 1, rssKB: 100, cpuPercent: 1.5},
		{pid: 11, parentPID: 10, rssKB: 200, cpuPercent: 2.5},
		{pid: 12, parentPID: 11, rssKB: 300, cpuPercent: 3.0},
		{pid: 20, parentPID: 1, rssKB: 400, cpuPercent: 4.0},
	})

	if usage.cpuPercent == nil || *usage.cpuPercent != 7.0 {
		t.Fatalf("expected tree CPU 7.0, got %#v", usage.cpuPercent)
	}
	if usage.memoryBytes == nil || *usage.memoryBytes != 600*1024 {
		t.Fatalf("expected tree memory 600 KiB, got %#v", usage.memoryBytes)
	}
}

func TestParseUnixProcessTable(t *testing.T) {
	rows := parseUnixProcessTable([]byte(`
    10     1  100  1.5
    11    10  200  2.5
    bad line
    12    11  300  3.0
  `))
	if len(rows) != 3 {
		t.Fatalf("expected 3 parsed rows, got %#v", rows)
	}
	if rows[1].pid != 11 || rows[1].parentPID != 10 || rows[1].rssKB != 200 || rows[1].cpuPercent != 2.5 {
		t.Fatalf("unexpected parsed row: %#v", rows[1])
	}
}

func TestParseWindowsUsageJSON(t *testing.T) {
	usage := parseWindowsUsageJSON([]byte(`{"CPU":12.5,"WorkingSet64":4096,"ProcessCount":3}`))
	if usage.cpuSeconds == nil || *usage.cpuSeconds != 12.5 {
		t.Fatalf("expected CPU seconds 12.5, got %#v", usage.cpuSeconds)
	}
	if usage.memoryBytes == nil || *usage.memoryBytes != 4096 {
		t.Fatalf("expected memory 4096, got %#v", usage.memoryBytes)
	}
}

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

func writeFakeServerScript(t *testing.T, dir string) string {
	t.Helper()
	launchTarget := "run.sh"
	script := "#!/bin/sh\necho 'Done (0.1s)! For help, type \"help\"'\nread line\nif [ \"$line\" = \"stop\" ]; then echo stopped; fi\n"
	if runtime.GOOS == "windows" {
		launchTarget = "run.bat"
		script = "@echo off\r\necho Done (0.1s)! For help, type \"help\"\r\nset /p cmd=\r\nif \"%cmd%\"==\"stop\" echo stopped\r\n"
	}
	if err := os.WriteFile(filepath.Join(dir, launchTarget), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return launchTarget
}

func writeFailingServerScript(t *testing.T, dir string) string {
	t.Helper()
	launchTarget := "fail.sh"
	script := "#!/bin/sh\necho boot failed\nexit 2\n"
	if runtime.GOOS == "windows" {
		launchTarget = "fail.bat"
		script = "@echo off\r\necho boot failed\r\nexit /b 2\r\n"
	}
	if err := os.WriteFile(filepath.Join(dir, launchTarget), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return launchTarget
}

func writeChattyFailingServerScript(t *testing.T, dir string) string {
	t.Helper()
	launchTarget := "chatty-fail.sh"
	script := "#!/bin/sh\nfor i in 1 2 3 4 5; do echo stdout-$i; echo stderr-$i >&2; done\necho stdout-final-line\necho stderr-final-line >&2\nexit 2\n"
	if runtime.GOOS == "windows" {
		launchTarget = "chatty-fail.bat"
		script = "@echo off\r\nfor %%i in (1 2 3 4 5) do echo stdout-%%i\r\nfor %%i in (1 2 3 4 5) do echo stderr-%%i 1>&2\r\necho stdout-final-line\r\necho stderr-final-line 1>&2\r\nexit /b 2\r\n"
	}
	if err := os.WriteFile(filepath.Join(dir, launchTarget), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return launchTarget
}

func waitForLifecycle(t *testing.T, manager *Manager, serverID string, lifecycle Lifecycle) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if manager.StatusFor(serverID).Lifecycle == lifecycle {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server %s did not reach lifecycle %s; current=%s logs=%v", serverID, lifecycle, manager.StatusFor(serverID).Lifecycle, manager.Logs(serverID))
}
