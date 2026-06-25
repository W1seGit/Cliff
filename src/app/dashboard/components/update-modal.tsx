"use client";

import { useState } from "react";
import { Download, RefreshCw } from "lucide-react";
import { Modal } from "./ui/modal";
import { Button } from "./ui/button";
import { applyUpdate } from "../lib/runtime-client";
import type { UpdateCheckResult } from "../lib/types";

export function UpdateModal({
  update,
  isOpen,
  onClose,
  onMessage,
  onApplied,
}: {
  update: UpdateCheckResult;
  isOpen: boolean;
  onClose: () => void;
  onMessage: (message: string) => void;
  onApplied?: () => void;
}) {
  const [applying, setApplying] = useState(false);

  async function handleApply() {
    setApplying(true);
    try {
      const result = await applyUpdate();
      if (result.success) {
        onMessage(result.message);
        onApplied?.();
        onClose();
      } else {
        onMessage(result.message || "Update failed");
      }
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Update failed");
    } finally {
      setApplying(false);
    }
  }

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title="Update available"
      description={
        <span>
          A new version of Cliff is available. You are running{" "}
          <strong>v{update.currentVersion}</strong> and version{" "}
          <strong>v{update.latestVersion}</strong> is ready to install.
          {update.archiveSize ? ` Download size: ${formatSize(update.archiveSize)}.` : ""}
          {" "}The daemon will restart after the update is applied."
        </span>
      }
      confirmLabel={applying ? "Updating..." : "Install now"}
      confirmVariant="primary"
      confirmDisabled={applying}
      confirmLoading={applying}
      onConfirm={handleApply}
      onCancel={onClose}
      cancelLabel="Later"
      busy={applying}
      form
    >
      <div className="update-modal-info">
        <div className="update-modal-row">
          <span className="muted">Current version</span>
          <strong>v{update.currentVersion}</strong>
        </div>
        <div className="update-modal-row">
          <span className="muted">New version</span>
          <strong>v{update.latestVersion}</strong>
        </div>
        {update.builtAt && (
          <div className="update-modal-row">
            <span className="muted">Released</span>
            <strong>{new Date(update.builtAt).toLocaleDateString("en-US", { year: "numeric", month: "short", day: "numeric" })}</strong>
          </div>
        )}
        <p className="update-modal-hint muted">
          You can always install this update later from <strong>App Settings &rarr; Updates</strong>.
        </p>
      </div>
    </Modal>
  );
}

function formatSize(bytes: number): string {
  if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${bytes} B`;
}
