import type { Backup, CommandPreset, FileListing, FilePayload, ImportDetection, JavaRuntimeInfo, LoaderOption, MinecraftMetadata, ModFile, ModrinthProjectDetails, ModSearchResult, PlayerAccess, PlayerLookup, PlayerSession, PlayitAgentInfo, PublicAccessRecord, RuntimeStatus, RuntimeUsage, ServerHealth, ServerProperties, ServerRecord, ServerType, Settings, UpdateApplyResult, UpdateCheckResult, User, WorldsPayload } from "./types";
import { api, externalApiUrl } from "./utils";

type RuntimeDashboardPayload = {
  servers: ServerRecord[];
  runtime: RuntimeStatus;
  health?: Record<string, ServerHealth>;
};

type RuntimeSubscription = {
  onSnapshot: (payload: { runtime: RuntimeStatus; logs?: string[] }) => void;
  onRuntime: (runtime: RuntimeStatus) => void;
  onLog: (line: string) => void;
  onError?: (message: string) => void;
  onCommandSender?: (sender: ((command: string) => boolean) | null) => void;
  includeUsage?: boolean;
  includeLogs?: boolean;
};

export function daemonRuntimeEnabled() {
  return true;
}

function daemonPath(path: string) {
  return externalApiUrl(path);
}

async function daemonApi<T>(path: string, init?: RequestInit): Promise<T> {
  return api<T>(daemonPath(path), init);
}

function normalizeRuntime(status: RuntimeStatus): RuntimeStatus {
  const servers = status.servers
    ? Object.fromEntries(Object.entries(status.servers).map(([serverId, runtime]) => [serverId, normalizeRuntime({ ...runtime, servers: undefined })]))
    : undefined;
  return {
    runningServerId: status.runningServerId || null,
    lifecycle: status.lifecycle || "stopped",
    pid: status.pid || null,
    startedAt: status.startedAt || null,
    uptimeSeconds: status.uptimeSeconds || 0,
    command: status.command || "",
    launchTarget: status.launchTarget || "",
    usage: status.usage,
    servers,
  };
}

export async function fetchRuntimeDashboard(includeHealth = false, selectedServerId = ""): Promise<RuntimeDashboardPayload> {
  const scopedOverview = includeHealth && selectedServerId;
  const params = new URLSearchParams(scopedOverview ? { health: "1" } : { runtime: "1" });
  if (scopedOverview) {
    params.set("usageFor", selectedServerId);
    params.set("healthFor", selectedServerId);
  }
  const data = await daemonApi<RuntimeDashboardPayload>(`/api/servers?${params.toString()}`);
  return { ...data, runtime: normalizeRuntime(data.runtime) };
}

export async function createServerProfile(body: Record<string, unknown>) {
  return daemonApi<{ server?: ServerRecord; note?: string }>("/api/servers", { method: "POST", body: JSON.stringify(body) });
}

export async function fetchSettings(includeStorage = true) {
  const path = includeStorage ? "/api/settings" : "/api/settings?storage=0";
  return daemonApi<Settings>(path);
}

export async function saveSettings(body: Record<string, unknown>) {
  return daemonApi<Settings>("/api/settings", { method: "PUT", body: JSON.stringify(body) });
}

export async function saveAccount(body: Record<string, unknown>) {
  return daemonApi<{ user: User }>("/api/auth/account", { method: "PATCH", body: JSON.stringify(body) });
}

export async function fetchJavaRuntimes() {
  return daemonApi<{ runtimes: JavaRuntimeInfo[] }>("/api/java/runtimes");
}

export async function installJavaRuntime(major: number) {
  return daemonApi<{ ok: boolean; path: string; runtimes: JavaRuntimeInfo[] }>("/api/java/runtimes", { method: "POST", body: JSON.stringify({ major }) });
}

export async function uninstallJavaRuntime(major: number) {
  return daemonApi<{ ok: boolean; runtimes: JavaRuntimeInfo[] }>("/api/java/runtimes", { method: "DELETE", body: JSON.stringify({ major }) });
}

export async function fetchPlayitAgent() {
  return daemonApi<PlayitAgentInfo>("/api/public-access/playit/agent");
}

export async function installPlayitAgent() {
  return daemonApi<PlayitAgentInfo>("/api/public-access/playit/agent/install", { method: "POST", body: JSON.stringify({}) });
}

export async function startPlayitAgent() {
  return daemonApi<PlayitAgentInfo>("/api/public-access/playit/agent/start", { method: "POST", body: JSON.stringify({}) });
}

export async function stopPlayitAgent() {
  return daemonApi<PlayitAgentInfo>("/api/public-access/playit/agent/stop", { method: "POST", body: JSON.stringify({}) });
}

export async function uninstallPlayitAgent() {
  return daemonApi<PlayitAgentInfo>("/api/public-access/playit/agent/uninstall", { method: "POST", body: JSON.stringify({}) });
}

export async function resetPlayitAgent() {
  return daemonApi<PlayitAgentInfo>("/api/public-access/playit/agent/reset", { method: "POST", body: JSON.stringify({}) });
}

export async function fetchPublicAccessConfig(serverId: string) {
  return daemonApi<{ config?: PublicAccessRecord | null }>(`/api/servers/${serverId}/public-access`);
}

export async function savePublicAccessConfig(serverId: string, body: Partial<PublicAccessRecord>) {
  return daemonApi<{ config: PublicAccessRecord }>(`/api/servers/${serverId}/public-access`, { method: "PUT", body: JSON.stringify(body) });
}

export async function deletePublicAccessConfig(serverId: string) {
  return daemonApi<{ ok: boolean }>(`/api/servers/${serverId}/public-access`, { method: "DELETE" });
}

export async function updateServerProfile(serverId: string, body: Record<string, unknown>) {
  return daemonApi<{ server: ServerRecord }>(`/api/servers/${serverId}`, { method: "PATCH", body: JSON.stringify(body) });
}

export async function deleteServerProfile(serverId: string, deleteFiles = false) {
  return daemonApi(`/api/servers/${serverId}`, { method: "DELETE", body: JSON.stringify({ deleteFiles }) });
}

export async function detectImportSource(form: FormData) {
  return daemonApi<{ detection: ImportDetection }>("/api/servers", { method: "POST", body: form });
}

export async function importStagedServer(body: Record<string, unknown>) {
  return daemonApi<{ server?: ServerRecord; note?: string }>("/api/servers", { method: "POST", body: JSON.stringify(body) });
}

export async function fetchRuntimeStatus(): Promise<RuntimeStatus> {
  return normalizeRuntime(await daemonApi<RuntimeStatus>("/api/runtime?light=1"));
}

export async function fetchServerUsage(serverId: string, window: string): Promise<{ usage: RuntimeUsage }> {
  return daemonApi<{ usage: RuntimeUsage }>(`/api/servers/${serverId}/usage?window=${window}`);
}

export async function fetchServerHealth(serverId: string): Promise<{ health: ServerHealth }> {
  return daemonApi<{ health: ServerHealth }>(`/api/servers/${serverId}/health`);
}

export async function fetchServerLogs(serverId: string) {
  return daemonApi<{ logs: string[] }>(`/api/servers/${serverId}/logs`);
}

export async function fetchDaemonLogs() {
  return daemonApi<{ logs: string[] }>(`/api/daemon-logs`);
}

export async function fetchDaemonLogsFull() {
  return daemonApi<{ logs: string[] }>(`/api/daemon-logs?full=1`);
}

export function daemonLogsUrl(query = "") {
  return daemonPath(`/api/daemon-logs${query}`);
}

export async function fetchServerMods(serverId: string) {
  return daemonApi<{ mods: ModFile[]; disabled?: boolean }>(`/api/servers/${serverId}/mods`);
}

export async function searchServerMods(serverId: string, query: string) {
  const path = `/api/servers/${serverId}/mods?${query}`;
  return daemonApi<{ results: ModSearchResult[]; disabled?: boolean; nextOffset?: number }>(path);
}

export async function fetchModrinthProjectDetails(serverId: string, query: string) {
  const path = `/api/servers/${serverId}/mods?${query}`;
  return daemonApi<ModrinthProjectDetails>(path);
}

export async function runServerModAction(serverId: string, body: Record<string, unknown>) {
  return daemonApi<{ files?: string[]; dependencies?: NonNullable<ModFile["metadata"]>["dependencyWarnings"] }>(`/api/servers/${serverId}/mods`, { method: "POST", body: JSON.stringify(body) });
}

export async function uploadServerMod(serverId: string, form: FormData) {
  return daemonApi<{ files?: string[] }>(`/api/servers/${serverId}/mods`, { method: "POST", body: form });
}

export function modUrl(serverId: string, query: string) {
  return daemonPath(`/api/servers/${serverId}/mods${query}`);
}

export function logsUrl(serverId: string, query = "") {
  return daemonPath(`/api/servers/${serverId}/logs${query}`);
}

export async function fetchServerBackups(serverId: string) {
  return daemonApi<{ backups: Backup[] }>(`/api/servers/${serverId}/backups`);
}

export async function runBackupAction(serverId: string, body: Record<string, unknown>) {
  return daemonApi(`/api/servers/${serverId}/backups`, { method: "POST", body: JSON.stringify(body) });
}

export function backupUrl(serverId: string, query: string) {
  return daemonPath(`/api/servers/${serverId}/backups${query}`);
}

export async function fetchServerProperties(serverId: string) {
  return daemonApi<ServerProperties>(`/api/servers/${serverId}/properties`);
}

export async function saveServerProperties(serverId: string, body: { editable: ServerProperties["editable"]; raw?: ServerProperties["raw"]; eulaAccepted: boolean }) {
  return daemonApi<ServerProperties>(`/api/servers/${serverId}/properties`, { method: "PUT", body: JSON.stringify(body) });
}

export function serverPropertiesUrl(serverId: string, query: string) {
  return daemonPath(`/api/servers/${serverId}/properties${query}`);
}

export async function fetchServerPlayers(serverId: string) {
  return daemonApi<{ access: PlayerAccess; sessions: PlayerSession[] }>(`/api/servers/${serverId}/players`);
}

export async function lookupServerPlayer(serverId: string, username: string) {
  const query = `?lookup=${encodeURIComponent(username)}`;
  return daemonApi<PlayerLookup>(`/api/servers/${serverId}/players${query}`);
}

export async function runPlayerAccessAction(serverId: string, body: Record<string, unknown>) {
  return daemonApi<{ access: PlayerAccess; sessions: PlayerSession[]; removed?: string[] }>(`/api/servers/${serverId}/players`, { method: "POST", body: JSON.stringify(body) });
}

export async function fetchServerFile(serverId: string, relativePath = "") {
  const query = `?path=${encodeURIComponent(relativePath)}`;
  return daemonApi<FileListing | FilePayload>(`/api/servers/${serverId}/files${query}`);
}

export async function runFileAction(serverId: string, body: Record<string, unknown>) {
  return daemonApi(`/api/servers/${serverId}/files`, { method: "POST", body: JSON.stringify(body) });
}

export async function uploadServerFile(serverId: string, form: FormData) {
  return daemonApi(`/api/servers/${serverId}/files`, { method: "POST", body: form });
}

export function serverFileUrl(serverId: string, relativePath: string, query = "") {
  const separator = query ? `&${query.replace(/^\?/, "")}` : "";
  return daemonPath(`/api/servers/${serverId}/files?path=${encodeURIComponent(relativePath)}${separator}`);
}

export async function fetchServerWorlds(serverId: string) {
  return daemonApi<WorldsPayload>(`/api/servers/${serverId}/worlds`);
}

export async function runWorldAction(serverId: string, body: Record<string, unknown>) {
  return daemonApi<WorldsPayload & { files?: string[] }>(`/api/servers/${serverId}/worlds`, { method: "POST", body: JSON.stringify(body) });
}

export async function uploadWorldFile(serverId: string, form: FormData) {
  return daemonApi<WorldsPayload>(`/api/servers/${serverId}/worlds`, { method: "POST", body: form });
}

export async function searchWorldDatapacks(serverId: string, query: string) {
  const path = `/api/servers/${serverId}/worlds?${query}`;
  return daemonApi<{ results: ModSearchResult[] }>(path);
}

export async function fetchWorldDatapackDetails(serverId: string, query: string) {
  const path = `/api/servers/${serverId}/worlds?${query}`;
  return daemonApi<ModrinthProjectDetails>(path);
}

export function worldUrl(serverId: string, query: string) {
  return daemonPath(`/api/servers/${serverId}/worlds${query}`);
}

export async function fetchMinecraftMetadata(refresh = false) {
  const query = refresh ? "?refresh=1" : "";
  return daemonApi<MinecraftMetadata>(`/api/minecraft/versions${query}`);
}

export async function fetchLoaderVersions(type: ServerType, minecraftVersion: string, refresh = false) {
  const params = new URLSearchParams({ type, minecraftVersion });
  if (refresh) params.set("refresh", "1");
  const path = `/api/minecraft/versions?${params.toString()}`;
  return daemonApi<{ loaders: LoaderOption[] }>(path);
}

export async function fetchTypeVersions(type: ServerType, refresh = false) {
  const params = new URLSearchParams({ type });
  if (refresh) params.set("refresh", "1");
  const path = `/api/minecraft/versions?${params.toString()}`;
  return daemonApi<{ versions: string[]; experimentalVersions?: string[] }>(path);
}

export async function startRuntimeServer(serverId: string) {
  return daemonApi<RuntimeStatus & { pending?: boolean; stopped?: boolean }>(`/api/servers/${serverId}/start`, { method: "POST", body: JSON.stringify({}) });
}

export async function stopRuntimeServer(serverId: string, force = false) {
  return daemonApi<RuntimeStatus & { pending?: boolean; stopped?: boolean }>(`/api/servers/${serverId}/stop${force ? "?force=1" : ""}`, { method: "POST", body: JSON.stringify(force ? { force: true } : {}) });
}

export async function restartRuntimeServer(serverId: string, force = false) {
  return daemonApi<RuntimeStatus & { pending?: boolean; stopped?: boolean }>(`/api/servers/${serverId}/restart${force ? "?force=1" : ""}`, { method: "POST", body: JSON.stringify(force ? { force: true } : {}) });
}

export async function sendRuntimeCommand(serverId: string, command: string) {
  return daemonApi(`/api/servers/${serverId}/command`, { method: "POST", body: JSON.stringify({ command }) });
}

export async function fetchCommandPresets(serverId: string) {
  return daemonApi<{ presets: CommandPreset[] }>(`/api/servers/${serverId}/command`);
}

export function subscribeRuntime(serverId: string, handlers: RuntimeSubscription) {
  const url = new URL(daemonPath(`/api/servers/${encodeURIComponent(serverId)}/console`), window.location.href);
  if (handlers.includeUsage) url.searchParams.set("usage", "1");
  if (handlers.includeLogs === false) url.searchParams.set("logs", "0");
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  const socket = new WebSocket(url);
  const sendCommand = (command: string) => {
    if (socket.readyState !== WebSocket.OPEN) return false;
    socket.send(JSON.stringify({ type: "command", command }));
    return true;
  };
  socket.addEventListener("open", () => handlers.onCommandSender?.(sendCommand));
  socket.addEventListener("message", (event) => {
    const message = JSON.parse(String(event.data)) as {
      type: string;
      logs?: string[];
      status?: RuntimeStatus;
      error?: string;
      event?: { type: string; line?: string; status?: RuntimeStatus };
    };
    if (message.type === "snapshot") {
      handlers.onSnapshot({ runtime: normalizeRuntime(message.status ?? emptyRuntime()), logs: Array.isArray(message.logs) ? message.logs : undefined });
    }
    if (message.type === "event" && message.event?.type === "log" && message.event.line) {
      handlers.onLog(message.event.line);
    }
    if (message.type === "event" && message.event?.type === "status" && message.event.status) {
      handlers.onRuntime(normalizeRuntime(message.event.status));
    }
    if (message.type === "error" && message.error) {
      handlers.onError?.(message.error);
    }
  });
  return () => {
    handlers.onCommandSender?.(null);
    socket.close();
  };
}

function emptyRuntime(): RuntimeStatus {
  return { runningServerId: null, lifecycle: "stopped", pid: null, startedAt: null, uptimeSeconds: 0, command: "", launchTarget: "" };
}

export async function checkForUpdates(force = false): Promise<UpdateCheckResult> {
  const path = force ? "/api/updates/check?force=1" : "/api/updates/check";
  return daemonApi<UpdateCheckResult>(path);
}

export async function applyUpdate(): Promise<UpdateApplyResult> {
  return daemonApi<UpdateApplyResult>("/api/updates/apply", { method: "POST" });
}
