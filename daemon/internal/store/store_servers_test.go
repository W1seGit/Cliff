package store

import (
	"context"
	"testing"
)

func TestServerRestartPolicyDefaultsAndUpdates(t *testing.T) {
	db := openTestStore(t)
	ctx := context.Background()
	server, err := db.CreateServer(ctx, Server{Name: "A", Path: t.TempDir(), MinMemoryMB: 1024, MaxMemoryMB: 2048, Port: 25565})
	if err != nil {
		t.Fatal(err)
	}
	if server.RestartPolicy != RestartPolicyOff || server.RestartMaxAttempts != DefaultRestartMaxAttempts || server.RestartWindowMinutes != DefaultRestartWindowMinutes {
		t.Fatalf("defaults were not applied: %+v", server)
	}

	updated, err := db.UpdateServer(ctx, server.ID, Server{RestartPolicy: RestartPolicyOnCrash, RestartMaxAttempts: 99, RestartWindowMinutes: 30, JvmPreset: "aikar"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.RestartPolicy != RestartPolicyOnCrash || updated.RestartMaxAttempts != 20 || updated.RestartWindowMinutes != 30 || updated.JvmPreset != "aikar" {
		t.Fatalf("restart settings were not saved or clamped: %+v", updated)
	}

	// A sparse internal update (no policy) must leave the policy alone.
	sparse, err := db.UpdateServer(ctx, server.ID, Server{LaunchJar: "server.jar"})
	if err != nil {
		t.Fatal(err)
	}
	if sparse.RestartPolicy != RestartPolicyOnCrash || sparse.JvmPreset != "aikar" {
		t.Fatalf("a sparse update reset the policy: %+v", sparse)
	}
}
