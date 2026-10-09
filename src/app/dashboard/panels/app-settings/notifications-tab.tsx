"use client";

import { useCallback, useEffect, useState } from "react";
import { Pencil, Plus, Send, Trash2 } from "lucide-react";
import { createWebhook, deleteWebhook, fetchWebhooks, testWebhook, updateWebhook } from "../../lib/runtime-client";
import type { ConfirmRequest, Webhook, WebhookEventInfo } from "../../lib/types";
import { Banner, Button, Card, Checkbox, IconButton, Input, Modal, Pill, Select, SkeletonRows, Toggle } from "../../components/ui";

type Draft = { id: string; name: string; kind: Webhook["kind"]; url: string; events: string[]; enabled: boolean };

const NOISY = /join|leave/i;
const errorText = (error: unknown, fallback: string) => (error instanceof Error ? error.message : fallback);

/** Group events by the first word of their id (for example "server", "backup", "player"). */
function groupEvents(events: WebhookEventInfo[]) {
  const groups = new Map<string, WebhookEventInfo[]>();
  for (const event of events) {
    const key = event.id.split(/[._:-]/)[0] || "other";
    groups.set(key, [...(groups.get(key) ?? []), event]);
  }
  return [...groups.entries()];
}

function capitalize(word: string) {
  return word.charAt(0).toUpperCase() + word.slice(1);
}

export function NotificationsTab({ onMessage, onConfirm }: { onMessage: (message: string) => void; onConfirm: (request: ConfirmRequest) => void }) {
  const [webhooks, setWebhooks] = useState<Webhook[]>([]);
  const [events, setEvents] = useState<WebhookEventInfo[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [loadError, setLoadError] = useState("");
  const [draft, setDraft] = useState<Draft | null>(null);
  const [saving, setSaving] = useState(false);
  const [formError, setFormError] = useState("");
  const [testing, setTesting] = useState("");
  const [testResult, setTestResult] = useState<{ id: string; ok: boolean; text: string } | null>(null);

  const load = useCallback(async () => {
    try {
      const data = await fetchWebhooks();
      setWebhooks(data.webhooks ?? []);
      setEvents(data.events ?? []);
      setLoadError("");
    } catch (error) {
      setLoadError(errorText(error, "Could not load notifications"));
    } finally {
      setLoaded(true);
    }
  }, []);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load();
  }, [load]);

  function openNew() {
    setFormError("");
    setDraft({ id: "", name: "", kind: "discord", url: "", events: events.filter((e) => !NOISY.test(e.id)).map((e) => e.id), enabled: true });
  }

  function openEdit(hook: Webhook) {
    setFormError("");
    setDraft({ id: hook.id, name: hook.name, kind: hook.kind, url: hook.url, events: [...(hook.events ?? [])], enabled: hook.enabled });
  }

  async function save() {
    if (!draft) return;
    setSaving(true);
    setFormError("");
    const body = { name: draft.name.trim(), kind: draft.kind, url: draft.url.trim(), events: draft.events, enabled: draft.enabled };
    try {
      if (draft.id) await updateWebhook(draft.id, body);
      else await createWebhook(body);
      setDraft(null);
      onMessage(draft.id ? "Notification saved" : "Notification added");
      await load();
    } catch (error) {
      setFormError(errorText(error, "Could not save"));
    } finally {
      setSaving(false);
    }
  }

  async function toggle(hook: Webhook, enabled: boolean) {
    setWebhooks((current) => current.map((item) => (item.id === hook.id ? { ...item, enabled } : item)));
    try {
      await updateWebhook(hook.id, { enabled });
    } catch (error) {
      onMessage(errorText(error, "Could not change notification"));
      await load();
    }
  }

  async function sendTest(hook: Webhook) {
    setTesting(hook.id);
    setTestResult(null);
    try {
      await testWebhook(hook.id);
      setTestResult({ id: hook.id, ok: true, text: `Test sent to ${hook.name}.` });
    } catch (error) {
      setTestResult({ id: hook.id, ok: false, text: errorText(error, "Test failed") });
    } finally {
      setTesting("");
    }
  }

  function remove(hook: Webhook) {
    onConfirm({
      title: `Delete "${hook.name}"?`,
      message: "Cliff will stop sending notifications to this address.",
      confirmLabel: "Delete",
      dangerous: true,
      onConfirm: async () => {
        try {
          await deleteWebhook(hook.id);
          onMessage("Notification deleted");
          await load();
        } catch (error) {
          onMessage(errorText(error, "Could not delete"));
        }
      },
    });
  }

  function toggleEvent(id: string, on: boolean) {
    setDraft((current) =>
      current ? { ...current, events: on ? [...new Set([...current.events, id])] : current.events.filter((item) => item !== id) } : current,
    );
  }

  const urlValid = draft ? /^https?:\/\/\S+$/i.test(draft.url.trim()) : false;
  const canSave = Boolean(draft && draft.name.trim() && urlValid && draft.events.length > 0);

  return (
    <>
      <Card
        title="Notifications"
        description="Send a message to Discord or another service when something happens, such as a server crash or a finished backup."
        actions={
          <Button variant="primary" iconLeft={<Plus size={14} />} onClick={openNew} disabled={!loaded || Boolean(loadError)}>
            Add notification
          </Button>
        }
      >
        {!loaded ? (
          <SkeletonRows rows={3} label="Loading notifications" />
        ) : loadError ? (
          <Banner variant="danger" title="Could not load notifications" action={<Button onClick={() => void load()}>Try again</Button>}>
            {loadError}
          </Banner>
        ) : webhooks.length === 0 ? (
          <p className="accounts-empty">No notifications yet. Add one to hear about your servers without opening the dashboard.</p>
        ) : (
          <ul className="accounts-list">
            {webhooks.map((hook) => (
              <li key={hook.id} className="accounts-row">
                <div className="accounts-row-main">
                  <span className="accounts-row-name">{hook.name}</span>
                  <span className="accounts-row-meta">
                    <Pill>{hook.kind === "discord" ? "Discord" : "Generic"}</Pill>
                    <span>
                      {hook.events?.length ?? 0} {hook.events?.length === 1 ? "event" : "events"}
                    </span>
                  </span>
                  {testResult?.id === hook.id && (
                    <span className={testResult.ok ? "accounts-ok" : "accounts-fail"} role="status">
                      {testResult.text}
                    </span>
                  )}
                </div>
                <div className="accounts-row-actions">
                  <Toggle checked={hook.enabled} onChange={(on) => void toggle(hook, on)} aria-label={`${hook.name} enabled`} />
                  <Button size="sm" iconLeft={<Send size={14} />} loading={testing === hook.id} loadingText="Sending..." disabled={testing !== ""} onClick={() => void sendTest(hook)}>
                    Send test
                  </Button>
                  <IconButton aria-label={`Edit ${hook.name}`} onClick={() => openEdit(hook)}>
                    <Pencil size={14} />
                  </IconButton>
                  <IconButton aria-label={`Delete ${hook.name}`} variant="danger" onClick={() => remove(hook)}>
                    <Trash2 size={14} />
                  </IconButton>
                </div>
              </li>
            ))}
          </ul>
        )}
      </Card>

      <Modal
        isOpen={draft !== null}
        onClose={() => setDraft(null)}
        title={draft?.id ? "Edit notification" : "Add notification"}
        confirmLabel={draft?.id ? "Save" : "Add"}
        onConfirm={() => void save()}
        confirmDisabled={!canSave}
        busy={saving}
      >
        {draft && (
          <>
            <Input label="Name" value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} placeholder="Friends Discord" />
            <Select label="Type" value={draft.kind} onChange={(e) => setDraft({ ...draft, kind: e.target.value as Webhook["kind"] })}>
              <option value="discord">Discord</option>
              <option value="generic">Generic (JSON)</option>
            </Select>
            <Input
              label="Webhook URL"
              value={draft.url}
              onChange={(e) => setDraft({ ...draft, url: e.target.value })}
              placeholder="https://"
              autoComplete="off"
              error={draft.url && !urlValid ? "Enter a full address starting with https://" : ""}
            />
            <p className="accounts-hint">
              {draft.kind === "discord"
                ? "A Discord webhook URL lets Cliff post in one channel. Create one in Discord under Server Settings > Integrations > Webhooks."
                : "Cliff sends a JSON message with event, level, title, message, serverId, serverName and at."}
            </p>
            <fieldset className="accounts-fieldset">
              <legend>Send a message when</legend>
              {groupEvents(events).map(([group, items]) => (
                <div key={group} className="accounts-group">
                  <span className="accounts-group-title">{capitalize(group)}</span>
                  <div className="accounts-checks">
                    {items.map((event) => (
                      <Checkbox key={event.id} label={event.label} checked={draft.events.includes(event.id)} onChange={(on) => toggleEvent(event.id, on)} />
                    ))}
                  </div>
                </div>
              ))}
              {draft.events.length === 0 && <span className="accounts-fail">Pick at least one event.</span>}
            </fieldset>
            <Checkbox label="Enabled" checked={draft.enabled} onChange={(on) => setDraft({ ...draft, enabled: on })} />
            {formError && <Banner variant="danger">{formError}</Banner>}
          </>
        )}
      </Modal>
    </>
  );
}
