package java

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRequiredMajor(t *testing.T) {
	cases := []struct {
		version string
		want    int
	}{
		{"1.8.9", 8},
		{"1.17.1", 16},
		{"1.18.2", 17},
		{"1.21.5", 21},
		{"26.1.2", 25},
	}
	for _, test := range cases {
		if got := RequiredMajor(test.version); got != test.want {
			t.Fatalf("RequiredMajor(%q) = %d, expected %d", test.version, got, test.want)
		}
	}
}

func TestResolveKeepsExplicitJava(t *testing.T) {
	got, err := (Resolver{DataDir: t.TempDir()}).Resolve(context.Background(), "/usr/bin/java", "1.21.5")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/usr/bin/java" {
		t.Fatalf("expected explicit java path to be preserved, got %q", got)
	}
}

func TestResolveUsesExistingManagedJava(t *testing.T) {
	dataDir := t.TempDir()
	major := 21
	javaPath := filepath.Join(dataDir, "java", "temurin-21", "bin", "java")
	if runtime.GOOS == "windows" {
		javaPath += ".exe"
	}
	writeExecutable(t, javaPath)

	got, err := (Resolver{DataDir: dataDir}).Resolve(context.Background(), "auto", "1.21.5")
	if err != nil {
		t.Fatal(err)
	}
	if got != javaPath {
		t.Fatalf("expected managed Java %d at %q, got %q", major, javaPath, got)
	}
}

func TestResolveManagedOverrideUsesExistingManagedJava(t *testing.T) {
	dataDir := t.TempDir()
	javaPath := filepath.Join(dataDir, "java", "temurin-17", "bin", "java")
	if runtime.GOOS == "windows" {
		javaPath += ".exe"
	}
	writeExecutable(t, javaPath)

	got, err := (Resolver{DataDir: dataDir}).Resolve(context.Background(), "managed:17", "1.21.5")
	if err != nil {
		t.Fatal(err)
	}
	if got != javaPath {
		t.Fatalf("expected managed override java at %q, got %q", javaPath, got)
	}
}

func TestListMarksInstalledAndRequiredRuntimes(t *testing.T) {
	dataDir := t.TempDir()
	javaPath := filepath.Join(dataDir, "java", "temurin-25", "bin", "java")
	if runtime.GOOS == "windows" {
		javaPath += ".exe"
	}
	writeExecutable(t, javaPath)

	runtimes := (Resolver{DataDir: dataDir}).List(25)
	var found RuntimeInfo
	for _, item := range runtimes {
		if item.Major == 25 {
			found = item
			break
		}
	}
	if found.Major != 25 {
		t.Fatalf("expected Java 25 in runtime list: %#v", runtimes)
	}
	if !found.Installed || !found.Required || found.Path != javaPath {
		t.Fatalf("unexpected Java 25 runtime info: %#v", found)
	}
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fake java"), 0o755); err != nil {
		t.Fatal(err)
	}
}
