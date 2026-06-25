"use client";

import { useState } from "react";
import type { ConfirmRequest } from "../lib/types";
import { Modal } from "./ui/modal";

export function ConfirmDialog({ request, onClose }: { request: ConfirmRequest | null; onClose: () => void }) {
  const [busy, setBusy] = useState(false);
  if (!request) return null;

  async function confirm() {
    if (!request) return;
    setBusy(true);
    try {
      await request.onConfirm();
      onClose();
    } finally {
      setBusy(false);
    }
  }

  async function cancel() {
    if (!request) return;
    if (!request.onCancel) {
      onClose();
      return;
    }
    setBusy(true);
    try {
      await request.onCancel();
      onClose();
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      isOpen={Boolean(request)}
      onClose={onClose}
      title={request.title}
      description={request.message}
      cancelLabel={request.cancelLabel ?? "Cancel"}
      confirmLabel={request.confirmLabel}
      confirmVariant={request.dangerous ? "danger" : "primary"}
      confirmDisabled={request.confirmDisabled}
      onConfirm={confirm}
      onCancel={cancel}
      busy={busy}
      form={false}
      rolePresentationClick={!request.disableBackdropCancel}
    />
  );
}
