import type { ReactNode } from "react";

export type ServerType = "vanilla" | "paper" | "purpur" | "folia" | "fabric" | "forge" | "neoforge";
export type User = { id?: string; username: string };
export type AccessInfo = {
  lanAddresses: string[];
  devUrls: string[];
  productionUrls: string[];
};
export type StorageUsage = {
  rootExists: boolean;
  serverRootSizeBytes: number;
  registeredServerSizeBytes: number;
  snapshotsSizeBytes: number;
  backupCount: number;
  freeBytes: number | null;
  totalBytes: number | null;
  updatedAt: string;
};
export type Settings = {
  serverRoot: string;
  dataDir?: string;
  logFile?: string;
  snapshotsEnabled: boolean;
  curseForgeApiKey: string;
  storage?: StorageUsage;
  access?: AccessInfo;
};
export type JavaRuntimeInfo = {
  major: number;
  installed: boolean;
  path: string;
  required: boolean;
  label: string;
  usedBy: string[];
};
export type PlayitAgentInfo = {
  installed: boolean;
  path: string;
  version: string;
  asset: string;
  running: boolean;
  pid: number;
  claiming: boolean;
  claimUrl: string;
  startedAt: string;
  logs: string[];
  error: string;
  tunnels?: PlayitTunnelInfo[];
  platform?: string;
  deps?: PlayitDepStatus[];
  depsChecked?: boolean;
  depsInstall?: PlayitJobState | null;
  build?: PlayitJobState | null;
};
export type PlayitDepStatus = {
  name: string;
  label: string;
  installed: boolean;
  checking: boolean;
  installPath: string;
  installCommand: string;
};
export type PlayitJobState = {
  running: boolean;
  done: boolean;
  step: string;
  logs: string[];
  error: string;
};
export type PlayitTunnelInfo = {
  name: string;
  tunnelType: string;
  publicAddress: string;
  localIp: string;
  localPort: number;
  active: boolean;
};
export type PublicAccessRecord = {
  serverId: string;
  provider: string;
  publicAddress: string;
  localHost: string;
  localPort: number;
  agentPath: string;
  claimed: boolean;
  createdAt: string;
  updatedAt: string;
};
export type ServerRecord = {
  id: string;
  name: string;
  path: string;
  type: ServerType;
  minecraftVersion: string;
  loaderVersion: string;
  javaPath: string;
  minMemoryMb: number;
  maxMemoryMb: number;
  port: number;
  launchJar: string;
  extraArgs: string;
  snapshotsEnabled: boolean;
  scheduledSnapshotsEnabled: boolean;
  snapshotIntervalMinutes: number;
  lastScheduledSnapshotAt: string;
  createdAt: string;
  updatedAt: string;
};
export type ImportDetection = {
  token?: string;
  name: string;
  path: string;
  type: ServerType;
  minecraftVersion: string;
  loaderVersion: string;
  port: number;
  activeWorld: string;
  launchJar: string;
  alreadyRegistered: boolean;
  mods: number;
  disabledMods: number;
};
export type RuntimeUsageSample = {
  at: string;
  cpuPercent: number | null;
  memoryBytes: number | null;
};
export type PlayerSample = {
  at: string;
  count: number;
};
export type RuntimeUsage = {
  cpuPercent: number | null;
  memoryBytes: number | null;
  memoryLimitBytes: number | null;
  samples: RuntimeUsageSample[];
  playerSamples?: PlayerSample[];
  lastSampleAt?: string;
};
export type RuntimeStatus = {
  runningServerId: string | null;
  lifecycle: "stopped" | "starting" | "running" | "stopping";
  pid: number | null;
  startedAt: string | null;
  uptimeSeconds: number;
  command: string;
  launchTarget: string;
  usage?: RuntimeUsage;
  servers?: Record<string, RuntimeStatus>;
};
export type ModDependencyWarning = {
  projectId: string;
  versionId?: string;
  title: string;
  slug?: string;
  summary?: string;
  iconUrl?: string;
  versionNumber?: string;
};
export type ModMetadata = {
  source: "modrinth" | "modrinth-modpack" | "curseforge";
  projectId: string;
  slug?: string;
  title: string;
  author?: string;
  summary?: string;
  description?: string;
  iconUrl?: string;
  pageUrl?: string;
  versionId?: string;
  versionName?: string;
  versionNumber?: string;
  dependencyWarnings?: ModDependencyWarning[];
  installedAt: string;
};
export type ModFile = {
  fileName: string;
  path: string;
  enabled: boolean;
  size: number;
  updatedAt: string;
  metadata?: ModMetadata;
};
export type HealthCheck = {
  id: string;
  label: string;
  state: "ok" | "warn" | "error";
  detail: string;
};
export type ServerHealth = {
  status: "ready" | "attention" | "blocked";
  activeWorld: string;
  counts: {
    mods: number;
    disabledMods: number;
    worlds: number;
    datapacks: number;
    playerFiles: number;
  };
  checks: HealthCheck[];
};
export type MinecraftVersionOption = {
  id: string;
  type: "release" | "snapshot" | "old_beta" | "old_alpha";
  url: string;
  time: string;
  releaseTime: string;
};
export type LoaderOption = {
  version: string;
  stable?: boolean;
};
export type MinecraftMetadata = {
  fetchedAt: string;
  latest: {
    release: string;
    snapshot: string;
  };
  minecraftVersions: MinecraftVersionOption[];
  loaders: Record<ServerType, LoaderOption[]>;
  loaderCatalog: Record<ServerType, LoaderOption[]>;
};
export type Backup = { id: string; reason: string; snapshotPath: string; createdAt: string; sizeBytes: number };
export type FileEntry = {
  name: string;
  path: string;
  type: "directory" | "file";
  size: number;
  updatedAt: string;
  editable: boolean;
};
export type FileListing = { cwd: string; parent: string; entries: FileEntry[] };
export type FilePayload = { file: { name: string; path: string; size: number; editable: boolean; content: string } };
export type ServerProperties = {
  raw: Record<string, string>;
  eulaAccepted: boolean;
  editable: {
    motd: string;
    levelName: string;
    levelSeed: string;
    gamemode: string;
    difficulty: string;
    maxPlayers: number;
    serverPort: number;
    viewDistance: number;
    simulationDistance: number;
    onlineMode: boolean;
    whiteList: boolean;
    pvp: boolean;
    enableCommandBlock: boolean;
    allowFlight: boolean;
  };
};
export type ServerPropertiesEditable = ServerProperties["editable"];
export type WorldInfo = {
  name: string;
  active: boolean;
  path: string;
  updatedAt: string;
  playerFiles: number;
  datapacks: Array<{ name: string; size: number; updatedAt: string; enabled: boolean; metadata?: ModMetadata }>;
};
export type WorldsPayload = { activeWorld: string; worlds: WorldInfo[] };
export type ModSearchResult = {
  project_id?: string;
  projectId?: string;
  id?: string | number;
  title?: string;
  name?: string;
  description?: string;
  summary?: string;
  downloads?: number;
  downloadCount?: number;
  categories?: string[];
  slug?: string;
  project_type?: string;
  icon_url?: string;
  logo?: { thumbnailUrl?: string };
  author?: string;
  follows?: number;
  date_modified?: string;
  server_side?: "required" | "optional" | "unsupported" | "unknown" | string;
  client_side?: "required" | "optional" | "unsupported" | "unknown" | string;
  versions?: string[];
  license_id?: string;
};

export type ModrinthProjectDetails = {
  project: {
    id: string;
    slug: string;
    project_type?: string;
    title: string;
    description?: string;
    body?: string;
    icon_url?: string;
    downloads?: number;
    followers?: number;
    issues_url?: string;
    source_url?: string;
    wiki_url?: string;
    discord_url?: string;
    donation_urls?: Array<{ id?: string; platform?: string; url: string }>;
  };
  versions: ModrinthVersion[];
};

export type ModrinthVersion = {
  id: string;
  project_id: string;
  name: string;
  version_number: string;
  date_published: string;
  files: Array<{ primary: boolean; url: string; filename: string; size?: number }>;
};
export type PlayerAccess = {
  ops: Array<{ uuid: string; name: string; level: number; bypassesPlayerLimit: boolean }>;
  whitelist: Array<{ uuid: string; name: string }>;
  bannedPlayers: Array<{ uuid: string; name: string; created: string; source: string; expires: string; reason: string }>;
  bannedIps: Array<{ ip: string; created: string; source: string; expires: string; reason: string }>;
};
export type PlayerSession = { name: string; ip: string; lastJoinedAt: string };
export type PlayerLookup = { name: string; uuid: string };
export type FleetHealth = Record<string, Pick<ServerHealth, "status" | "counts">>;
export type ConfirmRequest = {
  title: string;
  message: ReactNode;
  confirmLabel: string;
  cancelLabel?: string;
  dangerous?: boolean;
  confirmDisabled?: boolean;
  disableBackdropCancel?: boolean;
  onConfirm: () => void | Promise<void>;
  onCancel?: () => void | Promise<void>;
};
export type UnsavedChangesRegistration = {
  id: string;
  label: string;
  dirty: boolean;
  message?: ReactNode;
  saveLabel?: string;
  discardLabel?: string;
  canSave?: boolean;
  onSave?: () => void | Promise<void>;
};
export type CommandPreset = { id: string; command: string; createdAt: string };

export type UpdateCheckResult = {
  currentVersion: string;
  currentCommit: string;
  latestVersion: string;
  latestCommit: string;
  updateAvailable: boolean;
  releaseUrl?: string;
  archiveName?: string;
  archiveSize?: number;
  builtAt?: string;
  checkedAt: string;
  error?: string;
};

export type UpdateApplyResult = {
  success: boolean;
  message: string;
  newVersion?: string;
  restarting: boolean;
};
