"use client";

import { useState } from "react";
import { Download, RefreshCw } from "lucide-react";
import { Modal } from "./ui/modal";
import { applyUpdate, reloadAfterDaemonRestart } from "../lib/runtime-client";
import type { UpdateCheckResult } from "../lib/types";

export function UpdateModal({
  update,
  isOpen,
  onClose,
  onMessage,
}: {
  update: UpdateCheckResult;
  isOpen: boolean;
  onClose: () => void;
  onMessage: (message: string) => void;
}) {
  const [applying, setApplying] = useState(false);
  const [waitingForRestart, setWaitingForRestart] = useState(false);
  const busy = applying || waitingForRestart;

  async function handleApply() {
    setApplying(true);
    try {
      const result = await applyUpdate();
      if (result.success) {
        setWaitingForRestart(Boolean(result.restarting));
        onMessage(result.restarting ? "Update applied. Waiting for Cliff to restart..." : result.message);
        if (result.restarting) {
          await reloadAfterDaemonRestart();
        } else {
          window.location.reload();
        }
      } else {
        onMessage(result.message || "Update failed");
      }
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Update failed");
    } finally {
      setApplying(false);
      setWaitingForRestart(false);
    }
  }

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title="Update available"
      description={
        <span>
          Cliff can install this update locally, restart the daemon, then refresh this page with the new dashboard files.
        </span>
      }
      confirmLabel={waitingForRestart ? "Restarting..." : applying ? "Updating..." : "Install now"}
      confirmVariant="primary"
      confirmDisabled={busy}
      confirmLoading={busy}
      onConfirm={handleApply}
      onCancel={onClose}
      cancelLabel="Later"
      busy={busy}
      form
    >
      <div className="update-modal-panel">
        <div className="update-version-card current">
          <span>Current</span>
          <strong>v{update.currentVersion}</strong>
        </div>
        <div className="update-version-card latest">
          <span>Available</span>
          <strong>v{update.latestVersion}</strong>
        </div>
      </div>
      <div className="update-modal-details">
        {update.archiveSize ? (
          <div className="update-modal-row">
            <Download size={15} />
            <span>Download</span>
            <strong>{formatSize(update.archiveSize)}</strong>
          </div>
        ) : null}
        {update.builtAt && (
          <div className="update-modal-row">
            <RefreshCw size={15} />
            <span>Released</span>
            <strong>{new Date(update.builtAt).toLocaleDateString("en-US", { year: "numeric", month: "short", day: "numeric" })}</strong>
          </div>
        )}
      </div>
      <p className="update-modal-hint muted">
        Running servers are stopped gracefully during the update. Server data, worlds, and settings are not changed.
      </p>
      {waitingForRestart && (
        <p className="update-modal-status">
          Waiting for the restarted daemon, then this page will refresh.
        </p>
      )}
      <div className="update-modal-later muted">
        App Settings &gt; Updates keeps this available later.
      </div>
    </Modal>
  );
}

function formatSize(bytes: number): string {
  if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${bytes} B`;
}
