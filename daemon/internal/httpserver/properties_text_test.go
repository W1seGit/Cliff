package httpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func TestValidatePropertiesTextAcceptsRealisticFiles(t *testing.T) {
	text := "#Minecraft server properties\n#Mon Sep 29 19:00:00 EDT 2026\nmotd=A Minecraft Server\nmax-players=20\nserver-port=25565\nview-distance=10\nlevel-seed=\nlevel-name=world\n\n! also a comment\nonline-mode=true\n"
	if err := validatePropertiesText(text); err != nil {
		t.Fatalf("a normal file must validate: %v", err)
	}
	if err := validatePropertiesText(""); err != nil {
		t.Fatalf("an empty file is allowed: %v", err)
	}
	if err := validatePropertiesText("max-players = 30\r\nmotd = hi\r\n"); err != nil {
		t.Fatalf("spaces around = and CRLF endings are allowed: %v", err)
	}
}

func TestValidatePropertiesTextReportsLineNumbers(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"no equals", "motd=ok\nthis is not a property\n", "Line 2"},
		{"empty key", "=value\n", "Line 1"},
		{"port range", "motd=x\nserver-port=99999\n", "Line 2"},
		{"players", "max-players=0\n", "Line 1"},
		{"distance not a number", "view-distance=far\n", "Line 1"},
		{"empty level name", "level-name=\n", "level-name"},
		{"binary", "motd=a\x00b\n", "binary"},
	}
	for _, tc := range cases {
		err := validatePropertiesText(tc.text)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want it to mention %q", tc.name, err, tc.want)
		}
	}
	if err := validatePropertiesText(strings.Repeat("a=b\n", maxPropertiesTextBytes/4+10)); err == nil {
		t.Fatal("an oversized file must be rejected")
	}
}

func TestWriteServerPropertiesTextKeepsCommentsAndOrder(t *testing.T) {
	dir := t.TempDir()
	server := store.Server{Path: dir}
	text := "# my notes\nz-last=1\nmotd=Hello\n\n# section\nmax-players=12"
	if err := writeServerPropertiesText(server, text); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "server.properties"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != text+"\n" {
		t.Fatalf("file must be saved verbatim (plus a trailing newline), got %q", got)
	}

	payload := readServerPropertiesPayload(server)
	if payload.Text != text+"\n" {
		t.Fatalf("payload text = %q", payload.Text)
	}
	if payload.Editable.MaxPlayers != 12 || payload.Editable.MOTD != "Hello" {
		t.Fatalf("editable fields must reflect the file: %+v", payload.Editable)
	}
	if payload.Raw["z-last"] != "1" {
		t.Fatalf("raw map must include every key: %v", payload.Raw)
	}
}

func TestReadPropertiesRawTrimsKeysAndLeadingValueSpace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.properties")
	if err := os.WriteFile(path, []byte("max-players = 30\nmotd=  spaced  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw := readPropertiesRaw(path)
	if raw["max-players"] != "30" {
		t.Fatalf("max-players = %q, want 30", raw["max-players"])
	}
	if raw["motd"] != "spaced  " {
		t.Fatalf("only leading value whitespace is dropped, got %q", raw["motd"])
	}
}
