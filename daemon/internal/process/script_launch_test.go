package process

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const neoForgeRunBat = "@echo off\r\n" +
	"REM Forge requires a configured set of both JVM and program arguments.\r\n" +
	"REM Add custom JVM arguments to the user_jvm_args.txt\r\n" +
	"java @user_jvm_args.txt @libraries/net/neoforged/neoforge/21.10.5/win_args.txt %*\r\n" +
	"pause\r\n"

const neoForgeRunSh = "#!/usr/bin/env sh\n" +
	"# Forge requires a configured set of both JVM and program arguments.\n" +
	"java @user_jvm_args.txt @libraries/net/neoforged/neoforge/21.10.5/unix_args.txt \"$@\"\n"

func writeScript(t *testing.T, name string, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDirectScriptCommandParsesNeoForgeRunBat(t *testing.T) {
	script := writeScript(t, "run.bat", neoForgeRunBat)
	javaPath := filepath.Join(t.TempDir(), "jdk", "bin", "java.exe")
	if runtime.GOOS != "windows" {
		javaPath = filepath.Join(t.TempDir(), "jdk", "bin", "java")
	}

	command, args, ok := directScriptCommand(script, javaPath, 2048, 4096, nil)
	if !ok {
		t.Fatal("expected the NeoForge script to be parsed")
	}
	if command != javaPath {
		t.Fatalf("command = %q, want managed java %q", command, javaPath)
	}
	got := strings.Join(args, " ")
	want := "-Xms2048M -Xmx4096M @user_jvm_args.txt @libraries/net/neoforged/neoforge/21.10.5/win_args.txt nogui"
	if got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}
	if strings.Contains(got, "pause") || strings.Contains(got, "%*") {
		t.Fatalf("script noise leaked into args: %q", got)
	}
}

func TestDirectScriptCommandParsesUnixRunSh(t *testing.T) {
	script := writeScript(t, "run.sh", neoForgeRunSh)
	command, args, ok := directScriptCommand(script, "java", 1024, 2048, []string{"-Dfoo=bar"})
	if !ok {
		t.Fatal("expected run.sh to be parsed")
	}
	if command != "java" {
		t.Fatalf("a non-absolute java should stay as written, got %q", command)
	}
	got := strings.Join(args, " ")
	want := "-Xms1024M -Xmx2048M @user_jvm_args.txt @libraries/net/neoforged/neoforge/21.10.5/unix_args.txt -Dfoo=bar nogui"
	if got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

func TestDirectScriptCommandKeepsExplicitMemoryAndNoGUI(t *testing.T) {
	script := writeScript(t, "run.sh", "java -Xmx8G -jar server.jar nogui\n")
	_, args, ok := directScriptCommand(script, "java", 1024, 2048, nil)
	if !ok {
		t.Fatal("expected script to be parsed")
	}
	got := strings.Join(args, " ")
	if strings.Count(got, "-Xmx") != 1 || !strings.Contains(got, "-Xmx8G") {
		t.Fatalf("explicit -Xmx must win, got %q", got)
	}
	if strings.Count(got, "nogui") != 1 {
		t.Fatalf("nogui must not be duplicated, got %q", got)
	}
}

func TestDirectScriptCommandRejectsUnknownVariablesAndMissingJava(t *testing.T) {
	withVars := writeScript(t, "run.sh", "java $JAVA_ARGS -jar server.jar\n")
	if _, _, ok := directScriptCommand(withVars, "java", 1024, 2048, nil); ok {
		t.Fatal("scripts with shell variables must fall back to being run as-is")
	}
	noJava := writeScript(t, "run.sh", "echo hello\n./start-thing\n")
	if _, _, ok := directScriptCommand(noJava, "java", 1024, 2048, nil); ok {
		t.Fatal("scripts without a java line must fall back")
	}
	if _, _, ok := directScriptCommand(filepath.Join(t.TempDir(), "missing.sh"), "java", 1024, 2048, nil); ok {
		t.Fatal("a missing script must not parse")
	}
}

func TestLaunchCommandUsesManagedJavaForNeoForgeScript(t *testing.T) {
	dir := t.TempDir()
	scriptName := "run.sh"
	script := neoForgeRunSh
	if runtime.GOOS == "windows" {
		scriptName = "run.bat"
		script = neoForgeRunBat
	}
	if err := os.WriteFile(filepath.Join(dir, scriptName), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	javaPath := filepath.Join(t.TempDir(), "temurin-21", "bin", "java")
	server := fakeServer(dir, scriptName)
	server.JavaPath = javaPath

	cmd, _, text, err := launchCommand(server)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(text, javaPath+" ") {
		t.Fatalf("expected the managed java to be the command, got %q", text)
	}
	if strings.Contains(text, "cmd.exe") {
		t.Fatalf("the script must not be run through cmd.exe (its pause would hang), got %q", text)
	}
	foundHome := false
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "JAVA_HOME=") && strings.Contains(entry, "temurin-21") {
			foundHome = true
		}
	}
	if !foundHome {
		t.Fatal("expected JAVA_HOME to point at the managed runtime")
	}
}

func TestJavaEnvironmentPutsManagedJavaFirstOnPath(t *testing.T) {
	javaPath := filepath.Join(t.TempDir(), "jdk", "bin", "java")
	env := javaEnvironment(javaPath)
	binDir := filepath.Dir(javaPath)
	var pathValue, javaHome string
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		switch {
		case strings.EqualFold(key, "PATH"):
			pathValue = value
		case key == "JAVA_HOME":
			javaHome = value
		}
	}
	if !strings.HasPrefix(pathValue, binDir+string(os.PathListSeparator)) {
		t.Fatalf("PATH should start with %q, got %q", binDir, pathValue)
	}
	if javaHome != filepath.Dir(binDir) {
		t.Fatalf("JAVA_HOME = %q, want %q", javaHome, filepath.Dir(binDir))
	}
	if got := javaEnvironment("java"); len(got) != len(os.Environ()) {
		t.Fatal("a bare java command should leave the environment unchanged")
	}
}

func writeSilentServerScript(t *testing.T, dir string) string {
	t.Helper()
	launchTarget := "quiet.sh"
	script := "#!/bin/sh\necho 'Loading libraries, please wait...'\nread line\n"
	if runtime.GOOS == "windows" {
		launchTarget = "quiet.bat"
		script = "@echo off\r\necho Loading libraries, please wait...\r\nset /p cmd=\r\n"
	}
	if err := os.WriteFile(filepath.Join(dir, launchTarget), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return launchTarget
}

// A server that has not printed its ready line must stay "starting"; Start
// returning must not be taken as "the server is up".
func TestManagerStaysStartingUntilServerIsReady(t *testing.T) {
	dir := t.TempDir()
	launchTarget := writeSilentServerScript(t, dir)

	manager := NewManager(t.TempDir())
	defer manager.Shutdown(2 * time.Second)
	status, err := manager.Start(fakeServer(dir, launchTarget))
	if err != nil {
		t.Fatal(err)
	}
	if status.Lifecycle != LifecycleStarting {
		t.Fatalf("a server that has not reported ready must be starting, got %s", status.Lifecycle)
	}
	time.Sleep(300 * time.Millisecond)
	if got := manager.StatusFor("srv_test").Lifecycle; got != LifecycleStarting {
		t.Fatalf("lifecycle drifted to %s without a ready message", got)
	}
}

func TestManagerReadyWatchdogEventuallyReportsRunning(t *testing.T) {
	previous := readyTimeout
	readyTimeout = 300 * time.Millisecond
	defer func() { readyTimeout = previous }()

	dir := t.TempDir()
	launchTarget := writeSilentServerScript(t, dir)
	manager := NewManager(t.TempDir())
	defer manager.Shutdown(2 * time.Second)
	if _, err := manager.Start(fakeServer(dir, launchTarget)); err != nil {
		t.Fatal(err)
	}
	waitForLifecycle(t, manager, "srv_test", LifecycleRunning)
	logs := strings.Join(manager.Logs("srv_test"), "\n")
	if !strings.Contains(logs, "no ready message") {
		t.Fatalf("the watchdog should explain why it marked the server running, logs:\n%s", logs)
	}
}
