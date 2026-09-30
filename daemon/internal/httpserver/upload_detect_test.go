package httpserver

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func makeZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "archive")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(entries[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClassifyArchive(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		entries map[string]string
		kind    uploadKind
		loader  string
	}{
		{"fabric mod", "cool.jar", map[string]string{"fabric.mod.json": "{}", "a/B.class": "x"}, uploadKindMod, "fabric"},
		{"forge mod", "cool.jar", map[string]string{"META-INF/mods.toml": "x"}, uploadKindMod, "forge"},
		{"neoforge mod", "cool.jar", map[string]string{"META-INF/neoforge.mods.toml": "x"}, uploadKindMod, "neoforge"},
		{"bukkit plugin", "cool.jar", map[string]string{"plugin.yml": "name: x"}, uploadKindPlugin, "bukkit"},
		{"paper plugin", "cool.jar", map[string]string{"paper-plugin.yml": "name: x"}, uploadKindPlugin, "bukkit"},
		{"datapack", "pack.zip", map[string]string{"pack.mcmeta": "{}", "data/x/functions/a.mcfunction": "say hi"}, uploadKindDatapack, ""},
		{"datapack in wrapper folder", "pack.zip", map[string]string{"MyPack/pack.mcmeta": "{}", "MyPack/data/x/a.json": "{}"}, uploadKindDatapack, ""},
		{"resource pack", "res.zip", map[string]string{"pack.mcmeta": "{}", "assets/minecraft/textures/a.png": "x"}, uploadKindResources, ""},
		{"world save", "world.zip", map[string]string{"world/level.dat": "x", "world/region/r.0.0.mca": "x"}, uploadKindWorld, ""},
		{"zip of mods", "mods.zip", map[string]string{"a.jar": "x", "b.jar": "x", "readme.txt": "hi"}, uploadKindBundle, ""},
		{"zip with mods folder", "pack.zip", map[string]string{"mods/a.jar": "x", "mods/b.jar": "x", "config/a.toml": "x"}, uploadKindBundle, ""},
		{"jar without descriptor", "mystery.jar", map[string]string{"a/B.class": "x"}, uploadKindUnknownJar, ""},
		{"unrelated zip", "notes.zip", map[string]string{"notes.txt": "x"}, uploadKindUnknown, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info, err := classifyArchive(makeZip(t, tc.entries), tc.file)
			if err != nil {
				t.Fatal(err)
			}
			if info.Kind != tc.kind {
				t.Fatalf("kind = %s, want %s", info.Kind, tc.kind)
			}
			if tc.loader != "" && !contains(info.Loaders, tc.loader) {
				t.Fatalf("loaders = %v, want %s", info.Loaders, tc.loader)
			}
		})
	}
}

func TestClassifyArchiveIgnoresNestedJarsInsideAMod(t *testing.T) {
	info, err := classifyArchive(makeZip(t, map[string]string{
		"fabric.mod.json":       "{}",
		"META-INF/jars/dep.jar": "x",
	}), "big-mod.jar")
	if err != nil {
		t.Fatal(err)
	}
	if info.Kind != uploadKindMod {
		t.Fatalf("a jar with bundled dependencies is still a mod, got %s", info.Kind)
	}
}

func TestClassifyArchiveRejectsNonArchives(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fake.jar")
	if err := os.WriteFile(path, []byte("not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := classifyArchive(path, "fake.jar"); err == nil {
		t.Fatal("expected an error for a file that is not an archive")
	}
}

func TestServerAcceptsModAndPlugin(t *testing.T) {
	if ok, _ := serverAcceptsMod("fabric", []string{"fabric"}); !ok {
		t.Fatal("fabric mod on fabric server must be accepted")
	}
	if ok, _ := serverAcceptsMod("fabric", []string{"quilt"}); !ok {
		t.Fatal("quilt mods run on fabric")
	}
	if ok, _ := serverAcceptsMod("neoforge", []string{"forge", "neoforge"}); !ok {
		t.Fatal("mods.toml mods are accepted on neoforge")
	}
	if ok, why := serverAcceptsMod("forge", []string{"fabric"}); ok || !strings.Contains(why, "fabric") {
		t.Fatalf("fabric mod on forge must be rejected with a reason, got ok=%v why=%q", ok, why)
	}
	if ok, why := serverAcceptsMod("paper", []string{"fabric"}); ok || !strings.Contains(why, "plugins") {
		t.Fatalf("mod on a plugin server must be rejected, got ok=%v why=%q", ok, why)
	}
	if ok, _ := serverAcceptsMod("vanilla", []string{"fabric"}); ok {
		t.Fatal("vanilla loads no mods")
	}
	if ok, _ := serverAcceptsPlugin("paper"); !ok {
		t.Fatal("plugins run on paper")
	}
	if ok, _ := serverAcceptsPlugin("fabric"); ok {
		t.Fatal("plugins do not run on fabric")
	}
}

func newUploadSession(t *testing.T, serverType string) (*uploadSession, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "server.properties"), []byte("level-name=survival\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := store.Server{ID: "srv", Path: dir, Type: serverType}
	return &uploadSession{ctx: context.Background(), server: server}, dir
}

func TestPlaceInstallsDatapackIntoActiveWorld(t *testing.T) {
	session, dir := newUploadSession(t, "fabric")
	archive := makeZip(t, map[string]string{"pack.mcmeta": "{}", "data/x/a.json": "{}"})

	results := session.place(archive, "my-datapack.zip", "")
	if len(results) != 1 || results[0].Status != "added" || results[0].Kind != uploadKindDatapack {
		t.Fatalf("unexpected results: %+v", results)
	}
	if !fileExists(filepath.Join(dir, "survival", "datapacks", "my-datapack.zip")) {
		t.Fatal("datapack should land in the active world's datapacks folder")
	}
}

func TestPlaceExtractsEveryJarFromAModBundle(t *testing.T) {
	session, dir := newUploadSession(t, "fabric")
	fabricJar := makeZip(t, map[string]string{"fabric.mod.json": "{}"})
	forgeJar := makeZip(t, map[string]string{"META-INF/mods.toml": "x"})
	fabricBytes, _ := os.ReadFile(fabricJar)
	forgeBytes, _ := os.ReadFile(forgeJar)
	bundle := makeZip(t, map[string]string{
		"good.jar":  string(fabricBytes),
		"wrong.jar": string(forgeBytes),
		"notes.txt": "hello",
	})

	results := session.place(bundle, "mods.zip", "")
	byName := map[string]uploadResult{}
	for _, result := range results {
		byName[result.Name] = result
	}
	if byName["good.jar"].Status != "added" || byName["good.jar"].Source != "mods.zip" {
		t.Fatalf("good.jar should be added from the bundle: %+v", byName["good.jar"])
	}
	if byName["wrong.jar"].Status != "skipped" || !strings.Contains(byName["wrong.jar"].Message, "forge") {
		t.Fatalf("a forge mod on a fabric server should be skipped with a reason: %+v", byName["wrong.jar"])
	}
	if !fileExists(filepath.Join(dir, "mods", "good.jar")) {
		t.Fatal("the compatible mod should be in the mods folder")
	}
	if fileExists(filepath.Join(dir, "mods", "wrong.jar")) {
		t.Fatal("the incompatible mod must not be installed")
	}
}

func TestPlaceRenamesDuplicateJars(t *testing.T) {
	session, dir := newUploadSession(t, "paper")
	plugin := makeZip(t, map[string]string{"plugin.yml": "name: x"})
	first := session.place(plugin, "Essentials.jar", "")
	second := session.place(plugin, "Essentials.jar", "")
	if first[0].Name != "Essentials.jar" || second[0].Name != "Essentials-2.jar" {
		t.Fatalf("names = %q then %q", first[0].Name, second[0].Name)
	}
	if !fileExists(filepath.Join(dir, "plugins", "Essentials-2.jar")) {
		t.Fatal("the renamed plugin should exist")
	}
	if !strings.Contains(second[0].Message, "Renamed") {
		t.Fatalf("expected a rename note, got %q", second[0].Message)
	}
}

func TestPlaceRefusesWorldsResourcePacksAndUnknownFiles(t *testing.T) {
	session, _ := newUploadSession(t, "fabric")
	cases := []struct {
		name    string
		file    string
		entries map[string]string
		want    string
	}{
		{"world", "world.zip", map[string]string{"level.dat": "x"}, "Import world"},
		{"resources", "res.zip", map[string]string{"pack.mcmeta": "{}", "assets/a.png": "x"}, "resource pack"},
		{"unknown", "notes.zip", map[string]string{"a.txt": "x"}, "Could not tell"},
	}
	for _, tc := range cases {
		results := session.place(makeZip(t, tc.entries), tc.file, "")
		if len(results) != 1 || results[0].Status != "skipped" || !strings.Contains(results[0].Message, tc.want) {
			t.Fatalf("%s: unexpected results %+v", tc.name, results)
		}
	}
	if results := session.place(makeZip(t, map[string]string{"a": "b"}), "notes.txt", ""); results[0].Status != "skipped" {
		t.Fatalf("non jar/zip files must be skipped: %+v", results)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
