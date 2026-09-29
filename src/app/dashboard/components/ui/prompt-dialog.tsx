"use client";

import React, { useState } from "react";
import { Modal } from "./modal";
import { Input } from "./input";

export interface PromptDialogProps {
  isOpen: boolean;
  title: string;
  description?: React.ReactNode;
  label: string;
  initialValue?: string;
  placeholder?: string;
  confirmLabel?: string;
  /** Return an error message to block submit, or "" when valid. */
  validate?: (value: string) => string;
  onSubmit: (value: string) => void | Promise<void>;
  onClose: () => void;
}

function PromptBody({ label, initialValue = "", placeholder, validate, onSubmit, onClose, title, description, confirmLabel }: Omit<PromptDialogProps, "isOpen">) {
  const [value, setValue] = useState(initialValue);
  const [busy, setBusy] = useState(false);
  const trimmed = value.trim();
  const error = trimmed ? validate?.(trimmed) ?? "" : "";
  const canSubmit = Boolean(trimmed) && !error && !busy;

  async function submit() {
    if (!canSubmit) return;
    setBusy(true);
    try {
      await onSubmit(trimmed);
      onClose();
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      isOpen
      onClose={onClose}
      title={title}
      description={description}
      confirmLabel={confirmLabel ?? "Save"}
      confirmDisabled={!canSubmit}
      onConfirm={submit}
      busy={busy}
    >
      <form
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <Input
          label={label}
          value={value}
          placeholder={placeholder}
          autoFocus
          aria-invalid={error ? true : undefined}
          onChange={(event) => setValue(event.target.value)}
        />
        {error && (
          <p className="field-error" role="alert">
            {error}
          </p>
        )}
      </form>
    </Modal>
  );
}

/** Replacement for window.prompt. State resets each time the dialog opens. */
export function PromptDialog({ isOpen, ...rest }: PromptDialogProps) {
  if (!isOpen) return null;
  return <PromptBody {...rest} />;
}
