package httpserver

import (
	"strconv"
	"testing"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func TestServerHealthCacheReusesFreshEntry(t *testing.T) {
	cache := &serverHealthCache{}
	computes := 0
	server := store.Server{ID: "srv_health", Path: t.TempDir(), LaunchJar: "server.jar"}

	first := cache.get(server, time.Hour, func(store.Server) serverHealth {
		computes++
		return serverHealth{Status: "ready", Checks: []serverHealthCheck{{ID: "first"}}}
	})
	second := cache.get(server, time.Hour, func(store.Server) serverHealth {
		computes++
		return serverHealth{Status: "blocked"}
	})

	if computes != 1 {
		t.Fatalf("expected one health computation, got %d", computes)
	}
	if first.Status != "ready" || second.Status != "ready" {
		t.Fatalf("expected cached ready health, got %#v and %#v", first, second)
	}
	second.Checks[0].ID = "mutated"
	third := cache.get(server, time.Hour, func(store.Server) serverHealth {
		computes++
		return serverHealth{Status: "blocked"}
	})
	if third.Checks[0].ID != "first" {
		t.Fatalf("cached health should be returned by copy, got %#v", third.Checks)
	}
}

func TestServerHealthCacheRefreshesWhenProfileChanges(t *testing.T) {
	cache := &serverHealthCache{}
	server := store.Server{ID: "srv_health", Path: t.TempDir(), LaunchJar: "server.jar", Port: 25565}
	first := cache.get(server, time.Hour, func(store.Server) serverHealth {
		return serverHealth{Status: "ready"}
	})
	server.Port = 25566
	second := cache.get(server, time.Hour, func(store.Server) serverHealth {
		return serverHealth{Status: "attention"}
	})

	if first.Status != "ready" || second.Status != "attention" {
		t.Fatalf("expected changed profile to refresh cached health, got %#v then %#v", first, second)
	}
}

func TestServerHealthCacheStaysBounded(t *testing.T) {
	cache := &serverHealthCache{}
	for index := 0; index < maxServerHealthCacheEntries+25; index++ {
		server := store.Server{ID: "srv_" + strconv.Itoa(index), Path: t.TempDir(), LaunchJar: "server.jar"}
		cache.get(server, time.Hour, func(store.Server) serverHealth {
			return serverHealth{Status: "ready"}
		})
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.entries) > maxServerHealthCacheEntries {
		t.Fatalf("expected health cache to stay at or below %d entries, got %d", maxServerHealthCacheEntries, len(cache.entries))
	}
}
