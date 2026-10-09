package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/process"
	"github.com/W1seGit/Cliff/daemon/internal/store"
)

// Webhook event names a webhook can subscribe to.
const (
	notifyServerStarted  = "server.started"
	notifyServerReady    = "server.ready"
	notifyServerStopped  = "server.stopped"
	notifyServerCrashed  = "server.crashed"
	notifyServerRestart  = "server.restarting"
	notifyRestartGaveUp  = "server.restart-failed"
	notifyBackupDone     = "backup.completed"
	notifyBackupFailed   = "backup.failed"
	notifyPlayerJoin     = "player.join"
	notifyPlayerLeave    = "player.leave"
	notifyModsUpdated    = "mods.updated"
	notifyServerUpgraded = "server.upgraded"
)

// webhookEventCatalog is what the dashboard offers when setting up a webhook.
var webhookEventCatalog = []webhookEventInfo{
	{notifyServerStarted, "Server starting"},
	{notifyServerReady, "Server ready"},
	{notifyServerStopped, "Server stopped"},
	{notifyServerCrashed, "Server crashed"},
	{notifyServerRestart, "Server restarting after a crash"},
	{notifyRestartGaveUp, "Cliff gave up restarting a server"},
	{notifyBackupDone, "Backup completed"},
	{notifyBackupFailed, "Backup failed"},
	{notifyPlayerJoin, "Player joined"},
	{notifyPlayerLeave, "Player left"},
	{notifyModsUpdated, "Mods updated"},
	{notifyServerUpgraded, "Server version upgraded"},
}

type webhookEventInfo struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// notification is one thing worth telling the owner about.
type notification struct {
	Event      string `json:"event"`
	Level      string `json:"level"` // info, success, warning or error
	Title      string `json:"title"`
	Message    string `json:"message,omitempty"`
	ServerID   string `json:"serverId,omitempty"`
	ServerName string `json:"serverName,omitempty"`
	At         string `json:"at"`
}

type webhookNotifier struct {
	store  *store.Store
	client *http.Client
}

func newWebhookNotifier(db *store.Store) *webhookNotifier {
	return &webhookNotifier{store: db, client: &http.Client{Timeout: 10 * time.Second}}
}

// notify delivers note to every enabled webhook subscribed to its event. It
// never blocks the caller, and delivery problems are only logged.
func (n *webhookNotifier) notify(note notification) {
	if n == nil || n.store == nil {
		return
	}
	if note.At == "" {
		note.At = time.Now().UTC().Format(time.RFC3339)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		hooks, err := n.store.ListWebhooks(ctx)
		cancel()
		if err != nil {
			slog.Warn("could not load webhooks", "error", err)
			return
		}
		for _, hook := range hooks {
			if hook.Enabled && slices.Contains(hook.Events, note.Event) {
				if err := n.deliver(hook, note); err != nil {
					slog.Warn("webhook delivery failed", "webhook", hook.Name, "event", note.Event, "error", err)
				}
			}
		}
	}()
}

// deliver posts one notification, retrying once on a network error or a 5xx/429.
func (n *webhookNotifier) deliver(hook store.Webhook, note notification) error {
	body, err := webhookPayload(hook.Kind, note)
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		request, err := http.NewRequest(http.MethodPost, hook.URL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("User-Agent", "Cliff-Webhook")
		response, err := n.client.Do(request)
		if err != nil {
			lastErr = err
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		response.Body.Close()
		switch {
		case response.StatusCode >= 200 && response.StatusCode < 300:
			return nil
		case response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500:
			lastErr = fmt.Errorf("webhook returned %s", response.Status)
		default:
			return fmt.Errorf("webhook returned %s", response.Status)
		}
	}
	return lastErr
}

func webhookPayload(kind string, note notification) ([]byte, error) {
	if kind != store.WebhookKindDiscord {
		return json.Marshal(note)
	}
	colors := map[string]int{"success": 0x2ECC71, "warning": 0xF1C40F, "error": 0xE74C3C, "info": 0x5865F2}
	color, ok := colors[note.Level]
	if !ok {
		color = colors["info"]
	}
	embed := map[string]any{
		"title":     note.Title,
		"color":     color,
		"timestamp": note.At,
	}
	if note.Message != "" {
		embed["description"] = truncateRunes(note.Message, 1800)
	}
	if note.ServerName != "" {
		embed["footer"] = map[string]string{"text": note.ServerName}
	}
	return json.Marshal(map[string]any{"username": "Cliff", "embeds": []any{embed}})
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

// notificationForLifecycle turns a process lifecycle event into a notification.
func notificationForLifecycle(event process.LifecycleEvent) (notification, bool) {
	name := event.Server.Name
	note := notification{ServerID: event.Server.ID, ServerName: name}
	switch event.Type {
	case process.EventStarted:
		note.Event, note.Level, note.Title = notifyServerStarted, "info", name+" is starting"
	case process.EventReady:
		note.Event, note.Level, note.Title = notifyServerReady, "success", name+" is online"
		note.Message = "Started in " + event.Uptime.Round(time.Second).String()
	case process.EventStopped:
		note.Event, note.Level, note.Title = notifyServerStopped, "info", name+" stopped"
	case process.EventCrashed:
		note.Event, note.Level, note.Title = notifyServerCrashed, "error", name+" crashed"
		note.Message = fmt.Sprintf("Exit code %d after %s.\n%s", event.ExitCode, event.Uptime.Round(time.Second), event.LastOutput)
	case process.EventPlayerJoin:
		note.Event, note.Level, note.Title = notifyPlayerJoin, "info", event.Player+" joined "+name
		note.Message = fmt.Sprintf("%d online", event.PlayerCount)
	case process.EventPlayerLeave:
		note.Event, note.Level, note.Title = notifyPlayerLeave, "info", event.Player+" left "+name
		note.Message = fmt.Sprintf("%d online", event.PlayerCount)
	default:
		return notification{}, false
	}
	return note, true
}

// --- HTTP handlers ---------------------------------------------------------

func (h apiHandler) webhooks(w http.ResponseWriter, r *http.Request) {
	hooks, err := h.store.ListWebhooks(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhooks": hooks, "events": webhookEventCatalog})
}

func (h apiHandler) createWebhook(w http.ResponseWriter, r *http.Request) {
	var input store.Webhook
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid webhook body")
		return
	}
	hook, err := h.store.CreateWebhook(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]store.Webhook{"webhook": hook})
}

func (h apiHandler) updateWebhook(w http.ResponseWriter, r *http.Request) {
	var input store.Webhook
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid webhook body")
		return
	}
	hook, err := h.store.UpdateWebhook(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]store.Webhook{"webhook": hook})
}

func (h apiHandler) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	if err := h.store.DeleteWebhook(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// testWebhook sends a sample message and reports whether it was accepted.
func (h apiHandler) testWebhook(w http.ResponseWriter, r *http.Request) {
	hook, ok, err := h.store.GetWebhook(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "webhook not found")
		return
	}
	note := notification{
		Event:   "test",
		Level:   "success",
		Title:   "Cliff test notification",
		Message: "If you can read this, the webhook works.",
		At:      time.Now().UTC().Format(time.RFC3339),
	}
	if err := h.notifier.deliver(hook, note); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// notifyBackup reports the outcome of a user-visible backup.
func (h apiHandler) notifyBackup(server store.Server, err error) {
	note := notification{Event: notifyBackupDone, Level: "success", Title: "Backup completed", ServerID: server.ID, ServerName: server.Name}
	if err != nil {
		note.Event, note.Level, note.Title, note.Message = notifyBackupFailed, "error", "Backup failed", err.Error()
	}
	h.notifier.notify(note)
}
