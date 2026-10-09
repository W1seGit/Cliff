"use client";

import { useCallback, useEffect, useState } from "react";
import { Pencil, Plus, ShieldOff, Trash2 } from "lucide-react";
import { createUser, deleteUser, fetchRuntimeDashboard, fetchUsers, resetUserTwoFactor, updateUser } from "../../lib/runtime-client";
import type { Account, ConfirmRequest, PermissionGrants, User, UserRole } from "../../lib/types";
import { Banner, Button, Card, Checkbox, IconButton, Input, Modal, PasswordInput, Pill, Select, SkeletonRows } from "../../components/ui";

const MIN_USERNAME = 3;
const MIN_PASSWORD = 10;
const ALL = "*";

const PERMISSION_LABELS: Record<string, string> = {
  view: "See the server",
  console: "Use the console",
  power: "Start, stop and restart",
  files: "Edit files",
  mods: "Manage mods",
  players: "Manage players",
  worlds: "Manage worlds",
  backups: "Manage backups",
  settings: "Change settings",
};
const FALLBACK_PERMISSIONS = Object.keys(PERMISSION_LABELS);

type Draft = { id: string; username: string; role: UserRole; password: string; grants: PermissionGrants; totpEnabled: boolean };

const errorText = (error: unknown, fallback: string) => (error instanceof Error ? error.message : fallback);

/** Drop empty rows, and make sure any granted row also includes the implied "view". */
function cleanGrants(grants: PermissionGrants): PermissionGrants {
  const out: PermissionGrants = {};
  for (const [key, list] of Object.entries(grants)) {
    if (list.length === 0) continue;
    out[key] = list.includes("view") ? list : ["view", ...list];
  }
  return out;
}

function PermissionRow({
  name,
  permissions,
  granted,
  onChange,
}: {
  name: string;
  permissions: string[];
  granted: string[];
  onChange: (next: string[]) => void;
}) {
  const full = permissions.every((permission) => granted.includes(permission));
  function set(permission: string, on: boolean) {
    let next = on ? [...new Set([...granted, permission])] : granted.filter((item) => item !== permission);
    if (permission === "view" && !on) next = [];
    if (permission !== "view" && on && !next.includes("view")) next = ["view", ...next];
    onChange(next);
  }
  return (
    <fieldset className="accounts-perm-row">
      <legend>{name}</legend>
      <div className="accounts-checks">
        <Checkbox label={<strong>Full access</strong>} checked={full} onChange={(on) => onChange(on ? [...permissions] : [])} />
        {permissions.map((permission) => (
          <Checkbox key={permission} label={PERMISSION_LABELS[permission] ?? permission} checked={granted.includes(permission)} onChange={(on) => set(permission, on)} />
        ))}
      </div>
    </fieldset>
  );
}

export function UsersTab({
  currentUser,
  onMessage,
  onConfirm,
}: {
  currentUser: User;
  onMessage: (message: string) => void;
  onConfirm: (request: ConfirmRequest) => void;
}) {
  const [users, setUsers] = useState<Account[]>([]);
  const [permissions, setPermissions] = useState<string[]>(FALLBACK_PERMISSIONS);
  const [servers, setServers] = useState<{ id: string; name: string }[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [loadError, setLoadError] = useState("");
  const [draft, setDraft] = useState<Draft | null>(null);
  const [saving, setSaving] = useState(false);
  const [formError, setFormError] = useState("");
  const [listError, setListError] = useState("");

  const load = useCallback(async () => {
    try {
      const [data, dashboard] = await Promise.all([fetchUsers(), fetchRuntimeDashboard().catch(() => null)]);
      setUsers(data.users ?? []);
      if (data.permissions?.length) setPermissions(data.permissions);
      setServers((dashboard?.servers ?? []).map((server) => ({ id: server.id, name: server.name })));
      setLoadError("");
    } catch (error) {
      setLoadError(errorText(error, "Could not load users"));
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
    setDraft({ id: "", username: "", role: "member", password: "", grants: {}, totpEnabled: false });
  }

  function openEdit(account: Account) {
    setFormError("");
    setDraft({ id: account.id, username: account.username, role: account.role, password: "", grants: structuredClone(account.permissions ?? {}), totpEnabled: account.totpEnabled });
  }

  const isNew = draft?.id === "";
  const usernameError = draft && isNew && draft.username && draft.username.trim().length < MIN_USERNAME ? `Use at least ${MIN_USERNAME} characters.` : "";
  const passwordError = draft && draft.password && draft.password.length < MIN_PASSWORD ? `Use at least ${MIN_PASSWORD} characters.` : "";
  const canSave = Boolean(
    draft && !usernameError && !passwordError && (isNew ? draft.username.trim().length >= MIN_USERNAME && draft.password.length >= MIN_PASSWORD : true),
  );

  async function save() {
    if (!draft) return;
    setSaving(true);
    setFormError("");
    const grants = draft.role === "admin" ? {} : cleanGrants(draft.grants);
    try {
      if (draft.id) {
        await updateUser(draft.id, { role: draft.role, permissions: grants, ...(draft.password ? { password: draft.password } : {}) });
      } else {
        await createUser({ username: draft.username.trim(), password: draft.password, role: draft.role, permissions: grants });
      }
      onMessage(draft.id ? "User saved" : "User created");
      setDraft(null);
      await load();
    } catch (error) {
      setFormError(errorText(error, "Could not save the user"));
    } finally {
      setSaving(false);
    }
  }

  function remove(account: Account) {
    onConfirm({
      title: `Delete ${account.username}?`,
      message: "They will be signed out and will not be able to sign in again. Their servers are not touched.",
      confirmLabel: "Delete user",
      dangerous: true,
      onConfirm: async () => {
        try {
          await deleteUser(account.id);
          setListError("");
          onMessage("User deleted");
          await load();
        } catch (error) {
          const text = errorText(error, "Could not delete the user");
          setListError(text);
          onMessage(text);
        }
      },
    });
  }

  function resetTwoFactor(account: Account) {
    onConfirm({
      title: `Turn off two-factor for ${account.username}?`,
      message: "Use this when they lost their authenticator app and recovery codes. They can sign in with just their password and set it up again.",
      confirmLabel: "Turn off two-factor",
      dangerous: true,
      onConfirm: async () => {
        try {
          await resetUserTwoFactor(account.id);
          onMessage("Two-factor turned off");
          await load();
        } catch (error) {
          const text = errorText(error, "Could not turn off two-factor");
          setListError(text);
          onMessage(text);
        }
      },
    });
  }

  function setRow(key: string, next: string[]) {
    setDraft((current) => (current ? { ...current, grants: { ...current.grants, [key]: next } } : current));
  }

  return (
    <>
      <Card
        title="Users"
        description="People who can sign in to this dashboard. Admins can do everything; members only get what you grant them."
        actions={
          <Button variant="primary" iconLeft={<Plus size={14} />} onClick={openNew} disabled={!loaded || Boolean(loadError)}>
            Add user
          </Button>
        }
      >
        {listError && <Banner variant="danger">{listError}</Banner>}
        {!loaded ? (
          <SkeletonRows rows={3} label="Loading users" />
        ) : loadError ? (
          <Banner variant="danger" title="Could not load users" action={<Button onClick={() => void load()}>Try again</Button>}>
            {loadError}
          </Banner>
        ) : (
          <ul className="accounts-list">
            {users.map((account) => (
              <li key={account.id} className="accounts-row">
                <div className="accounts-row-main">
                  <span className="accounts-row-name">
                    {account.username}
                    {account.id === currentUser.id && <span className="accounts-you"> (you)</span>}
                  </span>
                  <span className="accounts-row-meta">
                    <Pill variant={account.role === "admin" ? "accent" : "default"}>{account.role === "admin" ? "Admin" : "Member"}</Pill>
                    <span>{account.totpEnabled ? "Two-factor on" : "Two-factor off"}</span>
                  </span>
                </div>
                <div className="accounts-row-actions">
                  {account.totpEnabled && (
                    <Button size="sm" iconLeft={<ShieldOff size={14} />} onClick={() => resetTwoFactor(account)}>
                      Turn off two-factor
                    </Button>
                  )}
                  <IconButton aria-label={`Edit ${account.username}`} onClick={() => openEdit(account)}>
                    <Pencil size={14} />
                  </IconButton>
                  <IconButton aria-label={`Delete ${account.username}`} variant="danger" onClick={() => remove(account)}>
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
        title={isNew ? "Add user" : `Edit ${draft?.username ?? "user"}`}
        confirmLabel={isNew ? "Create user" : "Save"}
        onConfirm={() => void save()}
        confirmDisabled={!canSave}
        busy={saving}
      >
        {draft && (
          <>
            {isNew && (
              <Input label="Username" value={draft.username} onChange={(e) => setDraft({ ...draft, username: e.target.value })} autoComplete="off" error={usernameError} />
            )}
            <PasswordInput
              label={isNew ? "Password" : "Reset password (optional)"}
              value={draft.password}
              onChange={(e) => setDraft({ ...draft, password: e.target.value })}
              autoComplete="new-password"
              error={passwordError}
            />
            <Select label="Role" value={draft.role} onChange={(e) => setDraft({ ...draft, role: e.target.value as UserRole })}>
              <option value="member">Member (only what you grant)</option>
              <option value="admin">Admin (everything)</option>
            </Select>
            {draft.role === "admin" ? (
              <p className="accounts-hint">Admins can do everything on every server, including managing users, so there is nothing to pick here.</p>
            ) : (
              <div className="accounts-perms">
                <p className="accounts-hint">Choose what this person can do. With nothing ticked they cannot see a server at all.</p>
                <PermissionRow name="All servers (including ones added later)" permissions={permissions} granted={draft.grants[ALL] ?? []} onChange={(next) => setRow(ALL, next)} />
                {servers.map((server) => (
                  <PermissionRow key={server.id} name={server.name} permissions={permissions} granted={draft.grants[server.id] ?? []} onChange={(next) => setRow(server.id, next)} />
                ))}
              </div>
            )}
            {formError && <Banner variant="danger">{formError}</Banner>}
          </>
        )}
      </Modal>
    </>
  );
}
