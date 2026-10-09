package store

import (
	"context"
	"testing"
)

func TestWebhookCRUD(t *testing.T) {
	db := openTestStore(t)
	ctx := context.Background()
	if _, err := db.CreateWebhook(ctx, Webhook{Name: "bad", URL: "ftp://x"}); err == nil {
		t.Fatal("a non-http URL should be rejected")
	}
	hook, err := db.CreateWebhook(ctx, Webhook{Name: "Discord", URL: "https://example.test/hook", Kind: WebhookKindDiscord, Events: []string{"server.crashed"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	hook, err = db.UpdateWebhook(ctx, hook.ID, Webhook{Name: "Renamed", URL: hook.URL, Kind: "weird", Events: []string{"server.crashed", "backup.failed"}, Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := db.ListWebhooks(ctx)
	if err != nil || len(listed) != 1 || listed[0].Name != "Renamed" || listed[0].Kind != WebhookKindGeneric || listed[0].Enabled || len(listed[0].Events) != 2 {
		t.Fatalf("unexpected webhooks %+v (%v)", listed, err)
	}
	if err := db.DeleteWebhook(ctx, hook.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := db.GetWebhook(ctx, hook.ID); ok {
		t.Fatal("webhook should be gone")
	}
}
