"use client";

import { useState } from "react";
import { Download, RefreshCw } from "lucide-react";
import { Modal } from "./ui/modal";
import { applyUpdate, reloadAfterDaemonRestart } from "../lib/runtime-client";
import type { UpdateCheckResult } from "../lib/types";

const skippedUpdateKey = "cliff.skippedUpdateVersion";

/** The version the user chose to skip, so the pop-up stays quiet until a newer one ships. */
export function skippedUpdateVersion(): string {
  try {
    return window.localStorage.getItem(skippedUpdateKey) ?? "";
  } catch {
    return "";
  }
}

function rememberSkippedUpdate(version: string) {
  try {
    window.localStorage.setItem(skippedUpdateKey, version);
  } catch {
    // Storage can be blocked; the pop-up then simply comes back next visit.
  }
}

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
  const [skipThisUpdate, setSkipThisUpdate] = useState(false);
  const busy = applying || waitingForRestart;

  function close() {
    if (skipThisUpdate) rememberSkippedUpdate(update.latestVersion);
    onClose();
  }

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
      onClose={close}
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
      onCancel={close}
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
        Running servers are stopped gracefully during the update. Server data, worlds, and settings are not changed. Cliff keeps a copy of its database and the previous version, so &quot;cliff rollback&quot; can undo the update.
      </p>
      {waitingForRestart && (
        <p className="update-modal-status">
          Waiting for the restarted daemon, then this page will refresh.
        </p>
      )}
      <label className="update-skip">
        <input
          type="checkbox"
          checked={skipThisUpdate}
          disabled={busy}
          onChange={(event) => setSkipThisUpdate(event.target.checked)}
        />
        <span>
          Skip v{update.latestVersion}
          <small>Don&apos;t show this pop-up again until the next version. You can still install it from App Settings &gt; Updates.</small>
        </span>
      </label>
    </Modal>
  );
}

function formatSize(bytes: number): string {
  if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${bytes} B`;
}
