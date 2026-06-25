"use client";

import { useState } from "react";
import { Archive, Camera, Download, Settings } from "lucide-react";
import { formatBytes, formatDate, formatDateTime } from "../lib/utils";
import { backupUrl, runBackupAction, updateServerProfile } from "../lib/runtime-client";
import type { Backup, ConfirmRequest, ServerRecord } from "../lib/types";
import { Button } from "../components/ui/button";
import { Panel } from "../components/ui/panel";
import { Modal } from "../components/ui/modal";
import { Toggle } from "../components/ui/toggle";
import { Input } from "../components/ui/input";
import { Hint } from "../components/ui/hint";
import { Table } from "../components/ui/table";
import { FilterBar } from "../components/ui/filter-bar";
import { SelectionBar } from "../components/ui/selection-bar";

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
        description="Configure automatic and scheduled snapshots for this server."
        busy={Boolean(busyAction)}
      >
        <div className="snapshot-settings">
          <div className="snapshot-setting-row">
            <div className="snapshot-setting-info">
              <span className="snapshot-setting-label">Auto snapshots</span>
              <span className="snapshot-setting-desc">Create a snapshot before mod or datapack add/remove.</span>
            </div>
            <Toggle
              checked={snapshotsEnabled}
              disabled={Boolean(busyAction)}
              onChange={toggleAutoSnapshots}
              aria-label="Toggle auto snapshots"
            />
          </div>

          <div className="snapshot-setting-row">
            <div className="snapshot-setting-info">
              <span className="snapshot-setting-label">Scheduled snapshots</span>
              <span className="snapshot-setting-desc">Automatically create snapshots at a regular interval.</span>
            </div>
            <Toggle
              checked={scheduledSnapshotsEnabled}
              disabled={Boolean(busyAction)}
              onChange={(checked) => saveSchedule(checked, snapshotIntervalMinutes || 360)}
              aria-label="Toggle scheduled snapshots"
            />
          </div>

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
          <tr><th><Input type="checkbox" aria-label="Select all snapshots" checked={allFilteredSelected} onChange={(event) => setSelectedBackups(event.target.checked ? filteredBackups.map((backup) => backup.id) : [])} /></th><th>Created</th><th>ID</th><th>Reason</th><th>Size</th><th><span className="table-count">{filteredBackups.length} of {backups.length}</span></th></tr>
        </thead>
        <tbody>
          {filteredBackups.map((backup) => (
            <tr key={backup.id}>
              <td><Input type="checkbox" aria-label={`Select snapshot ${backup.id}`} checked={selectedBackups.includes(backup.id)} onChange={(event) => setSelectedBackups((current) => event.target.checked ? [...current, backup.id] : current.filter((id) => id !== backup.id))} /></td>
              <td>{formatDateTime(backup.createdAt)}</td>
              <td><small className="muted">{backup.id}</small></td>
              <td>{backup.reason}</td>
              <td>{formatBytes(backup.sizeBytes)}</td>
              <td></td>
            </tr>
          ))}
          {backups.length === 0 && <tr><td colSpan={6} className="muted">No snapshots yet.</td></tr>}
        </tbody>
      </Table>
    </Panel>
  );
}
