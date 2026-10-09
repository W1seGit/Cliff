package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Webhook is a place Cliff posts server events to, such as a Discord channel.
type Webhook struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	URL       string   `json:"url"`
	Kind      string   `json:"kind"`
	Events    []string `json:"events"`
	Enabled   bool     `json:"enabled"`
	CreatedAt string   `json:"createdAt"`
}

const (
	WebhookKindDiscord = "discord"
	WebhookKindGeneric = "generic"
)

const webhookColumns = `id, name, url, kind, events, enabled, created_at`

func scanWebhook(row rowScanner) (Webhook, error) {
	var hook Webhook
	var events string
	var enabled string
	if err := row.Scan(&hook.ID, &hook.Name, &hook.URL, &hook.Kind, &events, &enabled, &hook.CreatedAt); err != nil {
		return Webhook{}, err
	}
	hook.Events = splitEvents(events)
	hook.Enabled = enabled == "true"
	return hook, nil
}

func splitEvents(value string) []string {
	events := []string{}
	for _, event := range strings.Split(value, ",") {
		if event = strings.TrimSpace(event); event != "" {
			events = append(events, event)
		}
	}
	return events
}

func normalizeWebhook(hook *Webhook) error {
	hook.Name = strings.TrimSpace(hook.Name)
	hook.URL = strings.TrimSpace(hook.URL)
	if hook.Name == "" {
		return errors.New("Webhook name is required")
	}
	if !strings.HasPrefix(hook.URL, "https://") && !strings.HasPrefix(hook.URL, "http://") {
		return errors.New("Webhook URL must start with http:// or https://")
	}
	if hook.Kind != WebhookKindDiscord {
		hook.Kind = WebhookKindGeneric
	}
	if hook.Events == nil {
		hook.Events = []string{}
	}
	return nil
}

func (s *Store) ListWebhooks(ctx context.Context) ([]Webhook, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+webhookColumns+` FROM webhooks ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hooks := []Webhook{}
	for rows.Next() {
		hook, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		hooks = append(hooks, hook)
	}
	return hooks, rows.Err()
}

func (s *Store) GetWebhook(ctx context.Context, id string) (Webhook, bool, error) {
	hook, err := scanWebhook(s.db.QueryRowContext(ctx, `SELECT `+webhookColumns+` FROM webhooks WHERE id = ?`, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return Webhook{}, false, nil
		}
		return Webhook{}, false, err
	}
	return hook, true, nil
}

func (s *Store) CreateWebhook(ctx context.Context, input Webhook) (Webhook, error) {
	hook := input
	if err := normalizeWebhook(&hook); err != nil {
		return Webhook{}, err
	}
	id, err := randomHex(8)
	if err != nil {
		return Webhook{}, err
	}
	hook.ID = "wh_" + id
	hook.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.ExecContext(ctx, `INSERT INTO webhooks (`+webhookColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		hook.ID, hook.Name, hook.URL, hook.Kind, strings.Join(hook.Events, ","), boolText(hook.Enabled), hook.CreatedAt)
	if err != nil {
		return Webhook{}, err
	}
	return hook, nil
}

func (s *Store) UpdateWebhook(ctx context.Context, id string, input Webhook) (Webhook, error) {
	current, ok, err := s.GetWebhook(ctx, id)
	if err != nil {
		return Webhook{}, err
	}
	if !ok {
		return Webhook{}, errors.New("Webhook not found")
	}
	next := input
	next.ID = current.ID
	next.CreatedAt = current.CreatedAt
	if err := normalizeWebhook(&next); err != nil {
		return Webhook{}, err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE webhooks SET name = ?, url = ?, kind = ?, events = ?, enabled = ? WHERE id = ?`,
		next.Name, next.URL, next.Kind, strings.Join(next.Events, ","), boolText(next.Enabled), id)
	if err != nil {
		return Webhook{}, err
	}
	return next, nil
}

func (s *Store) DeleteWebhook(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM webhooks WHERE id = ?`, id)
	return err
}
