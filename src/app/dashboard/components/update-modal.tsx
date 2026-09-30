"use client";

import { useEffect, useState } from "react";
import { AlertTriangle, Download, RefreshCw, ShieldCheck } from "lucide-react";
import { Modal } from "./ui/modal";
import { UpdateProgressList, useUpdateInstaller } from "./update-progress";
import { dismissLastUpdateResult, fetchUpdateSafety } from "../lib/runtime-client";
import type { LastUpdateResult, UpdateCheckResult, UpdateSafetyInfo } from "../lib/types";

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
  const installer = useUpdateInstaller(onMessage);
  const { refreshRunningServers } = installer;
  const [skipThisUpdate, setSkipThisUpdate] = useState(false);
  const stopping = installer.runningServers;

  useEffect(() => {
    if (isOpen) void refreshRunningServers();
  }, [isOpen, refreshRunningServers]);
  const busy = installer.installing;
  const started = installer.installing || Boolean(installer.error);

  function close() {
    if (skipThisUpdate) rememberSkippedUpdate(update.latestVersion);
    onClose();
  }

  return (
    <Modal
      isOpen={isOpen}
      onClose={close}
      title={started ? `Updating to v${update.latestVersion}` : "Update available"}
      description={
        started ? (
          <span>Cliff keeps working through each step. You can leave this window open.</span>
        ) : (
          <span>
            Cliff downloads the update, checks it, then restarts itself with the new dashboard files.
          </span>
        )
      }
      confirmLabel={installer.installing ? "Updating..." : installer.error ? "Try again" : stopping.length > 0 ? "Stop server and update" : "Install now"}
      confirmVariant="primary"
      confirmDisabled={busy}
      confirmLoading={busy}
      onConfirm={installer.install}
      onCancel={close}
      cancelLabel={installer.error ? "Close" : "Later"}
      busy={busy}
      form
    >
      {started ? (
        <UpdateProgressList progress={installer.progress} error={installer.error} failedStage={installer.failedStage} />
      ) : (
        <>
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
          <div className="update-safety-note">
            <ShieldCheck size={16} aria-hidden="true" />
            <div>
              <strong>Your data is safe, and the update can be undone.</strong>
              <p>
                Before installing, Cliff copies its own settings database (a few KB) and keeps the current version
                {update.safetyCopyBytes ? ` (about ${formatSize(update.safetyCopyBytes)} of disk)` : ""}. If the new version does not start,
                Cliff puts the old one back automatically. Your servers and worlds are never copied or changed. You can delete the
                kept copies in App Settings &gt; Updates.
              </p>
            </div>
          </div>
          {stopping.length > 0 ? (
            <div className="update-stop-warning" role="alert">
              <AlertTriangle size={16} aria-hidden="true" />
              <div>
                <strong>{stoppingNames(stopping)} will be stopped during the update</strong>
                <p>
                  Its world is saved first, and Cliff starts it again afterwards. Players are disconnected for about a minute.
                </p>
              </div>
            </div>
          ) : (
            <p className="update-modal-hint muted">No Minecraft server is running, so nothing will be interrupted.</p>
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
        </>
      )}
    </Modal>
  );
}

/** Tells the user how the last update ended, once, after Cliff is back. */
export function UpdateResultModal({ result, onDone }: { result: LastUpdateResult | null; onDone: () => void }) {
  const [safety, setSafety] = useState<UpdateSafetyInfo | null>(null);

  useEffect(() => {
    if (!result) return;
    let alive = true;
    fetchUpdateSafety().then((info) => { if (alive) setSafety(info); }).catch(() => undefined);
    return () => { alive = false; };
  }, [result]);

  if (!result) return null;

  function done() {
    dismissLastUpdateResult().catch(() => undefined);
    onDone();
  }

  const updated = result.status === "updated";
  const title = updated
    ? `Cliff is now v${result.to}`
    : result.status === "rolled-back"
      ? `The update did not work. Cliff is back on v${result.from}`
      : "The update did not finish";

  return (
    <Modal isOpen onClose={done} title={title} cancelLabel="Got it" description={<span>{result.message}</span>} form>
      {updated && safety && safety.canRollback && (
        <div className="update-safety-note">
          <ShieldCheck size={16} aria-hidden="true" />
          <div>
            <strong>Kept so you can undo this update</strong>
            <p>
              The previous version ({formatSize(safety.previousVersionBytes)}) and {safety.backupCount} database
              {safety.backupCount === 1 ? " copy" : " copies"} ({formatSize(safety.backupBytes)}). Once you are happy with the new
              version, delete them in App Settings &gt; Updates to free the space.
            </p>
          </div>
        </div>
      )}
      {!updated && (
        <p className="update-modal-hint muted">
          You can try the update again later from App Settings &gt; Updates.
        </p>
      )}
    </Modal>
  );
}

/** "Survival World", "A and B", or "A, B and C". */
export function stoppingNames(servers: { name: string }[]): string {
  const names = servers.map((server) => `"${server.name}"`);
  if (names.length <= 1) return names[0] ?? "A server";
  return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}

export function formatSize(bytes: number): string {
  if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${bytes} B`;
}
