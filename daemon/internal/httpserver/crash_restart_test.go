package httpserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/process"
	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func TestCrashRestarterStopsAfterTheLimitAndForgetsOldAttempts(t *testing.T) {
	restarter := newCrashRestarter()
	server := store.Server{ID: "srv_a", RestartMaxAttempts: 2, RestartWindowMinutes: 10}
	start := time.Now()

	for want := 1; want <= 2; want++ {
		if got, ok := restarter.reserve(server, start); !ok || got != want {
			t.Fatalf("attempt %d: got %d, allowed=%v", want, got, ok)
		}
	}
	if _, ok := restarter.reserve(server, start.Add(time.Minute)); ok {
		t.Fatal("a third restart inside the window should be refused")
	}
	if got, ok := restarter.reserve(server, start.Add(11*time.Minute)); !ok || got != 1 {
		t.Fatalf("attempts outside the window should expire: got %d, allowed=%v", got, ok)
	}
	if _, ok := restarter.reserve(store.Server{ID: "srv_b", RestartMaxAttempts: 1, RestartWindowMinutes: 10}, start); !ok {
		t.Fatal("servers are limited independently")
	}
}

func TestCrashRestartDelayBacksOffToAMinute(t *testing.T) {
	want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, time.Minute, time.Minute}
	for index, expected := range want {
		if got := crashRestartDelay(index + 1); got != expected {
			t.Fatalf("attempt %d: got %s, want %s", index+1, got, expected)
		}
	}
}

func TestNotificationForLifecycle(t *testing.T) {
	server := store.Server{ID: "srv_a", Name: "Survival"}
	note, ok := notificationForLifecycle(process.LifecycleEvent{Type: process.EventCrashed, Server: server, ExitCode: 1, Uptime: 90 * time.Second, LastOutput: "boom"})
	if !ok || note.Event != notifyServerCrashed || note.Level != "error" || !strings.Contains(note.Message, "boom") {
		t.Fatalf("unexpected crash notification: %+v", note)
	}
	note, ok = notificationForLifecycle(process.LifecycleEvent{Type: process.EventPlayerJoin, Server: server, Player: "Alex", PlayerCount: 3})
	if !ok || note.Event != notifyPlayerJoin || !strings.Contains(note.Title, "Alex") {
		t.Fatalf("unexpected join notification: %+v", note)
	}
	if _, ok := notificationForLifecycle(process.LifecycleEvent{Type: "unknown"}); ok {
		t.Fatal("unknown events produce no notification")
	}
}

func TestWebhookPayloadShapes(t *testing.T) {
	note := notification{Event: notifyServerReady, Level: "success", Title: "Survival is online", ServerName: "Survival", At: "2026-01-01T00:00:00Z"}

	raw, err := webhookPayload(store.WebhookKindDiscord, note)
	if err != nil {
		t.Fatal(err)
	}
	var discord struct {
		Embeds []struct {
			Title string `json:"title"`
			Color int    `json:"color"`
		} `json:"embeds"`
	}
	if err := json.Unmarshal(raw, &discord); err != nil || len(discord.Embeds) != 1 || discord.Embeds[0].Title != note.Title || discord.Embeds[0].Color != 0x2ECC71 {
		t.Fatalf("bad discord payload %s (%v)", raw, err)
	}

	raw, err = webhookPayload(store.WebhookKindGeneric, note)
	if err != nil {
		t.Fatal(err)
	}
	var generic notification
	if err := json.Unmarshal(raw, &generic); err != nil || generic.Event != notifyServerReady {
		t.Fatalf("bad generic payload %s (%v)", raw, err)
	}
}
