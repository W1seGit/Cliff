"use client";

import { useState } from "react";
import { ArrowUpCircle, Loader2 } from "lucide-react";
import { applyServerUpgrade, checkServerUpgrade } from "../../lib/runtime-client";
import { serverTypeNeedsLoader } from "../../lib/utils";
import type { MinecraftMetadata, ModUpdateInfo, ModUpdateStatus, ServerRecord, UpgradeReport, UpgradeResult } from "../../lib/types";
import { Banner, Button, Card, Checkbox, Disclosure, KeyValueList, Modal, Pill, Table } from "../../components/ui";
import { VersionSelect } from "../../components/version-select";
import { LoaderSelect } from "../../components/loader-select";

const groupOrder: { status: ModUpdateStatus; title: string; note: string }[] = [
  { status: "update", title: "Newer file available", note: "These mods have a version made for the new Minecraft version." },
  { status: "incompatible", title: "No file for the new version", note: "These mods have no release for the new Minecraft version yet." },
  { status: "unknown", title: "Not on Modrinth", note: "Cliff cannot check these. They stay as they are, so test them after the upgrade." },
  { status: "current", title: "Already up to date", note: "These need no change." },
];

function ModGroup({ status, title, note, mods }: { status: ModUpdateStatus; title: string; note: string; mods: ModUpdateInfo[] }) {
  if (mods.length === 0) return null;
  const table = (
    <Table aria-label={title}>
      <thead>
        <tr>
          <th scope="col">Mod</th>
          <th scope="col">{status === "update" ? "Now / after" : "File"}</th>
        </tr>
      </thead>
      <tbody>
        {mods.map((mod) => (
          <tr key={mod.fileName}>
            <td>
              {mod.title || mod.fileName}
              {!mod.enabled && <> <Pill>Off</Pill></>}
            </td>
            <td className="mono">
              {status === "update" ? `${mod.currentVersion || mod.fileName} → ${mod.latestVersionNumber || mod.latestFileName || "newer"}` : mod.fileName}
            </td>
          </tr>
        ))}
      </tbody>
    </Table>
  );
  if (status === "current") {
    return <Disclosure title={`${title} (${mods.length})`} description={note}>{table}</Disclosure>;
  }
  return (
    <section className="upgrade-group">
      <h3 className="upgrade-group-title">{title} <Pill>{mods.length}</Pill></h3>
      <p className="upgrade-group-note">{note}</p>
      {table}
    </section>
  );
}

function UpgradeWizard({
  server,
  metadata,
  metadataError,
  onClose,
  onUpgraded,
}: {
  server: ServerRecord;
  metadata: MinecraftMetadata | null;
  metadataError: string;
  onClose: () => void;
  onUpgraded: (server: ServerRecord) => void | Promise<void>;
}) {
  const needsLoader = serverTypeNeedsLoader(server.type);
  const [version, setVersion] = useState(server.minecraftVersion);
  const [loader, setLoader] = useState("");
  const [report, setReport] = useState<UpgradeReport | null>(null);
  const [checking, setChecking] = useState(false);
  const [applying, setApplying] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<UpgradeResult | null>(null);
  const [updateMods, setUpdateMods] = useState(true);
  const [disableIncompatible, setDisableIncompatible] = useState(true);
  const [confirmDowngrade, setConfirmDowngrade] = useState(false);

  const sameVersion = version === server.minecraftVersion && (!needsLoader || !loader || loader === server.loaderVersion);
  const canCheck = Boolean(version) && !sameVersion && !checking && !applying;
  const updateCount = report?.counts.update ?? 0;
  const incompatibleCount = report?.counts.incompatible ?? 0;

  function resetReport() {
    setReport(null);
    setError("");
    setConfirmDowngrade(false);
  }

  async function check() {
    setChecking(true);
    setError("");
    try {
      const next = await checkServerUpgrade(server.id, { minecraftVersion: version, loaderVersion: needsLoader && loader ? loader : undefined });
      setReport(next);
      setUpdateMods((next.counts.update ?? 0) > 0);
      setDisableIncompatible((next.counts.incompatible ?? 0) > 0);
      setConfirmDowngrade(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not check this upgrade");
    } finally {
      setChecking(false);
    }
  }

  async function apply() {
    if (!report) return;
    setApplying(true);
    setError("");
    try {
      const done = await applyServerUpgrade(server.id, {
        minecraftVersion: report.targetVersion,
        loaderVersion: needsLoader && loader ? loader : undefined,
        updateMods: updateMods && updateCount > 0,
        disableIncompatible: disableIncompatible && incompatibleCount > 0,
        allowDowngrade: report.downgrade && confirmDowngrade,
      });
      setResult(done);
      await onUpgraded(done.server);
    } catch (err) {
      setError(err instanceof Error ? err.message : "The upgrade failed");
    } finally {
      setApplying(false);
    }
  }

  const loaderLabel = server.type.charAt(0).toUpperCase() + server.type.slice(1);
  const confirmLabel = result ? undefined : report ? "Upgrade" : "Check";
  const confirmDisabled = result ? true : report ? report.downgrade && !confirmDowngrade : !canCheck;

  return (
    <Modal
      isOpen
      onClose={onClose}
      title="Upgrade Minecraft version"
      description={result ? undefined : "Pick the version to move to. Cliff checks your mods first and changes nothing until you press Upgrade."}
      confirmLabel={confirmLabel}
      confirmDisabled={confirmDisabled}
      confirmLoading={checking || applying}
      onConfirm={result ? undefined : report ? apply : check}
      cancelLabel={result ? "Close" : "Cancel"}
      busy={checking || applying}
    >
      <div className="upgrade-wizard">
        {result ? (
          <>
            <Banner variant={result.mods.failed.length > 0 || result.warnings.length > 0 ? "warning" : "info"} title={`Upgraded to ${result.server.minecraftVersion}`}>
              Start the server when you are ready. If something goes wrong, restore the safety snapshot from the Backups tab.
            </Banner>
            <KeyValueList
              items={[
                { key: "version", label: "Minecraft version", value: result.server.minecraftVersion, mono: true },
                { key: "snapshot", label: "Safety snapshot", value: result.snapshotId || "None", mono: true },
                { key: "updated", label: "Mods updated", value: String(result.mods.updated.length), note: result.mods.updated.join(", ") || undefined },
                { key: "disabled", label: "Mods turned off", value: String(result.mods.disabled.length), note: result.mods.disabled.join(", ") || undefined },
                { key: "failed", label: "Mods that failed", value: String(result.mods.failed.length), note: result.mods.failed.join(", ") || undefined },
              ]}
            />
            {result.warnings.length > 0 && (
              <>
                <Banner variant="warning" title="Things to check" />
                <ul className="upgrade-list">{result.warnings.map((warning) => <li key={warning}>{warning}</li>)}</ul>
              </>
            )}
          </>
        ) : applying ? (
          <div className="upgrade-progress" role="status" aria-live="polite">
            <Loader2 className="upgrade-spinner" size={20} aria-hidden="true" />
            <span>Upgrading. Cliff is taking a safety snapshot, then downloading the new server and updating mods. This can take several minutes, so please keep this window open.</span>
          </div>
        ) : (
          <>
            <div className="field-grid cols-1 gap-md">
              <label>
                Upgrade to
                <VersionSelect
                  value={version}
                  serverType={server.type}
                  metadata={metadata}
                  metadataError={metadataError}
                  onChange={(value) => { setVersion(value); setLoader(""); resetReport(); }}
                />
              </label>
              {needsLoader && (
                <label>
                  {loaderLabel} version
                  <LoaderSelect
                    type={server.type}
                    minecraftVersion={version}
                    metadata={metadata}
                    metadataError={metadataError}
                    value={loader}
                    onChange={(value) => { setLoader(value); resetReport(); }}
                  />
                </label>
              )}
            </div>
            {sameVersion && <p className="field-note">This server is already on {server.minecraftVersion}. Pick a different version.</p>}
            {error && <Banner variant="danger" title={report ? "The upgrade did not finish" : "Could not check"}>{error}</Banner>}
            {checking && (
              <div className="upgrade-progress" role="status" aria-live="polite">
                <Loader2 className="upgrade-spinner" size={20} aria-hidden="true" />
                <span>Checking your mods against {version}...</span>
              </div>
            )}
            {report && !checking && (
              <>
                {report.downgrade && (
                  <>
                    <Banner variant="danger" title="This is an older version than the server is on">
                      Worlds saved on a newer version can break or be damaged when opened on an older one.
                    </Banner>
                    <Checkbox
                      label="I understand and want to downgrade anyway"
                      checked={confirmDowngrade}
                      onChange={setConfirmDowngrade}
                    />
                  </>
                )}
                {report.warnings.length > 0 && (
                  <>
                    <Banner variant="warning" title="Before you continue" />
                    <ul className="upgrade-list">{report.warnings.map((warning) => <li key={warning}>{warning}</li>)}</ul>
                  </>
                )}
                <KeyValueList
                  items={[
                    { key: "from", label: "From", value: server.minecraftVersion, mono: true },
                    { key: "to", label: "To", value: report.targetVersion, mono: true },
                    { key: "java", label: "Java needed", value: report.javaMajor > 0 ? `Java ${report.javaMajor}` : "Not specified", note: "Cliff installs it if it is missing." },
                  ]}
                />
                {report.mods.length === 0 ? (
                  <p className="field-note">This server has no mods to check.</p>
                ) : (
                  groupOrder.map((group) => (
                    <ModGroup key={group.status} {...group} mods={report.mods.filter((mod) => mod.status === group.status)} />
                  ))
                )}
                {(updateCount > 0 || incompatibleCount > 0) && (
                  <div className="upgrade-options">
                    {updateCount > 0 && <Checkbox label="Update mods that have a newer file" checked={updateMods} onChange={setUpdateMods} />}
                    {incompatibleCount > 0 && <Checkbox label="Turn off mods that have no file for the new version" checked={disableIncompatible} onChange={setDisableIncompatible} />}
                  </div>
                )}
                <Banner variant="info">
                  Before changing anything, Cliff takes a safety snapshot of this server so you can roll back.
                </Banner>
              </>
            )}
          </>
        )}
      </div>
    </Modal>
  );
}

export function UpgradeCard({
  server,
  metadata,
  metadataError,
  isRunning,
  onUpgraded,
}: {
  server: ServerRecord;
  metadata: MinecraftMetadata | null;
  metadataError: string;
  isRunning: boolean;
  onUpgraded: (server: ServerRecord) => void | Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  return (
    <Card
      title="Upgrade Minecraft version"
      description={`This server is on ${server.minecraftVersion}. Move to another version and bring your mods along.`}
      actions={
        <Button iconLeft={<ArrowUpCircle size={16} aria-hidden="true" />} disabled={isRunning} aria-describedby="upgrade-card-note" onClick={() => setOpen(true)}>
          Upgrade...
        </Button>
      }
    >
      <p className="field-note" id="upgrade-card-note">
        {isRunning
          ? "Stop the server first. A server cannot be upgraded while it is running."
          : "Cliff checks your mods, takes a safety snapshot, then upgrades the server."}
      </p>
      {open && (
        <UpgradeWizard
          server={server}
          metadata={metadata}
          metadataError={metadataError}
          onClose={() => setOpen(false)}
          onUpgraded={onUpgraded}
        />
      )}
    </Card>
  );
}
