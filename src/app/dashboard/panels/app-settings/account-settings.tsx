"use client";

import { useEffect, useRef, useState } from "react";
import { saveAccount as saveAccountProfile } from "../../lib/runtime-client";
import type { UnsavedChangesRegistration, User } from "../../lib/types";
import { Card, Input, PasswordInput } from "../../components/ui";

const MIN_USERNAME = 3;
const MIN_PASSWORD = 10;

export function AccountSettings({
  user,
  onAccountSaved,
  onMessage,
  onUnsavedChange,
}: {
  user: User;
  onAccountSaved: (user: User) => void;
  onMessage: (message: string) => void;
  onUnsavedChange: (change: UnsavedChangesRegistration | null) => void;
}) {
  const [username, setUsername] = useState(user.username);
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [busy, setBusy] = useState(false);

  const trimmed = username.trim();
  const usernameChanged = trimmed !== user.username;
  const dirty = usernameChanged || Boolean(newPassword);

  const usernameError = usernameChanged && trimmed.length < MIN_USERNAME ? `Use at least ${MIN_USERNAME} characters.` : "";
  const newPasswordError = newPassword && newPassword.length < MIN_PASSWORD ? `Use at least ${MIN_PASSWORD} characters.` : "";
  const currentPasswordError = newPassword && !currentPassword ? "Enter your current password to set a new one." : "";
  const firstError = usernameError || newPasswordError || currentPasswordError;
  const canSave = dirty && !firstError && trimmed.length >= MIN_USERNAME && !busy;

  async function save() {
    if (!canSave) return false;
    setBusy(true);
    try {
      const data = await saveAccountProfile({ username: trimmed, currentPassword, newPassword });
      onAccountSaved(data.user);
      setCurrentPassword("");
      setNewPassword("");
      onMessage("Account saved");
      return true;
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Account save failed");
      return false;
    } finally {
      setBusy(false);
    }
  }

  function discard() {
    setUsername(user.username);
    setCurrentPassword("");
    setNewPassword("");
  }

  // Refs keep the registered callbacks pointing at the latest state without
  // re-registering on every keystroke.
  const saveRef = useRef(save);
  const discardRef = useRef(discard);
  useEffect(() => {
    saveRef.current = save;
    discardRef.current = discard;
  });

  useEffect(() => {
    onUnsavedChange(
      dirty
        ? {
            id: "account-settings",
            label: "Account settings",
            dirty: true,
            showSaveBar: true,
            canSave,
            saving: busy,
            disabledReason: firstError || undefined,
            saveLabel: "Save changes",
            onSave: async () => {
              if (!(await saveRef.current())) throw new Error("Account save failed");
            },
            onDiscard: () => discardRef.current(),
          }
        : null,
    );
    return () => onUnsavedChange(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dirty, canSave, busy, firstError]);

  return (
    <section className="account-settings" aria-label="Account settings">
      <Card title="Profile" description="The name you sign in with.">
        <Input
          label="Username"
          value={username}
          onChange={(event) => setUsername(event.target.value)}
          autoComplete="username"
          error={usernameError}
        />
      </Card>
      <Card title="Password" description="Leave these blank to keep your current password.">
        <PasswordInput
          label="Current password"
          value={currentPassword}
          onChange={(event) => setCurrentPassword(event.target.value)}
          autoComplete="current-password"
          error={currentPasswordError}
        />
        <PasswordInput
          label="New password"
          value={newPassword}
          onChange={(event) => setNewPassword(event.target.value)}
          autoComplete="new-password"
          error={newPasswordError}
        />
      </Card>
    </section>
  );
}
