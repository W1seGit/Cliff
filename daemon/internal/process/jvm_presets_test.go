package process

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func TestJvmPresetFlagsScaleWithHeap(t *testing.T) {
	small := JvmPresetFlags(JvmPresetAikar, 4096)
	large := JvmPresetFlags(JvmPresetAikar, 16384)
	if !slices.Contains(small, "-XX:G1HeapRegionSize=8M") || !slices.Contains(large, "-XX:G1HeapRegionSize=16M") {
		t.Fatalf("region size should grow past 12 GB: small=%v large=%v", small, large)
	}
	if JvmPresetFlags("", 4096) != nil || JvmPresetFlags("nope", 4096) != nil {
		t.Fatal("the default and unknown presets add no flags")
	}
	if !ValidJvmPreset("") || !ValidJvmPreset(JvmPresetAikar) || ValidJvmPreset("nope") {
		t.Fatal("ValidJvmPreset accepted the wrong ids")
	}
}

func TestLaunchCommandPutsPresetBeforeJar(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "server.jar"), []byte("jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, args, _, err := launchCommand(store.Server{Path: dir, LaunchJar: "server.jar", MinMemoryMB: 1024, MaxMemoryMB: 2048, JvmPreset: JvmPresetAikar})
	if err != nil {
		t.Fatal(err)
	}
	jar := slices.Index(args, "-jar")
	if jar < 0 || slices.Index(args, "-XX:+UseG1GC") > jar || args[len(args)-1] != "nogui" {
		t.Fatalf("flags must come before -jar: %v", args)
	}
}
