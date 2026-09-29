"use client";

import { Fragment, useState } from "react";
import { Archive, Camera, ChevronDown, Download, FileText, RotateCcw, Settings, Trash2 } from "lucide-react";
import { formatBytes, formatDate, formatDateTime } from "../lib/utils";
import { backupUrl, fetchBackupDiff, runBackupAction, updateServerProfile } from "../lib/runtime-client";
import type { Backup, BackupChange, BackupDiff, ConfirmRequest, ServerRecord } from "../lib/types";
import { Button } from "../components/ui/button";
import { Panel } from "../components/ui/panel";
import { Modal } from "../components/ui/modal";
import { Input } from "../components/ui/input";
import { Hint } from "../components/ui/hint";
import { Table } from "../components/ui/table";
import { FilterBar } from "../components/ui/filter-bar";
import { SelectionBar } from "../components/ui/selection-bar";
import { ToggleRow } from "../components/ui/setting-row";

export function BackupsPanel({
  server,
  backups,
  isRunning,
  onRefresh,
  onMessage,
  onConfirm,
}: {
  server: ServerRecord;
  backups: Backup[];
  isRunning: boolean;
  onRefresh: () => void;
  onMessage: (message: string) => void;
  onConfirm: (request: ConfirmRequest) => void;
}) {
  function intervalParts(minutesValue: number) {
    const minutes = Math.max(1, minutesValue || 360);
    if (minutes % 1440 === 0) return { amount: String(minutes / 1440), unit: "days" as const };
    if (minutes % 60 === 0) return { amount: String(minutes / 60), unit: "hours" as const };
    return { amount: String(minutes), unit: "minutes" as const };
  }

  const [busyAction, setBusyAction] = useState("");
  const [backupQuery, setBackupQuery] = useState("");
  const [newBackupReason, setNewBackupReason] = useState("manual snapshot");
  const [showCreateSnapshot, setShowCreateSnapshot] = useState(false);
  const [showSettings, setShowSettings] = useState(false);
  const [selectedBackups, setSelectedBackups] = useState<string[]>([]);
  const [expandedBackup, setExpandedBackup] = useState("");
  const [diff, setDiff] = useState<BackupDiff | null>(null);
  const [diffLoading, setDiffLoading] = useState("");
  const [snapshotOverride, setSnapshotOverride] = useState<{ serverId: string; enabled: boolean } | null>(null);
  const [scheduleOverride, setScheduleOverride] = useState<{ serverId: string; enabled: boolean; interval: number } | null>(null);
  const [scheduleDraft, setScheduleDraft] = useState(() => ({ serverId: server.id, ...intervalParts(server.snapshotIntervalMinutes) }));

  const filteredBackups = backups
    .filter((backup) => {
      const q = backupQuery.trim().toLowerCase();
      return !q || backup.reason.toLowerCase().includes(q) || backup.id.toLowerCase().includes(q);
    })
    .toSorted((a, b) => new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime());

  const allFilteredSelected = filteredBackups.length > 0 && filteredBackups.every((backup) => selectedBackups.includes(backup.id));
  const snapshotsEnabled = snapshotOverride?.serverId === server.id ? snapshotOverride.enabled : server.snapshotsEnabled;
  const scheduledSnapshotsEnabled = scheduleOverride?.serverId === server.id ? scheduleOverride.enabled : server.scheduledSnapshotsEnabled;
  const snapshotIntervalMinutes = scheduleOverride?.serverId === server.id ? scheduleOverride.interval : server.snapshotIntervalMinutes;
  const currentScheduleDraft = scheduleDraft.serverId === server.id ? scheduleDraft : { serverId: server.id, ...intervalParts(snapshotIntervalMinutes) };
  const scheduleAmount = currentScheduleDraft.amount;
  const scheduleUnit = currentScheduleDraft.unit;

  function scheduleToMinutes(amountValue = scheduleAmount, unitValue = scheduleUnit) {
    const amount = Math.max(1, Math.floor(Number(amountValue) || 1));
    if (unitValue === "days") return amount * 1440;
    if (unitValue === "hours") return amount * 60;
    return amount;
  }

  async function action(body: Record<string, string | number | string[]>, label = "snapshot") {
    if (busyAction) return false;
    setBusyAction(label);
    try {
      await runBackupAction(server.id, body);
      await onRefresh();
      if (label === "delete-selected") setSelectedBackups([]);
      return true;
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Backup action failed");
      return false;
    } finally {
      setBusyAction("");
    }
  }

  async function createSnapshot() {
    const reason = newBackupReason.trim();
    if (!reason) return onMessage("Snapshot label is required");
    const ok = await action({ reason }, "create");
    if (ok) setShowCreateSnapshot(false);
  }

  function changeLabel(change: BackupChange) {
    const type = change.type === "added" ? "Added" : change.type === "removed" ? "Removed" : change.type === "modified" ? "Modified" : change.type;
    const category = change.category === "content" ? "content" : change.category === "config" ? "config" : change.category === "world" ? "world data" : "file";
    const name = change.displayName || change.path.split(/[\\/]/).pop() || change.path;
    if (change.oldVersion && change.newVersion && change.oldVersion !== change.newVersion) {
      return `${type} ${category}: ${name} ${change.oldVersion} -> ${change.newVersion}`;
    }
    if (change.version) return `${type} ${category}: ${name} ${change.version}`;
    return `${type} ${category}`;
  }

  function categoryClass(category: string) {
    if (category === "content") return "content";
    if (category === "config") return "config";
    if (category === "world") return "world";
    return "other";
  }

  async function openDiff(backupId: string, change: BackupChange) {
    if (diffLoading) return;
    setDiffLoading(`${backupId}:${change.path}`);
    try {
      setDiff(await fetchBackupDiff(server.id, backupId, change.path));
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Diff could not be loaded");
    } finally {
      setDiffLoading("");
    }
  }

  async function toggleAutoSnapshots(nextValue: boolean) {
    if (busyAction) return;
    setSnapshotOverride({ serverId: server.id, enabled: nextValue });
    setBusyAction("snapshots-toggle");
    try {
      await updateServerProfile(server.id, { snapshotsEnabled: nextValue });
      await onRefresh();
      onMessage(nextValue ? "Auto snapshots enabled" : "Auto snapshots disabled");
    } catch (error) {
      setSnapshotOverride({ serverId: server.id, enabled: !nextValue });
      onMessage(error instanceof Error ? error.message : "Snapshot setting failed");
    } finally {
      setBusyAction("");
    }
  }

  async function saveSchedule(enabled: boolean, interval: number) {
    if (busyAction) return;
    const nextInterval = Math.max(0, Math.floor(interval));
    setScheduleOverride({ serverId: server.id, enabled, interval: nextInterval });
    setBusyAction("schedule-toggle");
    try {
      await updateServerProfile(server.id, { scheduledSnapshotsEnabled: enabled, snapshotIntervalMinutes: nextInterval });
      await onRefresh();
      onMessage(enabled ? "Scheduled snapshots enabled" : "Scheduled snapshots disabled");
    } catch (error) {
      setScheduleOverride({ serverId: server.id, enabled: server.scheduledSnapshotsEnabled, interval: server.snapshotIntervalMinutes });
      onMessage(error instanceof Error ? error.message : "Schedule setting failed");
    } finally {
      setBusyAction("");
    }
  }

  const schedulePresets = [
    { label: "Every 30 min", minutes: 30 },
    { label: "Every hour", minutes: 60 },
    { label: "Every 6 hours", minutes: 360 },
    { label: "Every 12 hours", minutes: 720 },
    { label: "Every day", minutes: 1440 },
    { label: "Every week", minutes: 10080 },
  ];

  return (
    <Panel
      className="backups-list-panel"
      title="Snapshots"
      description="Point-in-time snapshots you can restore or export."
      icon={<Archive />}
    >
      <Modal
        isOpen={showCreateSnapshot}
        onClose={() => setShowCreateSnapshot(false)}
        title="Create snapshot"
        description="Save the current server files with a short label for this snapshot."
        confirmLabel="Create snapshot"
        confirmDisabled={!newBackupReason.trim() || Boolean(busyAction)}
        confirmLoading={busyAction === "create"}
        onConfirm={createSnapshot}
        busy={Boolean(busyAction)}
      >
        <Input
          label="Snapshot label"
          autoFocus
          placeholder="Snapshot label"
          value={newBackupReason}
          onChange={(event) => setNewBackupReason(event.target.value)}
          onKeyDown={(event) => { if (event.key === "Enter") createSnapshot(); }}
        />
      </Modal>

      <Modal
        isOpen={showSettings}
        onClose={() => setShowSettings(false)}
        title="Snapshot settings"
        cancelLabel="Close"
        description="Configure automatic and scheduled snapshots for this server."
        busy={Boolean(busyAction)}
      >
        <div className="snapshot-settings">
          <ToggleRow
            label="Auto snapshots"
            description="Create a snapshot before mod or datapack add/remove."
            checked={snapshotsEnabled}
            disabled={Boolean(busyAction)}
            onChange={toggleAutoSnapshots}
          />
          <ToggleRow
            label="Scheduled snapshots"
            description="Automatically create snapshots at a regular interval."
            checked={scheduledSnapshotsEnabled}
            disabled={Boolean(busyAction)}
            onChange={(checked) => saveSchedule(checked, snapshotIntervalMinutes || 360)}
          />

          {scheduledSnapshotsEnabled && (
            <div className="schedule-presets">
              <span className="schedule-presets-label">Interval</span>
              <div className="schedule-presets-grid">
                {schedulePresets.map((preset) => {
                  const presetMinutes = preset.minutes;
                  const currentMinutes = scheduleToMinutes();
                  const isActive = currentMinutes === presetMinutes;
                  return (
                    <button
                      key={preset.label}
                      type="button"
                      className={`schedule-preset ${isActive ? "active" : ""}`}
                      disabled={Boolean(busyAction)}
                      onClick={() => {
                        const parts = intervalParts(presetMinutes);
                        setScheduleDraft({ serverId: server.id, ...parts });
                        void saveSchedule(true, presetMinutes);
                      }}
                    >
                      {preset.label}
                    </button>
                  );
                })}
              </div>
              <div className="schedule-custom">
                <span>Custom:</span>
                <Input
                  type="number"
                  min={1}
                  value={scheduleAmount}
                  disabled={Boolean(busyAction)}
                  onChange={(event) => setScheduleDraft({ serverId: server.id, amount: event.target.value, unit: scheduleUnit })}
                  onBlur={() => { if (scheduledSnapshotsEnabled) void saveSchedule(true, scheduleToMinutes()); }}
                />
                <div className="schedule-unit-buttons">
                  {(["minutes", "hours", "days"] as const).map((unit) => (
                    <button
                      key={unit}
                      type="button"
                      className={`schedule-unit-button ${scheduleUnit === unit ? "active" : ""}`}
                      disabled={Boolean(busyAction)}
                      onClick={() => {
                        setScheduleDraft({ serverId: server.id, amount: scheduleAmount, unit });
                        if (scheduledSnapshotsEnabled) void saveSchedule(true, scheduleToMinutes(scheduleAmount, unit));
                      }}
                    >
                      {unit}
                    </button>
                  ))}
                </div>
              </div>
              <span className="schedule-last">{server.lastScheduledSnapshotAt ? `Last scheduled ${formatDate(server.lastScheduledSnapshotAt)}` : "No scheduled snapshot yet"}</span>
            </div>
          )}
        </div>
      </Modal>

      <Modal
        isOpen={Boolean(diff)}
        onClose={() => setDiff(null)}
        title={diff ? `Changes in ${diff.path}` : "Changes"}
        description={diff?.truncated ? "Large file diff truncated to the first 256 KB." : undefined}
        busy={Boolean(diffLoading)}
        form={false}
      >
        {diff && (
          <div className="backup-diff-view">
            {diff.lines.length > 0 ? diff.lines.map((line, index) => (
              <div key={`${index}-${line.type}`} className={`backup-diff-line ${line.type}`}>
                <span>{line.type === "added" ? "+" : line.type === "removed" ? "-" : " "}</span>
                <code>{line.text || " "}</code>
              </div>
            )) : <p className="muted">No text differences.</p>}
          </div>
        )}
      </Modal>

      {isRunning && <Hint warn>Stop the server before restoring or exporting. Snapshots can still be created while running.</Hint>}
      <FilterBar
        fields={[
          {
            key: "search",
            label: "Search snapshots",
            type: "text",
            placeholder: "Search snapshots",
            value: backupQuery,
            onChange: setBackupQuery,
          },
        ]}
        actions={
          <>
            <Button className="backups-settings-action" onClick={() => setShowSettings(true)}><Settings size={14} />Settings</Button>
            <div className="backups-action-pair">
              <Button disabled={Boolean(busyAction) || isRunning} onClick={() => window.open(backupUrl(server.id, "?current=1"), "_blank")} title={isRunning ? "Stop the server before downloading" : "Download the current server folder as a zip"}><Download size={14} />Download server</Button>
              <Button variant="primary" disabled={Boolean(busyAction)} onClick={() => setShowCreateSnapshot(true)}><Camera size={14} />Create snapshot</Button>
            </div>
          </>
        }
      />
      {selectedBackups.length > 0 && (
        <SelectionBar
          selectedCount={selectedBackups.length}
          actions={[
            {
              label: "Delete selected",
              variant: "danger",
              disabled: Boolean(busyAction),
              onClick: () => onConfirm({
                title: "Delete selected snapshots",
                message: `${selectedBackups.length} snapshot${selectedBackups.length === 1 ? "" : "s"} will be permanently removed.`,
                confirmLabel: "Delete selected",
                dangerous: true,
                onConfirm: async () => { await action({ action: "delete-selected", backupIds: selectedBackups }, "delete-selected"); },
              }),
            },
          ]}
        />
      )}
      <Table>
        <thead>
          <tr><th><Input type="checkbox" aria-label="Select all snapshots" checked={allFilteredSelected} onChange={(event) => setSelectedBackups(event.target.checked ? filteredBackups.map((backup) => backup.id) : [])} /></th><th>Created</th><th>Reason</th><th>Changes</th><th>Stored</th><th>Logical</th><th><span className="table-count">{filteredBackups.length} of {backups.length}</span></th></tr>
        </thead>
        <tbody>
          {filteredBackups.map((backup) => {
            const expanded = expandedBackup === backup.id;
            const changes = backup.changes ?? [];
            return (
              <Fragment key={backup.id}>
                <tr>
                  <td><Input type="checkbox" aria-label={`Select snapshot ${backup.id}`} checked={selectedBackups.includes(backup.id)} onChange={(event) => setSelectedBackups((current) => event.target.checked ? [...current, backup.id] : current.filter((id) => id !== backup.id))} /></td>
                  <td>
                    <div className="backup-date-cell">
                      <span>{formatDateTime(backup.createdAt)}</span>
                      <small className="muted">{backup.id}</small>
                    </div>
                  </td>
                  <td>{backup.reason}</td>
                  <td>
                    <button type="button" className="backup-summary-button" onClick={() => setExpandedBackup(expanded ? "" : backup.id)}>
                      <ChevronDown size={14} className={expanded ? "expanded" : ""} />
                      <span>{backup.summary || "Legacy snapshot"}</span>
                    </button>
                  </td>
                  <td>{formatBytes(backup.sizeBytes)}</td>
                  <td>{formatBytes(backup.logicalSizeBytes ?? backup.sizeBytes)}</td>
                  <td>
                    <div className="row-actions">
                      <Button disabled={Boolean(busyAction) || isRunning} onClick={() => window.open(backupUrl(server.id, `?download=${encodeURIComponent(backup.id)}`), "_blank")} title={isRunning ? "Stop the server before downloading" : "Download this revision"}><Download size={14} /></Button>
                      <Button disabled={Boolean(busyAction) || isRunning} onClick={() => onConfirm({
                        title: "Restore snapshot",
                        message: `Restore ${backup.reason}? Cliff will create a safety snapshot first, then replace the server folder with this revision.`,
                        confirmLabel: "Restore",
                        dangerous: true,
                        onConfirm: async () => { await action({ action: "restore", backupId: backup.id }, "restore"); },
                      })} title={isRunning ? "Stop the server before restoring" : "Restore this revision"}><RotateCcw size={14} /></Button>
                      <Button variant="danger" disabled={Boolean(busyAction)} onClick={() => onConfirm({
                        title: "Delete snapshot",
                        message: `${backup.reason} will be permanently removed.`,
                        confirmLabel: "Delete",
                        dangerous: true,
                        onConfirm: async () => { await action({ action: "delete", backupId: backup.id }, "delete"); },
                      })}><Trash2 size={14} /></Button>
                    </div>
                  </td>
                </tr>
                {expanded && (
                  <tr className="backup-details-row">
                    <td colSpan={7}>
                      <div className="backup-details">
                        <div className="backup-stats-grid">
                          <span><strong>{backup.stats?.filesAdded ?? 0}</strong> added</span>
                          <span><strong>{backup.stats?.filesModified ?? 0}</strong> modified</span>
                          <span><strong>{backup.stats?.filesRemoved ?? 0}</strong> removed</span>
                          <span><strong>{backup.stats?.filesUnchanged ?? 0}</strong> unchanged</span>
                        </div>
                        {changes.length > 0 ? (
                          <div className="backup-change-list">
                            {changes.map((change) => (
                              <div key={`${change.type}-${change.path}`} className="backup-change-item">
                                <span className={`backup-change-kind ${categoryClass(change.category)}`}>{changeLabel(change)}</span>
                                <code>{change.path}</code>
                                <span className="backup-change-actions">
                                  {change.category === "config" && (
                                    <Button
                                      disabled={Boolean(diffLoading)}
                                      loading={diffLoading === `${backup.id}:${change.path}`}
                                      onClick={() => openDiff(backup.id, change)}
                                      title="View config diff"
                                    >
                                      <FileText size={13} />Diff
                                    </Button>
                                  )}
                                  {typeof change.size === "number" && <small className="muted">{formatBytes(change.size)}</small>}
                                </span>
                              </div>
                            ))}
                          </div>
                        ) : (
                          <p className="muted">No changed files in this revision.</p>
                        )}
                      </div>
                    </td>
                  </tr>
                )}
              </Fragment>
            );
          })}
          {backups.length === 0 && <tr><td colSpan={7} className="muted">No snapshots yet.</td></tr>}
        </tbody>
      </Table>
    </Panel>
  );
}
