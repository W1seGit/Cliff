"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import dynamic from "next/dynamic";
import toast, { Toaster } from "react-hot-toast";
import { AlertCircle, ArrowLeft, CheckCircle2, Info, Menu, TriangleAlert } from "lucide-react";
import { serverTypeSupportsContent, validPort } from "./dashboard/lib/utils";
import { createServerProfile, daemonRuntimeEnabled, deleteServerProfile, fetchMinecraftMetadata, fetchRuntimeDashboard, fetchRuntimeStatus, fetchServerBackups, fetchServerHealth, fetchServerLogs, fetchServerMods, fetchSettings, restartRuntimeServer, startRuntimeServer, stopRuntimeServer, subscribeRuntime, updateServerProfile, checkForUpdates } from "./dashboard/lib/runtime-client";
import type { ServerRecord, RuntimeStatus, ServerHealth, Settings, ModFile, User, Backup, ConfirmRequest, UnsavedChangesRegistration, UpdateCheckResult } from "./dashboard/lib/types";
import type { MinecraftMetadata } from "./dashboard/lib/types";
import { ConfirmDialog } from "./dashboard/components/confirm-dialog";
import { EulaModal } from "./dashboard/components/eula-modal";
import { UpdateModal } from "./dashboard/components/update-modal";
import { Sidebar } from "./dashboard/components/sidebar";
import { ServerHeader } from "./dashboard/components/server-header";
import { OverviewPanel } from "./dashboard/panels/overview-panel";
import { Button } from "./dashboard/components/ui/button";
import { EmptyPanel } from "./dashboard/components/ui/empty-panel";
import { Hint } from "./dashboard/components/ui/hint";

const ConsolePanel = dynamic(() => import("./dashboard/panels/console-panel").then((mod) => mod.ConsolePanel), { loading: () => <DashboardSkeleton /> });
const ModsPanel = dynamic(() => import("./dashboard/panels/mods-panel").then((mod) => mod.ModsPanel), { loading: () => <DashboardSkeleton /> });
const WorldsPanel = dynamic(() => import("./dashboard/panels/worlds-panel").then((mod) => mod.WorldsPanel), { loading: () => <DashboardSkeleton /> });
const PlayersPanel = dynamic(() => import("./dashboard/panels/players-panel").then((mod) => mod.PlayersPanel), { loading: () => <DashboardSkeleton /> });
const BackupsPanel = dynamic(() => import("./dashboard/panels/backups-panel").then((mod) => mod.BackupsPanel), { loading: () => <DashboardSkeleton /> });
const FilesPanel = dynamic(() => import("./dashboard/panels/files-panel").then((mod) => mod.FilesPanel), { loading: () => <DashboardSkeleton /> });
const PublicAccessPanel = dynamic(() => import("./dashboard/panels/public-access-panel").then((mod) => mod.PublicAccessPanel), { loading: () => <DashboardSkeleton /> });
const ServerSettingsPanel = dynamic(() => import("./dashboard/panels/server-settings-panel").then((mod) => mod.ServerSettingsPanel), { loading: () => <DashboardSkeleton /> });
const AppSettingsPanel = dynamic(() => import("./dashboard/panels/app-settings-panel").then((mod) => mod.AppSettingsPanel), { loading: () => <DashboardSkeleton /> });
const ImportPanel = dynamic(() => import("./dashboard/panels/import-panel").then((mod) => mod.ImportPanel), { loading: () => <DashboardSkeleton /> });
const CreatePanel = dynamic(() => import("./dashboard/panels/create-panel").then((mod) => mod.CreatePanel), { loading: () => <DashboardSkeleton /> });

const emptyRuntime: RuntimeStatus = { runningServerId: null, lifecycle: "stopped", pid: null, startedAt: null, uptimeSeconds: 0, command: "", launchTarget: "" };
const serverNavItems = ["overview", "console", "mods", "worlds", "players", "backups", "files", "public-access", "settings"] as const;
const modsSubTabs = new Set(["mods/installed", "mods/discover"]);
const utilityTabs = new Set(["app", "account", "import", "create"]);

function isModsTab(tab: string) {
  return tab === "mods" || modsSubTabs.has(tab);
}
type RuntimeWaiter = { matches: (status: RuntimeStatus) => boolean; resolve: (matched: boolean) => void; timer: number };

declare global {
  interface Window {
    __cliffPushState?: History["pushState"];
  }
}

function routeFor(tab: string, serverId?: string) {
  if (tab === "app") return "/app-settings";
  if (tab === "account") return "/account";
  if (tab === "import") return "/import";
  if (tab === "create") return "/create";
  if (tab === "public-access/setup") return serverId ? `/servers/${encodeURIComponent(serverId)}/public-access/setup` : "/";
  return serverId ? `/servers/${encodeURIComponent(serverId)}/${tab}` : "/";
}

function readAppRoute(fallbackServerId: string, fallbackTab: string) {
  if (typeof window === "undefined") return { serverId: fallbackServerId, tab: fallbackTab };
  const parts = window.location.pathname.split("/").filter(Boolean).map(decodeURIComponent);
  if (parts[0] === "servers" && parts[1]) {
    if (parts[2] === "mods") {
      const subview = parts[3] === "discover" ? "discover" : "installed";
      return { serverId: parts[1], tab: `mods/${subview}` };
    }
    if (parts[2] === "public-access" && parts[3] === "setup") {
      return { serverId: parts[1], tab: "public-access/setup" };
    }
    const tab = parts[2] && serverNavItems.includes(parts[2] as (typeof serverNavItems)[number]) ? parts[2] : "overview";
    return { serverId: parts[1], tab };
  }
  if (parts[0] === "app-settings") return { serverId: fallbackServerId, tab: "app" };
  if (parts[0] === "account") return { serverId: fallbackServerId, tab: "account" };
  if (parts[0] && utilityTabs.has(parts[0])) return { serverId: fallbackServerId, tab: parts[0] };
  return { serverId: fallbackServerId, tab: fallbackTab };
}

function pushAppRoute(path: string) {
  if (typeof window === "undefined") return;
  const pushState = window.__cliffPushState ?? History.prototype.pushState;
  pushState.call(window.history, null, "", path);
  window.dispatchEvent(new Event("cliff:navigate"));
}

function startFailureLines(logs: string[] | null | undefined, runtime: RuntimeStatus, selected?: ServerRecord) {
  if (!selected || runtime.runningServerId === selected.id) return [];
  const lines = Array.isArray(logs) ? logs : [];
  const lastLaunchIndex = lines.findLastIndex((line) => /^Starting .+ from /i.test(line));
  const lastReadyIndex = lines.findLastIndex((line) => /\bDone \([^)]+\)!/i.test(line));
  if (lastLaunchIndex === -1 || lastReadyIndex > lastLaunchIndex) return [];
  return lines.slice(lastLaunchIndex).filter((line) =>
    /Server exited during startup|Server process failed|UnsupportedClassVersionError|Exception occurred when launching|Unable to access jarfile|Invalid maximum heap size|Could not reserve enough space/i.test(line)
    || /Server exited with code (?!0\b)\S+/i.test(line),
  );
}

function runtimeForServer(runtime: RuntimeStatus, serverId?: string): RuntimeStatus {
  if (!serverId) return emptyRuntime;
  return runtime.servers?.[serverId] ?? (runtime.runningServerId === serverId ? runtime : emptyRuntime);
}

export default function DashboardClient({ user, initialServerId = "", initialTab = "overview" }: { user: User; initialServerId?: string; initialTab?: string }) {
  const [account, setAccount] = useState(user);
  const [servers, setServers] = useState<ServerRecord[]>([]);
  const [runtime, setRuntime] = useState<RuntimeStatus>(emptyRuntime);
  const [selectedId, setSelectedId] = useState(initialServerId);
  const [mods, setMods] = useState<ModFile[]>([]);
  const [backups, setBackups] = useState<Backup[]>([]);
  const [logs, setLogs] = useState<string[]>([]);
  const [health, setHealth] = useState<ServerHealth | null>(null);
  const [settings, setSettings] = useState<Settings | null>(null);
  const [metadata, setMetadata] = useState<MinecraftMetadata | null>(null);
  const [metadataError, setMetadataError] = useState("");
  const [tab, setRawTab] = useState(initialTab);
  const [metadataBusy, setMetadataBusy] = useState(false);
  const [refreshBusy, setRefreshBusy] = useState(false);
  const [confirmRequest, setConfirmRequest] = useState<ConfirmRequest | null>(null);
  const [eulaModalOpen, setEulaModalOpen] = useState(false);
  const [updateCheck, setUpdateCheck] = useState<UpdateCheckResult | null>(null);
  const [updateModalOpen, setUpdateModalOpen] = useState(false);
  const [updateDismissed, setUpdateDismissed] = useState(false);
  const [unsavedChange, setUnsavedChange] = useState<UnsavedChangesRegistration | null>(null);
  const [serverActionMenu, setServerActionMenu] = useState("");
  const [quickBusyAction, setQuickBusyAction] = useState("");
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [documentVisible, setDocumentVisible] = useState(true);
  const [initialLoading, setInitialLoading] = useState(true);
  const [nowMs, setNowMs] = useState(Date.now());
  const [liveCommandSender, setLiveCommandSender] = useState<((command: string) => boolean) | null>(null);
  const workspaceRef = useRef<HTMLElement | null>(null);
  const lastAttentionKey = useRef("");
  const titleTimer = useRef<number | null>(null);
  const runtimeRef = useRef<RuntimeStatus>(emptyRuntime);
  const runtimeWaiters = useRef<RuntimeWaiter[]>([]);
  const unsavedChangeRef = useRef<UnsavedChangesRegistration | null>(null);
  const tabRef = useRef(tab);
  const selectedIdRef = useRef(selectedId);

  useEffect(() => {
    tabRef.current = tab;
  }, [tab]);

  useEffect(() => {
    selectedIdRef.current = selectedId;
  }, [selectedId]);

  useEffect(() => {
    unsavedChangeRef.current = unsavedChange;
  }, [unsavedChange]);

  const registerUnsavedChange = useCallback((change: UnsavedChangesRegistration | null) => {
    const activeChange = change?.dirty ? change : null;
    unsavedChangeRef.current = activeChange;
    setUnsavedChange(activeChange);
  }, []);

  const setMessage = useCallback((text: string) => {
    if (!text) return;
    const normalized = text.toLowerCase();
    const isError = /(fail|failed|error|invalid|denied|missing|required|blocked|cannot|can't)/.test(normalized);
    const isWarning = /(stop|warning|already|pending|first|before)/.test(normalized);
    const icon = isError ? <AlertCircle size={18} /> : isWarning ? <TriangleAlert size={18} /> : /refresh|copied|saved|created|updated|installed|uploaded|deleted|removed|renamed|duplicated|imported|enabled|disabled|requested|active|success/.test(normalized) ? <CheckCircle2 size={18} /> : <Info size={18} />;
    toast.custom((t) => (
      <button
        className={`toast ${isError ? "error" : isWarning ? "warning" : "success"}`}
        onClick={() => toast.dismiss(t.id)}
      >
        {icon}
        <span>{text}</span>
      </button>
    ), { duration: 3000 });
  }, []);

  const requestGuardedNavigation = useCallback((navigate: () => void, targetLabel = "another page") => {
    const change = unsavedChangeRef.current;
    if (!change?.dirty) {
      navigate();
      return;
    }
    const canSave = Boolean(change.onSave) && change.canSave !== false;
    const defaultMessage = canSave
      ? `${change.label} has unsaved changes. Save before going to ${targetLabel}, or discard them?`
      : `${change.label} has unsaved changes. Discard them before going to ${targetLabel}, or keep editing?`;
    setConfirmRequest({
      title: "Unsaved changes",
      message: change.message ?? defaultMessage,
      confirmLabel: canSave ? change.saveLabel ?? "Save" : change.discardLabel ?? "Discard changes",
      cancelLabel: canSave ? change.discardLabel ?? "Discard changes" : "Keep editing",
      dangerous: !canSave,
      disableBackdropCancel: true,
      onConfirm: async () => {
        if (canSave && change.onSave) {
          await change.onSave();
          registerUnsavedChange(null);
          navigate();
          return;
        }
        registerUnsavedChange(null);
        navigate();
      },
      onCancel: canSave
        ? async () => {
          registerUnsavedChange(null);
          navigate();
        }
        : undefined,
    });
  }, [registerUnsavedChange]);

  useEffect(() => {
    const syncRoute = () => {
      const nextRoute = readAppRoute(initialServerId, initialTab);
      const currentPath = routeFor(tabRef.current, selectedIdRef.current);
      if (unsavedChangeRef.current?.dirty && window.location.pathname !== currentPath) {
        const targetPath = window.location.pathname;
        window.history.pushState(null, "", currentPath);
        requestGuardedNavigation(() => {
          pushAppRoute(targetPath);
          const guardedRoute = readAppRoute(initialServerId, initialTab);
          setSelectedId(guardedRoute.serverId);
          setRawTab(guardedRoute.tab);
        }, "that page");
        return;
      }
      setSelectedId(nextRoute.serverId);
      setRawTab(nextRoute.tab);
    };
    window.addEventListener("popstate", syncRoute);
    window.addEventListener("cliff:navigate", syncRoute);
    return () => {
      window.removeEventListener("popstate", syncRoute);
      window.removeEventListener("cliff:navigate", syncRoute);
    };
  }, [initialServerId, initialTab, requestGuardedNavigation]);

  useEffect(() => {
    const onBeforeUnload = (event: BeforeUnloadEvent) => {
      if (!unsavedChangeRef.current?.dirty) return;
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => window.removeEventListener("beforeunload", onBeforeUnload);
  }, []);

  const selected = useMemo(() => servers.find((server) => server.id === selectedId) ?? servers[0], [servers, selectedId]);
  const selectedServerId = selected?.id ?? "";
  const selectedRuntime = useMemo(() => runtimeForServer(runtime, selected?.id), [runtime, selected?.id]);
  const runningServer = useMemo(() => servers.find((server) => server.id === runtime.runningServerId), [runtime.runningServerId, servers]);
  const isRunning = selected ? selectedRuntime.runningServerId === selected.id : false;
  const anotherServerRunning = false;
  const selectedLifecycle = isRunning ? selectedRuntime.lifecycle : "stopped";
  const selectedModsSupported = selected ? serverTypeSupportsContent(selected.type) : false;
  const publicAccessSetup = tab === "public-access/setup";
  const serverContext = Boolean(selected && !utilityTabs.has(tab) && !publicAccessSetup);
  const liveServerId = selected && serverContext && documentVisible && selectedRuntime.runningServerId === selected.id ? selected.id : "";
  const selectedDisplayRuntime = useMemo(() => selectedRuntime.startedAt
    ? { ...selectedRuntime, uptimeSeconds: Math.max(0, Math.floor((nowMs - new Date(selectedRuntime.startedAt).getTime()) / 1000)) }
    : selectedRuntime,
  [selectedRuntime, nowMs]);

  function resolveRuntimeWaiters(nextRuntime: RuntimeStatus) {
    if (runtimeWaiters.current.length === 0) return;
    const pending: RuntimeWaiter[] = [];
    for (const waiter of runtimeWaiters.current) {
      if (waiter.matches(nextRuntime)) {
        window.clearTimeout(waiter.timer);
        waiter.resolve(true);
      } else {
        pending.push(waiter);
      }
    }
    runtimeWaiters.current = pending;
  }

  const applyRuntime = useCallback((nextRuntime: RuntimeStatus) => {
    runtimeRef.current = nextRuntime;
    resolveRuntimeWaiters(nextRuntime);
    setRuntime(nextRuntime);
  }, []);

  const setTab = useCallback((nextTab: string, nextServerId = selectedId) => {
    const targetServer = servers.find((server) => server.id === nextServerId) ?? selected;
    let resolvedTab = nextTab;
    if (isModsTab(resolvedTab) && targetServer && !serverTypeSupportsContent(targetServer.type)) {
      setMessage("Mods and plugins are disabled for this server type.");
      resolvedTab = "overview";
    }
    if (resolvedTab === "mods") resolvedTab = "mods/installed";
    const targetRoute = routeFor(resolvedTab, nextServerId);
    if (targetRoute === routeFor(tabRef.current, selectedIdRef.current)) return;
    requestGuardedNavigation(() => {
      setRawTab(resolvedTab);
      pushAppRoute(targetRoute);
    }, "another page");
  }, [requestGuardedNavigation, selected, selectedId, servers, setMessage]);

  const selectServer = useCallback((id: string, nextTab = utilityTabs.has(tab) ? "overview" : tab) => {
    const targetRoute = routeFor(nextTab, id);
    if (targetRoute === routeFor(tabRef.current, selectedIdRef.current)) return;
    requestGuardedNavigation(() => {
      setSelectedId(id);
      setRawTab(nextTab);
      pushAppRoute(targetRoute);
    }, "another server");
  }, [requestGuardedNavigation, tab]);

  async function loadDashboard({ includeSettings = true, includeSettingsStorage = false, includeHealth = false }: { includeSettings?: boolean; includeSettingsStorage?: boolean; includeHealth?: boolean } = {}) {
    const metadataRequest = metadata
      ? Promise.resolve<{ data: MinecraftMetadata | null; error: string }>({ data: metadata, error: "" })
      : fetchMinecraftMetadata().then((data) => ({ data, error: "" })).catch((error) => ({ data: null, error: error instanceof Error ? error.message : "Metadata failed" }));
    const requestedServerId = selectedId && includeHealth ? selectedId : "";
    const [serverData, settingsData, metadataResult] = await Promise.all([
      fetchRuntimeDashboard(includeHealth, requestedServerId),
      includeSettings ? fetchSettings(includeSettingsStorage) : Promise.resolve(null),
      metadataRequest,
    ]);
    setServers(serverData.servers);
    applyRuntime(serverData.runtime);
    const activeServerId = selectedId && serverData.servers.some((server) => server.id === selectedId) ? selectedId : serverData.servers[0]?.id ?? "";
    if (includeHealth && activeServerId && serverData.health?.[activeServerId]) {
      setHealth(serverData.health[activeServerId]);
    }
    if (metadataResult.data) { setMetadata(metadataResult.data); setMetadataError(""); }
    else if (metadataResult.error) setMetadataError(metadataResult.error);
    if (settingsData) {
      setSettings((current) => settingsData.storage || !current ? settingsData : { ...settingsData, storage: current.storage });
    }
    if (!selectedId && serverData.servers[0] && !utilityTabs.has(tab)) setSelectedId(serverData.servers[0].id);
    if (selectedId && !serverData.servers.some((server) => server.id === selectedId)) setSelectedId(serverData.servers[0]?.id ?? "");
    // No servers exist — redirect to the overview welcome screen.
    // Use replaceState instead of pushAppRoute to avoid syncRoute
    // overriding the tab back to initialTab (e.g., "create").
    if (serverData.servers.length === 0 && tab !== "overview" && tab !== "create" && tab !== "import") {
      setRawTab("overview");
      setSelectedId("");
      if (typeof window !== "undefined") {
        window.history.replaceState(null, "", "/");
      }
    } else if (serverData.servers.length === 0 && selectedId) {
      setSelectedId("");
      if (typeof window !== "undefined" && !utilityTabs.has(tab)) {
        window.history.replaceState(null, "", "/");
      }
    }
  }

  async function refresh(options: { includeSettings?: boolean; includeSettingsStorage?: boolean; includeHealth?: boolean } = {}) { await loadDashboard(options); }

  async function refreshAll() {
    if (refreshBusy) return;
    setRefreshBusy(true);
    try {
      await loadDashboard({ includeSettings: true, includeSettingsStorage: tab === "app", includeHealth: tab === "overview" });
      await refreshSelected(selected?.id, {
        clear: false,
        includeMods: isModsTab(tab),
        includeBackups: tab === "backups",
        includeLogs: tab === "console",
        includeHealth: tab === "overview",
      });
      setMessage("Refreshed");
    }
    catch (error) { setMessage(error instanceof Error ? error.message : "Refresh failed"); }
    finally { setRefreshBusy(false); }
  }

  async function refreshVersionMetadata() {
    if (metadataBusy) return;
    setMetadataBusy(true);
    try {
      const data = await fetchMinecraftMetadata(true);
      setMetadata(data);
      setMetadataError("");
      setMessage(`Latest release is ${data.latest.release}.`);
    } catch (error) { setMetadataError(error instanceof Error ? error.message : "Version refresh failed"); setMessage(metadataError); }
    finally { setMetadataBusy(false); }
  }

  async function refreshSelected(serverId = selected?.id, { clear = false, includeMods = isModsTab(tab), includeBackups = tab === "backups", includeLogs = tab === "console", includeHealth = tab === "overview" }: { clear?: boolean; includeMods?: boolean; includeBackups?: boolean; includeLogs?: boolean; includeHealth?: boolean } = {}) {
    if (!serverId) { if (clear) { setMods([]); setBackups([]); setLogs([]); setHealth(null); } return; }
    if (clear) { setMods([]); setBackups([]); setLogs([]); setHealth(null); }
    const targetServer = servers.find((server) => server.id === serverId) ?? (selected?.id === serverId ? selected : null);
    const fetchMods = includeMods && Boolean(targetServer && serverTypeSupportsContent(targetServer.type));
    const [modData, backupData, logData, healthData] = await Promise.all([
      fetchMods ? fetchServerMods(serverId) : Promise.resolve(null),
      includeBackups ? fetchServerBackups(serverId) : Promise.resolve(null),
      includeLogs ? fetchServerLogs(serverId) : Promise.resolve(null),
      includeHealth ? fetchServerHealth(serverId) : Promise.resolve(null),
    ]);
    if (modData) setMods(modData.mods ?? []);
    else if (targetServer && !serverTypeSupportsContent(targetServer.type)) setMods([]);
    if (backupData) setBackups(backupData.backups ?? []);
    if (logData) setLogs(logData.logs ?? []);
    if (healthData) setHealth(healthData.health);
  }

  async function waitForRuntimeState(matches: (status: RuntimeStatus) => boolean, timeoutMs = 12000) {
    if (matches(runtimeRef.current)) return true;
    if (daemonRuntimeEnabled() && liveServerId) {
      const liveMatched = await new Promise<boolean>((resolve) => {
        const waiter: RuntimeWaiter = {
          matches,
          resolve,
          timer: window.setTimeout(() => {
            runtimeWaiters.current = runtimeWaiters.current.filter((item) => item !== waiter);
            resolve(false);
          }, timeoutMs),
        };
        runtimeWaiters.current = [...runtimeWaiters.current, waiter];
      });
      if (liveMatched) return true;
    }

    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      const status = await fetchRuntimeStatus();
      applyRuntime(status);
      if (matches(status)) return true;
      await new Promise((resolve) => window.setTimeout(resolve, 750));
    }
    return false;
  }

  async function waitForServerRuntimeState(serverId: string, matches: (status: RuntimeStatus) => boolean, timeoutMs = 12000) {
    return waitForRuntimeState((status) => matches(runtimeForServer(status, serverId)), timeoutMs);
  }

  async function runQuickAction(path: string, body = {}, busyLabel = path) {
    if (!selected || quickBusyAction) return;
    const actionServerId = selected.id;
    setQuickBusyAction(busyLabel);
    // Navigate to console immediately for start/restart — don't wait for the
    // server to finish starting. The backend start endpoint blocks up to 5s
    // waiting for startup confirmation, but the user should see the console
    // right away so they can watch the boot output.
    if (path === "start" || path === "restart") setTab("console", actionServerId);
    try {
      const force = "force" in body;
      const result = path === "start"
        ? await startRuntimeServer(actionServerId)
        : path === "stop"
          ? await stopRuntimeServer(actionServerId, force)
          : await restartRuntimeServer(actionServerId, force);
      await loadDashboard({ includeSettings: false, includeSettingsStorage: false });
      await refreshSelected(actionServerId, { clear: false, includeMods: false, includeBackups: false });
      if (path === "start") {
        setMessage("Server started");
      } else if (path === "stop") {
        if ("pending" in result && result.pending) {
          setMessage("Server is still stopping");
        } else {
          const stopped = await waitForServerRuntimeState(actionServerId, (status) => status.runningServerId !== actionServerId, "force" in body ? 5000 : 15000);
          setMessage(stopped ? "Server stopped" : "Server is still stopping");
        }
      } else if (path === "restart") {
        await waitForServerRuntimeState(actionServerId, (status) => status.runningServerId === actionServerId, 15000);
        setMessage("Server restarted");
      } else {
        setMessage(`${busyLabel.replace("-", " ")} complete`);
      }
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Action failed");
    }
    finally { setQuickBusyAction(""); }
  }

  async function renameSidebarServer(server: ServerRecord) {
    const nextName = window.prompt("Rename server", server.name)?.trim();
    setServerActionMenu("");
    if (!nextName || nextName === server.name) return;
    try {
      await updateServerProfile(server.id, { name: nextName });
      await refresh();
      if (selected?.id === server.id) await refreshSelected(server.id);
      setMessage("Renamed");
    } catch (error) { setMessage(error instanceof Error ? error.message : "Rename failed"); }
  }

  async function duplicateSidebarServer(server: ServerRecord) {
    setServerActionMenu("");
    if (runtimeForServer(runtime, server.id).runningServerId === server.id) { setMessage("Stop this server before cloning."); return; }
    const cloneName = window.prompt("Clone server as", `${server.name} Copy`)?.trim();
    if (!cloneName) return;
    const portPrompt = window.prompt("Port for clone", String(server.port + 1));
    if (portPrompt === null) return;
    const portValue = Number(portPrompt);
    if (!validPort(portValue)) { setMessage("Invalid port."); return; }
    try {
      const data = await createServerProfile({ mode: "clone", sourceServerId: server.id, name: cloneName, port: portValue });
      await refresh();
      if (data.server) selectServer(data.server.id);
      setMessage("Server cloned");
    } catch (error) { setMessage(error instanceof Error ? error.message : "Clone failed"); }
  }

  function deleteSidebarServer(server: ServerRecord) {
    setServerActionMenu("");
    setConfirmRequest({
      title: "Delete server",
      message: `"${server.name}" and all its files will be permanently deleted. This cannot be undone.`,
      confirmLabel: "Delete server",
      dangerous: true,
      onConfirm: async () => {
        try {
          await deleteServerProfile(server.id, true);
          registerUnsavedChange(null);
          if (selected?.id === server.id) {
            const next = servers.find((item) => item.id !== server.id);
            if (next) selectServer(next.id);
            else {
              setSelectedId("");
              setRawTab("overview");
              // Use replaceState instead of pushAppRoute to avoid triggering
              // syncRoute, which would override the tab back to initialTab
              // (e.g., "create" if the page was loaded on /create).
              if (typeof window !== "undefined") {
                window.history.replaceState(null, "", "/");
              }
            }
          }
          await refresh();
          setMessage("Server deleted");
        } catch (error) { setMessage(error instanceof Error ? error.message : "Deletion failed"); }
      },
    });
  }

  useEffect(() => {
    const query = window.matchMedia("(max-width: 900px)");
    const syncSidebarMode = () => setSidebarCollapsed(query.matches);
    syncSidebarMode();
    query.addEventListener("change", syncSidebarMode);
    return () => query.removeEventListener("change", syncSidebarMode);
  }, []);
  useEffect(() => {
    const timer = window.setTimeout(() => {
      refresh({ includeSettings: true, includeSettingsStorage: tab === "app", includeHealth: tab === "overview" })
        .catch((error) => setMessage(error.message))
        .finally(() => setInitialLoading(false));
    }, 0);
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Auto-check for updates on mount and periodically.
  useEffect(() => {
    let alive = true;
    const doCheck = () => {
      checkForUpdates()
        .then((result) => {
          if (!alive) return;
          setUpdateCheck(result);
          if (result.updateAvailable && !updateDismissed) {
            setUpdateModalOpen(true);
          }
        })
        .catch(() => {
          // Silently ignore update check errors on the frontend.
        });
    };
    const checkTimer = window.setTimeout(doCheck, 3000);
    const interval = window.setInterval(doCheck, 6 * 60 * 60 * 1000);
    return () => {
      alive = false;
      window.clearTimeout(checkTimer);
      window.clearInterval(interval);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [updateDismissed]);
  useEffect(() => {
    const updateVisibility = () => setDocumentVisible(!document.hidden);
    updateVisibility();
    document.addEventListener("visibilitychange", updateVisibility);
    return () => document.removeEventListener("visibilitychange", updateVisibility);
  }, []);
  useEffect(() => () => {
    for (const waiter of runtimeWaiters.current) {
      window.clearTimeout(waiter.timer);
      waiter.resolve(false);
    }
    runtimeWaiters.current = [];
  }, []);
  useEffect(() => {
    if (!runtime.startedAt || !serverContext || !documentVisible) return;
    const timer = window.setInterval(() => setNowMs(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [runtime.startedAt, serverContext, documentVisible]);
  useEffect(() => {
    if (!selected?.id) return;
    const timer = window.setTimeout(() => {
      refreshSelected(selected.id, {
        clear: true,
        includeMods: isModsTab(tab),
        includeBackups: tab === "backups",
        includeLogs: tab === "console",
        includeHealth: tab === "overview",
      }).catch((error) => setMessage(error.message));
    }, 0);
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selected?.id, tab]);
  useEffect(() => {
    if (tab !== "app" || settings?.storage) return;
    fetchSettings(true).then(setSettings).catch((error) => setMessage(error.message));
  }, [setMessage, settings, tab]);
  useEffect(() => {
    if (tab !== "mods") return;
    const timer = window.setTimeout(() => setTab("mods/installed", selectedId), 0);
    return () => window.clearTimeout(timer);
  }, [tab, selectedId, setTab]);
  useEffect(() => {
    if (initialLoading || !isModsTab(tab) || !selectedServerId || selectedModsSupported) return;
    const timer = window.setTimeout(() => setTab("overview", selectedServerId), 0);
    return () => window.clearTimeout(timer);
  }, [initialLoading, selectedServerId, selectedModsSupported, setTab, tab]);
  useEffect(() => {
    if (daemonRuntimeEnabled() && liveServerId) return;
    const intervalMs = runtime.runningServerId ? 60000 : 120000;
    const timer = window.setInterval(() => {
      if (document.hidden) return;
      loadDashboard({ includeSettings: false, includeSettingsStorage: false, includeHealth: false }).catch(() => undefined);
      if (selected?.id) refreshSelected(selected.id, { clear: false, includeMods: isModsTab(tab), includeBackups: tab === "backups", includeLogs: false, includeHealth: tab === "overview" }).catch(() => undefined);
    }, intervalMs);
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [liveServerId, runtime.runningServerId, selected?.id, tab]);

  useEffect(() => {
    if (!liveServerId || typeof WebSocket === "undefined") {
      return;
    }
    return subscribeRuntime(liveServerId, {
      onSnapshot: (data) => {
        applyRuntime(data.runtime);
        if (data.logs) setLogs(data.logs);
      },
      onRuntime: (nextRuntime) => {
        applyRuntime(nextRuntime);
      },
      onLog: (line) => {
        setLogs((current) => [...current, line].slice(-1000));
      },
      onError: setMessage,
      onCommandSender: (sender) => setLiveCommandSender(sender ? () => sender : null),
      includeUsage: tab === "overview",
      includeLogs: tab === "console",
    });
  }, [applyRuntime, liveServerId, setMessage, tab]);

  useEffect(() => {
    const attentionLines = startFailureLines(logs, selectedRuntime, selected);
    const nextKey = attentionLines.slice(-3).join("\n");
    if (!nextKey || nextKey === lastAttentionKey.current || tab === "console") {
      lastAttentionKey.current = nextKey;
      return;
    }
    lastAttentionKey.current = nextKey;
    const originalTitle = document.title;
    let flips = 0;
    if (titleTimer.current) window.clearInterval(titleTimer.current);
    titleTimer.current = window.setInterval(() => {
      document.title = flips % 2 === 0 ? "Console attention" : originalTitle;
      flips += 1;
      if (flips > 6 && titleTimer.current) {
        window.clearInterval(titleTimer.current);
        titleTimer.current = null;
        document.title = originalTitle;
      }
    }, 650);
    return () => {
      if (titleTimer.current) window.clearInterval(titleTimer.current);
      document.title = originalTitle;
    };
  }, [logs, selectedRuntime, selected, tab]);

  const standaloneUtility = tab === "app" || tab === "account" || tab === "import" || tab === "create" || publicAccessSetup;
  const pageTitle = initialLoading ? "Loading dashboard" : tab === "account" ? "Manage account" : tab === "app" ? "App settings" : tab === "import" ? "Import server" : tab === "create" ? "Create server" : publicAccessSetup ? "Public Access" : selected?.name ?? "No server selected";
  const pageSubtitle = initialLoading
    ? "Loading servers, runtime, and version metadata."
    : tab === "account"
    ? "Update your local dashboard account."
    : tab === "app"
    ? "Global dashboard settings."
    : tab === "import"
      ? "Register an existing server folder or ZIP."
      : tab === "create"
        ? "Create a managed Minecraft server profile."
        : publicAccessSetup
          ? selected ? `Set up Playit for ${selected.name}.` : "Set up Playit for this server."
        : selected ? `${selected.type} ${selected.minecraftVersion} / port ${selected.port}` : "Import or create a server.";
  const consoleAttention = tab !== "console" && startFailureLines(logs, selectedRuntime, selected).length > 0;
  const goBackToServer = () => {
    if (publicAccessSetup && selectedServerId) {
      setTab("public-access", selectedServerId);
      return;
    }
    if (selectedServerId) { setTab("overview", selectedServerId); return; }
    // No server selected — go to overview, bypassing the guarded navigation
    // since there's nothing to save and the guard can get stuck after deletions.
    // Use replaceState to avoid syncRoute overriding the tab back to initialTab.
    registerUnsavedChange(null);
    setRawTab("overview");
    if (typeof window !== "undefined") {
      window.history.replaceState(null, "", "/");
    }
  };

  return (
    <main className={`shell ${sidebarCollapsed ? "sidebar-collapsed" : ""} ${standaloneUtility ? "standalone" : ""}`}>
      {!standaloneUtility && (
        <Sidebar
          user={account}
          servers={servers}
          runtime={runtime}
          selected={selected}
          showSelectedServer={serverContext}
          collapsed={sidebarCollapsed}
          setCollapsed={setSidebarCollapsed}
          serverActionMenu={serverActionMenu}
          setServerActionMenu={setServerActionMenu}
          setSelectedId={selectServer}
          setTab={setTab}
          tab={tab}
          consoleAttention={consoleAttention}
          onRename={renameSidebarServer}
          onDuplicate={duplicateSidebarServer}
          onDelete={deleteSidebarServer}
          loading={initialLoading}
        />
      )}
      {!standaloneUtility && !sidebarCollapsed && <Button className="sidebar-backdrop" aria-label="Close sidebar" onClick={() => setSidebarCollapsed(true)} />}
      <section className={`workspace ${standaloneUtility ? "standalone-workspace" : ""}`} ref={workspaceRef}>
        {serverContext && selected && !initialLoading ? (
          <ServerHeader
            selected={selected}
            isRunning={isRunning}
            lifecycle={selectedLifecycle}
            anotherServerRunning={anotherServerRunning}
            runningServer={runningServer}
            busyAction={quickBusyAction}
            onAction={runQuickAction}
            refreshBusy={refreshBusy}
            onRefresh={refreshAll}
            onMessage={setMessage}
            onOpenSidebar={() => setSidebarCollapsed(false)}
          />
        ) : (
          <header className={`topbar ${standaloneUtility ? "standalone-topbar" : ""}`}>
            <div className="standalone-topbar-left">
              {standaloneUtility ? (
                <Button className="utility-back-button" onClick={goBackToServer}>
                  <ArrowLeft size={16} />Back
                </Button>
              ) : (
                <Button className="mobile-sidebar-button" aria-label="Open sidebar" onClick={() => setSidebarCollapsed(false)}>
                  <Menu size={18} />
                </Button>
              )}
              <div className="topbar-copy">
                <h1>{pageTitle}</h1>
                <p>{pageSubtitle}</p>
              </div>
            </div>
            {serverContext && <div className="status-row">
              <span className={`status ${isRunning ? selectedLifecycle === "running" ? "on" : "busy" : anotherServerRunning ? "busy" : ""}`}>{isRunning ? selectedLifecycle === "starting" ? "Starting" : selectedLifecycle === "stopping" ? "Stopping" : "Running" : anotherServerRunning ? "Blocked" : "Stopped"}</span>
              {anotherServerRunning && runningServer && <span className="status-detail">Blocked by {runningServer.name}</span>}
            </div>}
            {tab === "account" && (
              <Button variant="primary" disabled={!unsavedChange?.dirty || !unsavedChange?.canSave} onClick={() => unsavedChange?.onSave?.()} className="topbar-save-button">
                {unsavedChange?.saveLabel ?? "Save"}
              </Button>
            )}
          </header>
        )}

        {metadataError && (
          <Hint variant="source" warn>
            <span>Version metadata failed: {metadataError}</span>
            <Button disabled={metadataBusy} onClick={refreshVersionMetadata}>{metadataBusy ? "Refreshing..." : "Refresh"}</Button>
          </Hint>
        )}

        {initialLoading && <DashboardSkeleton />}
        {!initialLoading && tab === "overview" && <OverviewPanel selected={selected} health={health} isRunning={isRunning} runtime={selectedDisplayRuntime} setTab={setTab} onMessage={setMessage} onAcceptEula={() => setEulaModalOpen(true)} />}
        {!initialLoading && tab === "console" && selected && <ConsolePanel selected={selected} isRunning={isRunning} anotherServerRunning={anotherServerRunning} runningServer={runningServer} runtime={selectedDisplayRuntime} logs={logs} onCommand={liveServerId === selected.id ? liveCommandSender : null} onMessage={setMessage} onRefresh={() => refreshSelected(selected.id, { clear: false, includeMods: false, includeBackups: false })} onAcceptEula={() => setEulaModalOpen(true)} />}
        {!initialLoading && tab === "console" && !selected && <EmptyPanel title="No server selected" action="Import server" onAction={() => setTab("import")} />}
        {!initialLoading && isModsTab(tab) && selected && selectedModsSupported && <ModsPanel key={selected.id} server={selected} mods={mods} metadata={metadata} metadataError={metadataError} isRunning={isRunning} view={tab === "mods/discover" ? "discover" : "installed"} onRefresh={() => refreshSelected()} onMessage={setMessage} onConfirm={setConfirmRequest} onNavigateDiscover={() => setTab("mods/discover", selected.id)} />}
        {!initialLoading && isModsTab(tab) && !selected && <EmptyPanel title="No mods to show" action="Import server" onAction={() => setTab("import")} />}
        {!initialLoading && tab === "players" && selected && <PlayersPanel server={selected} onMessage={setMessage} />}
        {!initialLoading && tab === "players" && !selected && <EmptyPanel title="No player lists" action="Import server" onAction={() => setTab("import")} />}
        {!initialLoading && tab === "worlds" && selected && <WorldsPanel server={selected} isRunning={isRunning} onMessage={setMessage} onConfirm={setConfirmRequest} />}
        {!initialLoading && tab === "worlds" && !selected && <EmptyPanel title="No worlds to show" action="Import server" onAction={() => setTab("import")} />}
        {!initialLoading && tab === "backups" && selected && <BackupsPanel server={selected} backups={backups} isRunning={isRunning} onRefresh={() => refreshSelected()} onMessage={setMessage} onConfirm={setConfirmRequest} />}
        {!initialLoading && tab === "backups" && !selected && <EmptyPanel title="No backups yet" action="Import server" onAction={() => setTab("import")} />}
        {!initialLoading && tab === "files" && selected && <FilesPanel server={selected} onConfirm={setConfirmRequest} onMessage={setMessage} onUnsavedChange={registerUnsavedChange} />}
        {!initialLoading && tab === "files" && !selected && <EmptyPanel title="No files yet" action="Import server" onAction={() => setTab("import")} />}
        {!initialLoading && tab === "public-access" && selected && <PublicAccessPanel key={selected.id} server={selected} onConfigure={() => setTab("public-access/setup", selected.id)} onMessage={setMessage} />}
        {!initialLoading && tab === "public-access/setup" && selected && <PublicAccessPanel key={`${selected.id}:setup`} mode="setup" server={selected} onBack={() => setTab("public-access", selected.id)} onMessage={setMessage} />}
        {!initialLoading && tab === "public-access" && !selected && <EmptyPanel title="No public access setup" action="Import server" onAction={() => setTab("import")} />}
        {!initialLoading && tab === "settings" && selected && <ServerSettingsPanel server={selected} metadata={metadata} metadataError={metadataError} isRunning={isRunning} onSaved={refresh} onMessage={setMessage} onUnsavedChange={registerUnsavedChange} />}
        {!initialLoading && tab === "settings" && !selected && <EmptyPanel title="No server settings" action="Import server" onAction={() => setTab("import")} />}
        {!initialLoading && (tab === "app" || tab === "account") && settings && <AppSettingsPanel key={`${account.id ?? account.username}:${account.username}:${settings.serverRoot}:${settings.curseForgeApiKey}:${tab}`} mode={tab === "account" ? "account" : "settings"} user={account} settings={settings} metadata={metadata} metadataError={metadataError} metadataBusy={metadataBusy} updateCheck={updateCheck} onRefreshVersions={refreshVersionMetadata} onAccountSaved={setAccount} onSaved={() => refresh({ includeSettings: true, includeSettingsStorage: true })} onMessage={setMessage} onUnsavedChange={registerUnsavedChange} onConfirm={setConfirmRequest} />}
        {!initialLoading && (tab === "app" || tab === "account") && !settings && <DashboardSkeleton />}
        {!initialLoading && tab === "import" && <ImportPanel metadata={metadata} metadataError={metadataError} onImported={async (serverId?: string) => { await refresh(); registerUnsavedChange(null); if (serverId) selectServer(serverId, "overview"); else setTab("overview"); }} onMessage={setMessage} onUnsavedChange={registerUnsavedChange} />}
        {!initialLoading && tab === "create" && <CreatePanel metadata={metadata} metadataError={metadataError} onCreated={async (serverId?: string) => { await refresh(); registerUnsavedChange(null); if (serverId) selectServer(serverId, "overview"); else setTab("overview"); }} onMessage={setMessage} onUnsavedChange={registerUnsavedChange} />}
      </section>
      <Toaster position="bottom-right" toastOptions={{ duration: 3000 }} containerStyle={{ zIndex: 9999 }} />
      <ConfirmDialog request={confirmRequest} onClose={() => setConfirmRequest(null)} />
      {selected && <EulaModal serverId={selected.id} isOpen={eulaModalOpen} onClose={() => setEulaModalOpen(false)} onMessage={setMessage} onSaved={() => refreshSelected(selected.id, { clear: false, includeMods: false, includeBackups: false, includeHealth: true })} />}
      {updateCheck && updateCheck.updateAvailable && (
        <UpdateModal
          update={updateCheck}
          isOpen={updateModalOpen}
          onClose={() => { setUpdateModalOpen(false); setUpdateDismissed(true); }}
          onMessage={setMessage}
          onApplied={() => { setUpdateCheck(null); setUpdateModalOpen(false); }}
        />
      )}
    </main>
  );
}

function DashboardSkeleton() {
  return (
    <section className="dashboard-skeleton" aria-label="Loading dashboard">
      <div className="panel skeleton-panel skeleton-usage-panel">
        <div className="skeleton skeleton-heading" />
        <div className="skeleton skeleton-strip" />
        <div className="skeleton-graph-grid">
          <div className="skeleton skeleton-graph" />
          <div className="skeleton skeleton-graph" />
        </div>
        <div className="skeleton-card-grid">
          <div className="skeleton skeleton-card" />
          <div className="skeleton skeleton-card" />
          <div className="skeleton skeleton-card" />
        </div>
      </div>
      <div className="panel skeleton-panel">
        <div className="skeleton skeleton-heading" />
        <div className="skeleton skeleton-strip tall" />
      </div>
    </section>
  );
}
