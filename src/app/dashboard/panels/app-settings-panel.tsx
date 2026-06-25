"use client";

import { useEffect, useRef, useState } from "react";
import { Eye, EyeOff, Download, Copy, RefreshCw } from "lucide-react";
import { browserOrigin, externalApiBase, formatBytes, serverTypeNeedsLoader } from "../lib/utils";
import { daemonLogsUrl, fetchDaemonLogs, fetchDaemonLogsFull, fetchJavaRuntimes, fetchTypeVersions, installJavaRuntime, uninstallJavaRuntime, saveAccount as saveAccountProfile, saveSettings, checkForUpdates, applyUpdate } from "../lib/runtime-client";
import type { ConfirmRequest, JavaRuntimeInfo, MinecraftMetadata, ServerType, Settings, UnsavedChangesRegistration, UpdateCheckResult, User } from "../lib/types";
import { Button } from "../components/ui/button";
import { Panel } from "../components/ui/panel";
import { Input } from "../components/ui/input";
import { Hint } from "../components/ui/hint";
import { Select } from "../components/ui/select";
import { StatRow } from "../components/ui/stat-row";
import { Tabs } from "../components/ui/tabs";
import { ConsoleView } from "../components/ui/console-view";

type SettingsTab = "general" | "java" | "network" | "logs" | "updates";

export function AppSettingsPanel({
  mode = "settings",
  user,
  settings,
  metadata,
  metadataError,
  metadataBusy,
  updateCheck,
  onRefreshVersions,
  onAccountSaved,
  onSaved,
  onMessage,
  onUnsavedChange,
  onConfirm,
}: {
  mode?: "settings" | "account";
  user: User;
  settings: Settings;
  metadata: MinecraftMetadata | null;
  metadataError: string;
  metadataBusy: boolean;
  updateCheck?: UpdateCheckResult | null;
  onRefreshVersions: () => void;
  onAccountSaved: (user: User) => void;
  onSaved: () => void;
  onMessage: (message: string) => void;
  onUnsavedChange: (change: UnsavedChangesRegistration | null) => void;
  onConfirm: (request: ConfirmRequest) => void;
}) {
  const [username, setUsername] = useState(user.username);
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [curseForgeApiKey] = useState(settings.curseForgeApiKey);
  const [showCurrentPassword, setShowCurrentPassword] = useState(false);
  const [showNewPassword, setShowNewPassword] = useState(false);
  const [busy, setBusy] = useState(false);
  const [accountBusy, setAccountBusy] = useState(false);
  const [javaRuntimes, setJavaRuntimes] = useState<JavaRuntimeInfo[]>([]);
  const [javaBusy, setJavaBusy] = useState<number | null>(null);
  const [activeTab, setActiveTab] = useState<SettingsTab>(() => {
    if (typeof window === "undefined") return "general";
    const hash = window.location.hash.replace("#", "");
    const valid = ["general", "java", "network", "logs", "updates"];
    return (valid as string[]).includes(hash) ? hash as SettingsTab : "general";
  });
  const [daemonLogLines, setDaemonLogLines] = useState<string[]>([]);
  const [daemonLogsBusy, setDaemonLogsBusy] = useState(false);
  const [daemonLogsLoaded, setDaemonLogsLoaded] = useState(false);
  const [logMode, setLogMode] = useState<"live" | "full">("live");
  const loadedLogModeRef = useRef<"live" | "full" | null>(null);
  const [typeVersionCounts, setTypeVersionCounts] = useState<Record<string, number>>({});
  const [typeExpVersionCounts, setTypeExpVersionCounts] = useState<Record<string, number>>({});
  const [typeVersionsBusy, setTypeVersionsBusy] = useState(false);
  const [updateBusy, setUpdateBusy] = useState(false);
  const [updateChecking, setUpdateChecking] = useState(false);
  const [localUpdateCheck, setLocalUpdateCheck] = useState<UpdateCheckResult | null>(updateCheck ?? null);
  const accountChanged = username.trim() !== user.username || Boolean(newPassword);
  const canSaveAccount = Boolean(username.trim().length >= 3 && (!newPassword || (newPassword.length >= 10 && currentPassword)) && !accountBusy);
  const settingsChanged = curseForgeApiKey !== settings.curseForgeApiKey;
  const hasUnsavedChanges = mode === "account" ? accountChanged : settingsChanged;
  const lanAddresses = settings.access?.lanAddresses ?? [];
  const backendUrl = externalApiBase() || (typeof window !== "undefined" ? browserOrigin() : "");
  const dashboardPort = (() => {
    if (typeof window === "undefined") return "8080";
    const p = new URL(window.location.origin).port;
    return p || (window.location.protocol === "https:" ? "443" : "80");
  })();

  useEffect(() => {
    if (mode === "account") return;
    let alive = true;
    fetchJavaRuntimes()
      .then((data) => {
        if (alive) setJavaRuntimes(data.runtimes ?? []);
      })
      .catch((error) => {
        if (alive) onMessage(error instanceof Error ? error.message : "Java runtime check failed");
      });
    return () => {
      alive = false;
    };
  }, [mode, onMessage]);

  useEffect(() => {
    if (mode === "account" || activeTab !== "logs") return;
    if (loadedLogModeRef.current === logMode) return;
    let alive = true;
    const fetcher = logMode === "full" ? fetchDaemonLogsFull : fetchDaemonLogs;
    setDaemonLogsLoaded(false);
    fetcher()
      .then((data) => { if (alive) { setDaemonLogLines(data.logs ?? []); setDaemonLogsLoaded(true); loadedLogModeRef.current = logMode; } })
      .catch((error) => { if (alive) onMessage(error instanceof Error ? error.message : "Failed to load daemon logs"); });
    return () => { alive = false; };
  }, [mode, activeTab, logMode, onMessage]);

  // Sync tab from URL hash on back/forward navigation
  useEffect(() => {
    if (mode === "account") return;
    const onHashChange = () => {
      const hash = window.location.hash.replace("#", "");
      const valid = ["general", "java", "network", "logs", "updates"];
      const next = (valid as string[]).includes(hash) ? hash as SettingsTab : "general";
      setActiveTab((prev) => prev !== next ? next : prev);
    };
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, [mode]);

  // Fetch supported version counts for each server type
  const refreshTypeVersionCounts = async () => {
    setTypeVersionsBusy(true);
    const types: ServerType[] = ["vanilla", "paper", "purpur", "folia", "fabric", "forge", "neoforge"];
    const counts: Record<string, number> = {};
    const expCounts: Record<string, number> = {};
    await Promise.all(types.map(async (type) => {
      try {
        const data = await fetchTypeVersions(type);
        counts[type] = data.versions?.length ?? 0;
        expCounts[type] = data.experimentalVersions?.length ?? 0;
      } catch { counts[type] = 0; expCounts[type] = 0; }
    }));
    setTypeVersionCounts(counts);
    setTypeExpVersionCounts(expCounts);
    setTypeVersionsBusy(false);
  };

  useEffect(() => {
    if (mode === "account") return;
    let cancelled = false;
    const types: ServerType[] = ["vanilla", "paper", "purpur", "folia", "fabric", "forge", "neoforge"];
    (async () => {
      const counts: Record<string, number> = {};
      const expCounts: Record<string, number> = {};
      await Promise.all(types.map(async (type) => {
        try {
          const data = await fetchTypeVersions(type);
          if (cancelled) return;
          counts[type] = data.versions?.length ?? 0;
          expCounts[type] = data.experimentalVersions?.length ?? 0;
        } catch { counts[type] = 0; expCounts[type] = 0; }
      }));
      if (cancelled) return;
      setTypeVersionCounts(counts);
      setTypeExpVersionCounts(expCounts);
    })();
    return () => { cancelled = true; };
  }, [mode]);

  async function save() {
    if (busy) return false;
    setBusy(true);
    try {
      await saveSettings({ curseForgeApiKey });
      await onSaved();
      onMessage("Settings saved");
      return true;
    } catch (error) { onMessage(error instanceof Error ? error.message : "Settings failed"); return false; }
    finally { setBusy(false); }
  }

  async function saveAccount() {
    if (!accountChanged || !canSaveAccount) return false;
    setAccountBusy(true);
    try {
      const data = await saveAccountProfile({ username: username.trim(), currentPassword, newPassword });
      onAccountSaved(data.user);
      setCurrentPassword("");
      setNewPassword("");
      onMessage("Account saved");
      return true;
    } catch (error) { onMessage(error instanceof Error ? error.message : "Account save failed"); return false; }
    finally { setAccountBusy(false); }
  }

  async function saveDirtyChanges() {
    const saved = mode === "account" ? await saveAccount() : await save();
    if (!saved) throw new Error(mode === "account" ? "Account save failed" : "Settings failed");
  }

  const saveDirtyChangesRef = useRef(saveDirtyChanges);
  useEffect(() => { saveDirtyChangesRef.current = saveDirtyChanges; });

  useEffect(() => {
    onUnsavedChange(hasUnsavedChanges ? {
      id: mode === "account" ? "account-settings" : "app-settings",
      label: mode === "account" ? "Account settings" : "App settings",
      dirty: true,
      canSave: mode === "account" ? canSaveAccount : !busy,
      saveLabel: mode === "account" ? (accountBusy ? "Saving..." : "Save") : (busy ? "Saving..." : "Save"),
      onSave: () => saveDirtyChangesRef.current(),
    } : null);
    return () => onUnsavedChange(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasUnsavedChanges, mode, canSaveAccount, busy, accountBusy]);

  async function installJava(major: number) {
    if (javaBusy) return;
    setJavaBusy(major);
    try {
      const data = await installJavaRuntime(major);
      setJavaRuntimes(data.runtimes ?? []);
      onMessage(`Java ${major} installed`);
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Java install failed");
    } finally {
      setJavaBusy(null);
    }
  }

  async function uninstallJava(major: number) {
    if (javaBusy) return;
    setJavaBusy(major);
    try {
      const data = await uninstallJavaRuntime(major);
      setJavaRuntimes(data.runtimes ?? []);
      onMessage(`Java ${major} uninstalled`);
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Java uninstall failed");
    } finally {
      setJavaBusy(null);
    }
  }

  async function refreshJavaRuntimes() {
    if (javaBusy) return;
    setJavaBusy(-1);
    try {
      const data = await fetchJavaRuntimes();
      setJavaRuntimes(data.runtimes ?? []);
      onMessage("Java runtimes refreshed");
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Java refresh failed");
    } finally {
      setJavaBusy(null);
    }
  }

  async function refreshDaemonLogs() {
    setDaemonLogsBusy(true);
    try {
      const fetcher = logMode === "full" ? fetchDaemonLogsFull : fetchDaemonLogs;
      const data = await fetcher();
      setDaemonLogLines(data.logs ?? []);
      setDaemonLogsLoaded(true);
      loadedLogModeRef.current = logMode;
      onMessage("Logs refreshed");
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Failed to load daemon logs");
    } finally {
      setDaemonLogsBusy(false);
    }
  }

  async function checkForUpdatesNow() {
    setUpdateChecking(true);
    try {
      const result = await checkForUpdates(true);
      setLocalUpdateCheck(result);
      if (result.error) {
        onMessage(`Update check failed: ${result.error}`);
      } else if (result.updateAvailable) {
        onMessage(`Update available: v${result.latestVersion}`);
      } else {
        onMessage("Cliff is up to date");
      }
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Update check failed");
    } finally {
      setUpdateChecking(false);
    }
  }

  async function installUpdate() {
    setUpdateBusy(true);
    try {
      const result = await applyUpdate();
      if (result.success) {
        onMessage(result.message);
      } else {
        onMessage(result.message || "Update failed");
      }
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Update failed");
    } finally {
      setUpdateBusy(false);
    }
  }

  if (mode === "account") {
    return (
      <section className="settings-layout account-only">
        <div className="settings-column">
          <Panel className="account-settings-form form-grid compact-form settings-panel" as="div">
            <Input label="New username" value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="username" />
            <Input label="Current password" type={showCurrentPassword ? "text" : "password"} value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} autoComplete="current-password" suffix={
              <button type="button" className="input-suffix-button" onClick={() => setShowCurrentPassword((v) => !v)} aria-label={showCurrentPassword ? "Hide password" : "Show password"}>
                {showCurrentPassword ? <EyeOff size={15} /> : <Eye size={15} />}
              </button>
            } />
            <Input label="New password" type={showNewPassword ? "text" : "password"} value={newPassword} onChange={(event) => setNewPassword(event.target.value)} autoComplete="new-password" suffix={
              <button type="button" className="input-suffix-button" onClick={() => setShowNewPassword((v) => !v)} aria-label={showNewPassword ? "Hide password" : "Show password"}>
                {showNewPassword ? <EyeOff size={15} /> : <Eye size={15} />}
              </button>
            } />
            <Hint>Leave password fields blank to only update the username.</Hint>
          </Panel>
        </div>
      </section>
    );
  }

  return (
    <section className="app-settings-tabbed">
      <Tabs
        ariaLabel="App settings sections"
        items={[
          { id: "general", label: "General" },
          { id: "java", label: "Java" },
          { id: "network", label: "Network & Storage" },
          { id: "logs", label: "Logs" },
          { id: "updates", label: "Updates" },
        ]}
        activeId={activeTab}
        onChange={(id) => {
          setActiveTab(id as SettingsTab);
          if (typeof window !== "undefined") {
            window.history.replaceState(null, "", id === "general" ? window.location.pathname : `${window.location.pathname}#${id}`);
          }
        }}
      />

      {activeTab === "general" && (
        <>
          <div className="app-settings-actions">
            <Button variant="primary" disabled={busy} onClick={save} loading={busy} loadingText="Saving...">Save</Button>
          </div>
          <Panel className="form-grid compact-form settings-panel">
            <div className="settings-section">
              <h2 className="settings-section-header">CurseForge API</h2>
              <Hint>CurseForge integration will come in a later update.</Hint>
            </div>
            <div className="settings-section">
              <h2 className="settings-section-header">Versions</h2>
              {metadataError && <Hint warn>{metadataError}</Hint>}
              <div className="settings-version-info">
                <div className="settings-version-row">
                  <span className="muted">Latest release</span>
                  <strong>{metadata?.latest.release ?? "loading"}</strong>
                </div>
                <div className="settings-version-row">
                  <span className="muted">Latest snapshot</span>
                  <strong>{metadata?.latest.snapshot ?? "loading"}</strong>
                </div>
                <div className="settings-version-row">
                  <span className="muted">Total Mojang versions</span>
                  <strong>{metadata?.minecraftVersions.length ?? 0}</strong>
                </div>
              </div>
              <div className="settings-version-loaders">
                <div className="settings-version-row">
                  <span className="muted">Vanilla</span>
                  <strong>{typeVersionCounts.vanilla ?? "..."}</strong>
                  <small className="muted">supported versions</small>
                </div>
                <div className="settings-version-row">
                  <span className="muted">Paper</span>
                  <strong>{typeVersionCounts.paper ?? "..."}</strong>
                  <small className="muted">supported versions</small>
                  {typeExpVersionCounts.paper > 0 && <small className="muted">+{typeExpVersionCounts.paper} experimental</small>}
                </div>
                <div className="settings-version-row">
                  <span className="muted">Purpur</span>
                  <strong>{typeVersionCounts.purpur ?? "..."}</strong>
                  <small className="muted">supported versions</small>
                </div>
                <div className="settings-version-row">
                  <span className="muted">Folia</span>
                  <strong>{typeVersionCounts.folia ?? "..."}</strong>
                  <small className="muted">supported versions</small>
                  {typeExpVersionCounts.folia > 0 && <small className="muted">+{typeExpVersionCounts.folia} experimental</small>}
                </div>
                {(Object.entries(metadata?.loaderCatalog ?? metadata?.loaders ?? {}) as Array<[ServerType, { version: string }[]]>).filter(([type]) => serverTypeNeedsLoader(type)).map(([type, loaders]) => (
                  <div className="settings-version-row" key={type}>
                    <span className="muted">{type}</span>
                    <strong>{typeVersionCounts[type] ?? "..."}</strong>
                    <small className="muted">supported versions</small>
                    {loaders[0]?.version && <small className="muted">latest loader: {loaders[0].version}</small>}
                  </div>
                ))}
              </div>
              <Button disabled={metadataBusy || typeVersionsBusy} onClick={() => { onRefreshVersions(); refreshTypeVersionCounts(); }}>{metadataBusy || typeVersionsBusy ? "Refreshing..." : "Refresh versions"}</Button>
            </div>
          </Panel>
        </>
      )}

      {activeTab === "java" && (
        <>
          <div className="app-settings-actions">
            <Button disabled={Boolean(javaBusy)} onClick={refreshJavaRuntimes}>{javaBusy === -1 ? "Refreshing..." : "Refresh"}</Button>
          </div>
          <Panel className="form-grid compact-form settings-panel">
            <div className="settings-section">
              <h2 className="settings-section-header">Java runtimes</h2>
              <div className="property-list">
                {javaRuntimes.map((runtime) => (
                  <div className="property-row java-runtime-row" key={runtime.major}>
                    <span>
                      {runtime.label}
                      {runtime.required ? <small className="block muted">Required by one or more server profiles</small> : <small className="block muted">Available for manual selection</small>}
                      {runtime.usedBy.length > 0 && <small className="block muted">Used by: {runtime.usedBy.join(", ")}</small>}
                    </span>
                    <strong>{runtime.installed ? "Installed" : "Not installed"}</strong>
                    {runtime.installed ? (
                      <Button variant="danger" disabled={Boolean(javaBusy) || runtime.required} onClick={() => onConfirm({
                        title: `Uninstall ${runtime.label}`,
                        message: runtime.usedBy.length > 0
                          ? `This Java version is used by: ${runtime.usedBy.join(", ")}. Uninstalling will break those servers until they are reconfigured.`
                          : `This will remove the ${runtime.label} runtime from disk. Servers using it will need to be reconfigured.`,
                        confirmLabel: "Uninstall",
                        dangerous: true,
                        onConfirm: async () => { await uninstallJava(runtime.major); },
                      })}>{javaBusy === runtime.major ? "Uninstalling..." : "Uninstall"}</Button>
                    ) : (
                      <Button disabled={Boolean(javaBusy)} onClick={() => installJava(runtime.major)}>{javaBusy === runtime.major ? "Installing..." : "Install"}</Button>
                    )}
                  </div>
                ))}
                {javaRuntimes.length === 0 && <Hint>Loading Java runtimes...</Hint>}
              </div>
              <Hint>Required runtimes cannot be uninstalled while servers depend on them.</Hint>
            </div>
          </Panel>
        </>
      )}

      {activeTab === "network" && (
        <Panel className="form-grid compact-form settings-panel">
          <div className="settings-section">
            <h2 className="settings-section-header">Paths</h2>
            <StatRow variant="stacked" items={[
              { label: "Server storage root", value: settings.serverRoot || "not set" },
              { label: "Panel data directory", value: settings.dataDir || "not set" },
            ]} />
          </div>
          <div className="settings-section">
            <h2 className="settings-section-header">Storage</h2>
            <StatRow items={[
              { label: "Root", value: settings.storage?.rootExists ? formatBytes(settings.storage.serverRootSizeBytes) : "missing" },
              { label: "Snapshots", value: formatBytes(settings.storage?.snapshotsSizeBytes ?? 0) },
              { label: "Free", value: settings.storage?.freeBytes == null ? "unknown" : formatBytes(settings.storage.freeBytes) },
            ]} />
            <StatRow variant="stacked" items={[
              { label: "Registered servers", value: formatBytes(settings.storage?.registeredServerSizeBytes ?? 0) },
              { label: "Backups", value: settings.storage?.backupCount ?? 0 },
              { label: "Disk total", value: settings.storage?.totalBytes == null ? "unknown" : formatBytes(settings.storage.totalBytes) },
            ]} />
          </div>
          <div className="settings-section">
            <h2 className="settings-section-header">Network access</h2>
            {lanAddresses.length ? (
              <StatRow variant="stacked" items={[
                ...lanAddresses.map((ip) => ({
                  label: `Dashboard (${ip})`,
                  value: `${ip}:${dashboardPort}`,
                })),
                { label: "Backend", value: backendUrl },
              ]} />
            ) : <Hint warn>No LAN IPv4 address detected.</Hint>}
          </div>
        </Panel>
      )}

      {activeTab === "logs" && (
        <>
          <div className="app-settings-actions">
            <Select value={logMode} onChange={(e) => setLogMode(e.target.value as "live" | "full")} aria-label="Log mode">
              <option value="live">Live buffer</option>
              <option value="full">Full log file</option>
            </Select>
            <Button disabled={daemonLogsBusy || daemonLogLines.length === 0} onClick={() => navigator.clipboard.writeText(daemonLogLines.join("\n")).then(() => onMessage("Logs copied")).catch(() => onMessage("Copy failed"))}><Copy size={14} />Copy</Button>
            <Button disabled={daemonLogsBusy} onClick={refreshDaemonLogs}>{daemonLogsBusy ? "Loading..." : "Refresh"}</Button>
            <Button disabled={daemonLogLines.length === 0} onClick={() => window.open(daemonLogsUrl(logMode === "full" ? "?full=1&download=1" : "?download=1"), "_blank")}><Download size={14} />Download</Button>
          </div>
          <Panel className="form-grid compact-form settings-panel">
            <div className="settings-section">
              <div className="settings-section-header-row">
                <h2 className="settings-section-header">Daemon logs</h2>
                <span className="log-mode-hint">{logMode === "live" ? "Recent in-memory buffer" : "Complete log file from disk"}</span>
              </div>
              {settings.logFile && <p className="log-file-path">Log file: <code>{settings.logFile}</code></p>}
              <ConsoleView
                lines={daemonLogLines}
                emptyMessage={!daemonLogsLoaded ? "Loading logs..." : "No daemon logs recorded yet."}
              />
            </div>
          </Panel>
        </>
      )}

      {activeTab === "updates" && (
        <>
          <div className="app-settings-actions">
            <Button disabled={updateChecking} onClick={checkForUpdatesNow}>
              {updateChecking ? "Checking..." : "Check for updates"}
            </Button>
          </div>
          <Panel className="form-grid compact-form settings-panel">
            <div className="settings-section">
              <h2 className="settings-section-header">Cliff updates</h2>
              {(() => {
                const check = localUpdateCheck ?? updateCheck;
                if (!check) {
                  return <Hint>Click "Check for updates" to see if a new version is available.</Hint>;
                }
                if (check.error) {
                  return <Hint warn>Update check failed: {check.error}</Hint>;
                }
                return (
                  <>
                    <div className="property-list">
                      <div className="property-row">
                        <span className="muted">Current version</span>
                        <strong>v{check.currentVersion}</strong>
                      </div>
                      <div className="property-row">
                        <span className="muted">Latest version</span>
                        <strong>v{check.latestVersion}</strong>
                      </div>
                      {check.builtAt && (
                        <div className="property-row">
                          <span className="muted">Released</span>
                          <strong>{new Date(check.builtAt).toLocaleDateString("en-US", { year: "numeric", month: "short", day: "numeric" })}</strong>
                        </div>
                      )}
                      {check.archiveSize ? (
                        <div className="property-row">
                          <span className="muted">Download size</span>
                          <strong>{formatBytes(check.archiveSize)}</strong>
                        </div>
                      ) : null}
                    </div>
                    {check.updateAvailable ? (
                      <>
                        <Hint>A new version is available. Click "Install update" to download and apply it. The daemon will restart automatically.</Hint>
                        <Button variant="primary" disabled={updateBusy} onClick={installUpdate} loading={updateBusy} loadingText="Updating...">
                          {updateBusy ? "Updating..." : "Install update"}
                        </Button>
                      </>
                    ) : (
                      <Hint>Cliff is up to date.</Hint>
                    )}
                    {check.releaseUrl && (
                      <p className="muted">
                        <a href={check.releaseUrl} target="_blank" rel="noopener noreferrer">View release notes on GitHub</a>
                      </p>
                    )}
                  </>
                );
              })()}
            </div>
          </Panel>
        </>
      )}
    </section>
  );
}
