"use client";

import { ExternalLink, RefreshCw, ShieldCheck, Trash2 } from "lucide-react";
import { formatBytes } from "../../lib/utils";
import type { RunningServer, UpdateCheckResult, UpdateProgress, UpdateSafetyInfo } from "../../lib/types";
import { Banner, Button, Card, KeyValueList, StatusDot, type KeyValueItem } from "../../components/ui";
import { UpdateProgressList } from "../../components/update-progress";
import { stoppingNames } from "../../components/update-modal";

type FailedStage = Exclude<UpdateProgress["stage"], "" | "failed"> | null;

export function UpdatesTab({
  check,
  checking,
  installing,
  progress,
  installError,
  failedStage,
  runningServers,
  safety,
  clearing,
  onCheck,
  onInstall,
  onClearSafety,
}: {
  check: UpdateCheckResult | null;
  checking: boolean;
  installing: boolean;
  progress: UpdateProgress;
  installError: string;
  failedStage: FailedStage;
  runningServers: RunningServer[];
  safety: UpdateSafetyInfo | null;
  clearing: boolean;
  onCheck: () => void;
  onInstall: () => void;
  onClearSafety: () => void;
}) {
  const checkButton = (
    <Button iconLeft={<RefreshCw size={14} />} disabled={checking || installing} onClick={onCheck}>
      {checking ? "Checking..." : "Check for updates"}
    </Button>
  );

  return (
    <>
      <UpdateCard
        check={check}
        checkButton={checkButton}
        installing={installing}
        progress={progress}
        installError={installError}
        failedStage={failedStage}
        runningServers={runningServers}
        onInstall={onInstall}
      />
      <SafetyCard safety={safety} busy={clearing || installing} onClear={onClearSafety} />
    </>
  );
}

function UpdateCard({
  check,
  checkButton,
  installing,
  progress,
  installError,
  failedStage,
  runningServers,
  onInstall,
}: {
  check: UpdateCheckResult | null;
  checkButton: React.ReactNode;
  installing: boolean;
  progress: UpdateProgress;
  installError: string;
  failedStage: FailedStage;
  runningServers: RunningServer[];
  onInstall: () => void;
}) {
  if (!check) {
    return (
      <Card title="Cliff updates" description="Click Check for updates to see if a new version is available." actions={checkButton}>
        <p className="muted">No update check has run in this session.</p>
      </Card>
    );
  }

  if (check.error) {
    return (
      <Card title="Cliff updates" actions={checkButton}>
        <Banner variant="warning">Update check failed: {check.error}</Banner>
      </Card>
    );
  }

  const items: KeyValueItem[] = [
    { key: "current", label: "Current version", value: `v${check.currentVersion}`, mono: true },
    { key: "latest", label: "Latest version", value: `v${check.latestVersion}`, mono: true },
  ];
  if (check.builtAt) {
    items.push({
      key: "released",
      label: "Released",
      value: new Date(check.builtAt).toLocaleDateString("en-US", { year: "numeric", month: "short", day: "numeric" }),
    });
  }
  if (check.archiveSize) items.push({ key: "size", label: "Download size", value: formatBytes(check.archiveSize) });

  const showProgress = installing || Boolean(installError);

  return (
    <Card title="Cliff updates" actions={checkButton}>
      {check.updateAvailable ? (
        <Banner
          variant="info"
          title={`Version ${check.latestVersion} is available`}
          action={
            <Button variant="primary" disabled={installing} loading={installing} loadingText="Updating..." onClick={onInstall}>
              {installError ? "Try again" : runningServers.length > 0 ? "Stop server and update" : "Install update"}
            </Button>
          }
        >
          Before installing, Cliff copies its settings database and keeps the current version
          {check.safetyCopyBytes ? ` (about ${formatBytes(check.safetyCopyBytes)})`  : ""}. If the new version does not start, Cliff puts the
          old one back automatically.
        </Banner>
      ) : (
        <p className="update-status">
          <StatusDot tone="running" /> Cliff is up to date.
        </p>
      )}
      {check.updateAvailable && (
        runningServers.length > 0 ? (
          <Banner variant="warning" title={`${stoppingNames(runningServers)} will be stopped during the update`}>
            Its world is saved first, and Cliff starts it again afterwards. Players are disconnected for about a minute.
          </Banner>
        ) : (
          <p className="muted">No Minecraft server is running, so nothing will be interrupted.</p>
        )
      )}
      {showProgress && <UpdateProgressList progress={progress} error={installError} failedStage={failedStage} />}
      <KeyValueList items={items} />
      {check.releaseUrl && (
        <p className="update-release-link">
          <a href={check.releaseUrl} target="_blank" rel="noopener noreferrer">
            View release notes on GitHub <ExternalLink size={12} aria-hidden="true" />
          </a>
        </p>
      )}
    </Card>
  );
}

function SafetyCard({ safety, busy, onClear }: { safety: UpdateSafetyInfo | null; busy: boolean; onClear: () => void }) {
  const hasCopies = Boolean(safety && (safety.canRollback || safety.backupCount > 0));
  const items: KeyValueItem[] = safety
    ? [
        { key: "previous", label: "Previous version", value: safety.canRollback ? formatBytes(safety.previousVersionBytes) : "None kept" },
        {
          key: "db",
          label: "Database copies",
          value: safety.backupCount > 0 ? `${safety.backupCount} (${formatBytes(safety.backupBytes)})` : "None",
        },
        { key: "total", label: "Disk space used", value: formatBytes(safety.totalBytes) },
      ]
    : [];
  if (safety && safety.backupCount > 0) items.push({ key: "where", label: "Stored in", value: safety.backupDir, mono: true });

  return (
    <Card
      title="Update safety copies"
      description="Kept after an update so it can be undone. Nothing here holds your servers or worlds."
      actions={
        <Button
          variant="danger-ghost"
          iconLeft={<Trash2 size={14} />}
          disabled={busy || !hasCopies}
          onClick={onClear}
          title={hasCopies ? "Delete the previous version and the database copies" : "There is nothing to delete yet"}
        >
          Delete safety copies
        </Button>
      }
    >
      {!safety ? (
        <p className="muted">Checking what is kept...</p>
      ) : hasCopies ? (
        <>
          <p className="update-safety-lead">
            <ShieldCheck size={15} aria-hidden="true" /> The last update can be undone with <code>cliff rollback</code> in a terminal. Once you are
            happy with the new version you can delete these to free the space (or run <code>cliff cleanup</code>).
          </p>
          <KeyValueList items={items} />
        </>
      ) : (
        <p className="muted">
          Nothing is kept right now. After your next update Cliff keeps the previous version and a small copy of its database here, so the
          update can be undone.
        </p>
      )}
    </Card>
  );
}
