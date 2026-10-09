package httpserver

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func buildMrpack(t *testing.T, index string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	if err := writeZipBytes(archive, "modrinth.index.json", []byte(index)); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestReadMrpackManifest(t *testing.T) {
	manifest, err := readMrpackManifest(buildMrpack(t, `{"formatVersion":1,"game":"minecraft","name":"Pack","dependencies":{"minecraft":"1.21.1","fabric-loader":"0.16.0"},"files":[]}`))
	if err != nil || manifest.Name != "Pack" || manifest.Dependencies["fabric-loader"] != "0.16.0" {
		t.Fatalf("unexpected manifest %+v (%v)", manifest, err)
	}
	if _, err := readMrpackManifest([]byte("not a zip")); err == nil {
		t.Fatal("garbage should be rejected")
	}
	if _, err := readMrpackManifest(buildMrpack(t, "{")); err == nil {
		t.Fatal("a broken index should be rejected")
	}
}

func TestCheckMrpackForServer(t *testing.T) {
	fabric := store.Server{Type: "fabric", MinecraftVersion: "1.21.1"}
	good := mrpackManifest{Game: "minecraft", Dependencies: map[string]string{"minecraft": "1.21.1", "fabric-loader": "0.16.0"}, Files: []mrpackFile{
		{Path: "mods/a.jar", Downloads: []string{"https://cdn.modrinth.com/data/a/versions/1/a.jar"}},
	}}
	if err := checkMrpackForServer(good, fabric); err != nil {
		t.Fatalf("a matching pack should pass: %v", err)
	}

	cases := map[string]struct {
		manifest mrpackManifest
		server   store.Server
		wantText string
	}{
		"wrong minecraft": {mrpackManifest{Dependencies: map[string]string{"minecraft": "1.20.1", "fabric-loader": "1"}}, fabric, "Minecraft 1.20.1"},
		"wrong loader":    {mrpackManifest{Dependencies: map[string]string{"minecraft": "1.21.1", "forge": "1"}}, fabric, "does not support"},
		"loader on paper": {mrpackManifest{Dependencies: map[string]string{"minecraft": "1.21.1", "fabric-loader": "1"}}, store.Server{Type: "paper", MinecraftVersion: "1.21.1"}, "needs a mod loader"},
		"bad host": {mrpackManifest{Dependencies: map[string]string{"minecraft": "1.21.1", "fabric-loader": "1"}, Files: []mrpackFile{
			{Path: "mods/a.jar", Downloads: []string{"https://127.0.0.1/a.jar"}},
		}}, fabric, "host Cliff does not allow"},
		"plain http": {mrpackManifest{Dependencies: map[string]string{"minecraft": "1.21.1", "fabric-loader": "1"}, Files: []mrpackFile{
			{Path: "mods/a.jar", Downloads: []string{"http://cdn.modrinth.com/a.jar"}},
		}}, fabric, "host Cliff does not allow"},
	}
	for name, tc := range cases {
		err := checkMrpackForServer(tc.manifest, tc.server)
		if err == nil || !strings.Contains(err.Error(), tc.wantText) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.wantText)
		}
	}
}

func TestVerifyPackFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.jar")
	touch(t, path, "jar bytes")
	sum := sha1.Sum([]byte("jar bytes"))
	good := hex.EncodeToString(sum[:])
	if err := verifyPackFile(path, map[string]string{"sha1": good}); err != nil {
		t.Fatalf("matching checksum rejected: %v", err)
	}
	if err := verifyPackFile(path, map[string]string{"sha1": strings.Repeat("0", 40)}); err == nil {
		t.Fatal("a wrong checksum must be rejected")
	}
	if err := verifyPackFile(path, nil); err != nil {
		t.Fatal("a pack without checksums is not verified")
	}
}

func TestCompareMinecraftVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.21.4", "1.20.1", 1},
		{"1.20.1", "1.20.1", 0},
		{"1.20", "1.20.0", 0},
		{"1.19.4", "1.20", -1},
		{"26.1", "1.21.11", 1},
		{"24w14a", "1.20.1", 0},
	}
	for _, tc := range cases {
		if got := compareMinecraftVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compare(%s, %s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestModrinthLoadersFor(t *testing.T) {
	if got := modrinthLoadersFor("purpur"); len(got) != 4 || got[0] != "purpur" {
		t.Fatalf("purpur loaders: %v", got)
	}
	if got := modrinthLoadersFor("fabric"); len(got) != 1 || got[0] != "fabric" {
		t.Fatalf("fabric loaders: %v", got)
	}
	if modrinthLoadersFor("vanilla") != nil {
		t.Fatal("vanilla has no mod loaders")
	}
}
