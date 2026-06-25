package config

import (
	"path/filepath"
	"testing"
)

func TestLoadUsesServerRootOption(t *testing.T) {
	t.Setenv("CLIFF_SERVER_ROOT", filepath.Join(t.TempDir(), "env-servers"))
	optionRoot := filepath.Join(t.TempDir(), "option-servers")

	cfg, err := Load(Options{
		DataDir:    filepath.Join(t.TempDir(), "data"),
		ServerRoot: optionRoot,
	})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.ServerRoot != optionRoot {
		t.Fatalf("expected option server root %q, got %q", optionRoot, cfg.ServerRoot)
	}
}

func TestLoadFallsBackToServerRootEnvironment(t *testing.T) {
	envRoot := filepath.Join(t.TempDir(), "env-servers")
	t.Setenv("CLIFF_SERVER_ROOT", envRoot)

	cfg, err := Load(Options{DataDir: filepath.Join(t.TempDir(), "data")})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.ServerRoot != envRoot {
		t.Fatalf("expected env server root %q, got %q", envRoot, cfg.ServerRoot)
	}
}
