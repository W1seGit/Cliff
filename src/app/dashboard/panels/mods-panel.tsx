"use client";

import { useEffect, useRef, useState } from "react";
import {
  ChevronDown, ChevronRight, CircleCheck, Download, LayoutGrid, List, Package, PackagePlus, Puzzle, Search, SlidersHorizontal,
  TriangleAlert, Upload, Users,
} from "lucide-react";
import { compactNumber, formatBytes, serverTypeNeedsLoader, serverTypeNeedsPlugins } from "../lib/utils";
import {
  fetchModrinthProjectDetails, fetchServerWorlds, fetchWorldDatapackDetails, modUrl, runServerModAction,
  runWorldAction, searchServerMods, searchWorldDatapacks, uploadServerMod, uploadWorldFile, worldUrl,
} from "../lib/runtime-client";
import { VersionSelect } from "../components/version-select";
import type {
  ConfirmRequest, MinecraftMetadata, ModFile, ModSearchResult,
  ModrinthProjectDetails, ServerRecord, WorldInfo, WorldsPayload,
} from "../lib/types";
import { Button } from "../components/ui/button";
import { Panel } from "../components/ui/panel";
import { Input } from "../components/ui/input";
import { Select } from "../components/ui/select";
import { Table, SortableTh } from "../components/ui/table";
import { Hint } from "../components/ui/hint";
import { Pill } from "../components/ui/pill";
import { FilterBar } from "../components/ui/filter-bar";
import { SelectionBar } from "../components/ui/selection-bar";
import { Tabs } from "../components/ui/tabs";

type DependencyWarning = NonNullable<NonNullable<ModFile["metadata"]>["dependencyWarnings"]>[number];
type DiscoverSource = "marketplace" | "upload";
type SideFilter = "both" | "server" | "client";
type MarketFilter = "modrinth" | "curseforge";
type InstalledItem = {
  id: string;
  type: "mod" | "datapack" | "modpack";
  fileName: string;
  enabled: boolean;
  size: number;
  scope: string;
  metadata?: ModFile["metadata"];
  children?: InstalledItem[];
};

function modrinthUrl(project: ModrinthProjectDetails["project"] | ModSearchResult) {
  const slug = "slug" in project ? project.slug : undefined;
  const id = "project_id" in project ? project.project_id : "id" in project ? project.id : undefined;
  const type = "project_type" in project && (project.project_type === "datapack" || project.project_type === "modpack") ? project.project_type : "mod";
  return `https://modrinth.com/${type}/${slug || id || ""}`;
}

function searchResultKey(result: ModSearchResult) {
  return String(result.project_id ?? result.projectId ?? result.id ?? result.slug ?? result.title ?? result.name);
}

function searchResultProjectId(result: ModSearchResult) {
  const projectId = result.project_id ?? result.projectId;
  return projectId == null ? "" : String(projectId);
}

function appendUniqueResults(current: ModSearchResult[], next: ModSearchResult[]) {
  const seen = new Set(current.map(searchResultKey));
  return [...current, ...next.filter((result) => {
    const key = searchResultKey(result);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  })];
}

const modSearchPageSize = 30;

function ModIcon({ url, size = 40 }: { url?: string; size?: number }) {
  if (url) {
    return (
      // eslint-disable-next-line @next/next/no-img-element
      <img src={url} alt="" loading="lazy" style={{ width: size, height: size }} />
    );
  }
  return (
    <span className="mod-icon-fallback" style={{ width: size, height: size }}>
      <Package size={size * 0.5} />
    </span>
  );
}

type DiscoverFiltersState = {
  version: string;
  loader: string;
  category: string;
  content: "mod" | "modpack" | "datapack" | "plugin";
  sort: string;
  side: SideFilter;
  market: MarketFilter;
};

/** Shared filter form used by both the desktop sidebar and the mobile collapsible panel. */
function DiscoverFilters({
  filters,
  metadata,
  metadataError,
  worlds,
  selectedWorld,
  updateFilters,
  setSelectedWorld,
  pluginProfile,
}: {
  filters: DiscoverFiltersState;
  metadata: MinecraftMetadata | null;
  metadataError: string;
  worlds: WorldInfo[];
  selectedWorld: string;
  updateFilters: (updater: (current: DiscoverFiltersState) => DiscoverFiltersState) => void;
  setSelectedWorld: (value: string) => void;
  pluginProfile: boolean;
}) {
  return (
    <>
      <label className="discover-filter-field">
        <span>Content</span>
        <Select value={filters.content} onChange={(event) => updateFilters((current) => ({ ...current, content: event.target.value as typeof current.content }))}>
          {pluginProfile ? (
            <>
              <option value="plugin">Plugins</option>
              <option value="datapack">Datapacks</option>
            </>
          ) : (
            <>
              <option value="mod">Mods</option>
              <option value="modpack">Modpacks</option>
              <option value="datapack">Datapacks</option>
            </>
          )}
        </Select>
      </label>
      {filters.content === "datapack" && (
        <label className="discover-filter-field">
          <span>Target world</span>
          <Select value={selectedWorld} onChange={(event) => setSelectedWorld(event.target.value)}>
            {worlds.map((world) => <option key={world.name} value={world.name}>{world.name}</option>)}
          </Select>
        </label>
      )}
      <div className="discover-filter-field">
        <span>Version</span>
        <VersionSelect value={filters.version} metadata={metadata} metadataError={metadataError} onChange={(value) => updateFilters((current) => ({ ...current, version: value }))} />
      </div>
      {filters.content !== "datapack" && (
        <>
          <label className="discover-filter-field">
            <span>Platform</span>
            <Select value={filters.loader} onChange={(event) => updateFilters((current) => ({ ...current, loader: event.target.value }))}>
              <option value="">Any</option>
              {pluginProfile ? (
                <>
                  <option value="paper">Paper</option>
                  <option value="purpur">Purpur</option>
                  <option value="folia">Folia</option>
                  <option value="spigot">Spigot</option>
                  <option value="bukkit">Bukkit</option>
                </>
              ) : (
                <>
                  <option value="paper">Paper</option>
                  <option value="purpur">Purpur</option>
                  <option value="folia">Folia</option>
                  <option value="fabric">Fabric</option>
                  <option value="forge">Forge</option>
                  <option value="neoforge">NeoForge</option>
                  <option value="quilt">Quilt</option>
                </>
              )}
            </Select>
          </label>
          <label className="discover-filter-field">
            <span>Category</span>
            <Select value={filters.category} onChange={(event) => updateFilters((current) => ({ ...current, category: event.target.value }))}>
              <option value="">Any</option>
              <option value="adventure">Adventure</option>
              <option value="magic">Magic</option>
              <option value="optimization">Optimization</option>
              <option value="utility">Utility</option>
              <option value="decoration">Decoration</option>
              <option value="technology">Technology</option>
              <option value="worldgen">Worldgen</option>
              <option value="food">Food</option>
              <option value="equipment">Equipment</option>
              <option value="library">Library</option>
            </Select>
          </label>
        </>
      )}
      <label className="discover-filter-field">
        <span>Sort</span>
        <Select value={filters.sort} onChange={(event) => updateFilters((current) => ({ ...current, sort: event.target.value }))}>
          <option value="relevance">Relevance</option>
          <option value="downloads">Popular</option>
          <option value="popularity">Trending</option>
          <option value="updated">Recently updated</option>
        </Select>
      </label>
      {filters.content !== "datapack" && (
        <label className="discover-filter-field">
          <span>Side</span>
          <Select value={filters.side} onChange={(event) => updateFilters((current) => ({ ...current, side: event.target.value as SideFilter }))}>
            <option value="both">Client/Server</option>
            <option value="server">Server</option>
            <option value="client">Client</option>
          </Select>
        </label>
      )}
    </>
  );
}

export function ModsPanel({
  server,
  mods,
  metadata,
  metadataError,
  isRunning,
  view,
  onRefresh,
  onMessage,
  onConfirm,
  onNavigateDiscover,
}: {
  server: ServerRecord;
  mods: ModFile[];
  metadata: MinecraftMetadata | null;
  metadataError: string;
  isRunning: boolean;
  view: "installed" | "discover";
  onRefresh: () => void;
  onMessage: (message: string) => void;
  onConfirm: (request: ConfirmRequest) => void;
  onNavigateDiscover: () => void;
}) {
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<ModSearchResult[]>([]);
  const [busyId, setBusyId] = useState("");
  const [uploading, setUploading] = useState(false);
  const [searching, setSearching] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [hasMoreResults, setHasMoreResults] = useState(false);
  const [nextResultOffset, setNextResultOffset] = useState(0);
  const [uploadFile, setUploadFile] = useState<File | null>(null);
  const [uploadType, setUploadType] = useState<"mod" | "datapack" | null>(null);
  const [uploadDragActive, setUploadDragActive] = useState(false);
  const [installedQuery, setInstalledQuery] = useState("");
  const [installedType, setInstalledType] = useState<string>("");
  const [installedStatus, setInstalledStatus] = useState<string>("");
  const [installedSortKey, setInstalledSortKey] = useState<"file" | "type" | "status" | "size" | null>(null);
  const [installedSortDir, setInstalledSortDir] = useState<"asc" | "desc">("asc");
  const [selectedInstalled, setSelectedInstalled] = useState<string[]>([]);
  const [installedPage, setInstalledPage] = useState(0);
  const [expandedModpacks, setExpandedModpacks] = useState<Set<string>>(new Set());
  const INSTALLED_PAGE_SIZE = 30;
  const [source, setSource] = useState<DiscoverSource>(() => {
    if (typeof window === "undefined" || view !== "discover") return "marketplace";
    const hash = window.location.hash.replace("#", "");
    return hash === "upload" ? "upload" : "marketplace";
  });
  const [viewMode, setViewMode] = useState<"list" | "grid">("list");
  const [mobileFiltersOpen, setMobileFiltersOpen] = useState(false);
  const [worldsData, setWorldsData] = useState<WorldsPayload | null>(null);
  const [selectedWorld, setSelectedWorld] = useState("");
  const [filters, setFilters] = useState({
    version: server.minecraftVersion,
    loader: server.type === "vanilla" ? "" : server.type,
    category: "",
    content: (serverTypeNeedsPlugins(server.type) ? "plugin" : "mod") as "mod" | "modpack" | "datapack" | "plugin",
    sort: "downloads",
    side: "server" as SideFilter,
    market: "modrinth" as MarketFilter,
  });
  const activeSearchKeyRef = useRef("");
  const blockedMoreKeyRef = useRef("");
  const loadingMoreRef = useRef(false);
  const resultListRef = useRef<HTMLDivElement | null>(null);
  const uploadInputRef = useRef<HTMLInputElement | null>(null);

  const busy = Boolean(busyId) || uploading || searching || loadingMore;
  const vanillaProfile = !serverTypeNeedsLoader(server.type) && !serverTypeNeedsPlugins(server.type);
  const pluginProfile = serverTypeNeedsPlugins(server.type);
  const worlds = worldsData?.worlds ?? [];
  const mobileFilterCount = [
    filters.version !== server.minecraftVersion,
    filters.loader !== (server.type === "vanilla" ? "" : server.type),
    filters.category !== "",
    filters.content !== (pluginProfile ? "plugin" : "mod"),
    filters.sort !== "downloads",
    filters.side !== "server",
  ].filter(Boolean).length;
  const modItems: InstalledItem[] = mods.map((mod) => ({
    id: `mod:${mod.enabled ? "enabled" : "disabled"}:${mod.fileName}`,
    type: "mod" as const,
    fileName: mod.fileName,
    enabled: mod.enabled,
    size: mod.size,
    scope: "Server",
    metadata: mod.metadata,
  }));
  const modpackGroups = new Map<string, InstalledItem[]>();
  const standaloneMods: InstalledItem[] = [];
  for (const item of modItems) {
    if (item.metadata?.source === "modrinth-modpack" && item.metadata.projectId) {
      const key = item.metadata.projectId;
      modpackGroups.set(key, [...(modpackGroups.get(key) ?? []), item]);
    } else {
      standaloneMods.push(item);
    }
  }
  const modpackItems: InstalledItem[] = Array.from(modpackGroups.entries()).map(([projectId, children]) => {
    const first = children[0];
    const allEnabled = children.every((child) => child.enabled);
    const anyEnabled = children.some((child) => child.enabled);
    return {
      id: `modpack:${projectId}`,
      type: "modpack" as const,
      fileName: first.metadata?.title ?? first.fileName,
      enabled: allEnabled || anyEnabled,
      size: children.reduce((sum, child) => sum + child.size, 0),
      scope: `${children.length} files`,
      metadata: first.metadata,
      children,
    };
  });
  const installedContent: InstalledItem[] = [
    ...modpackItems,
    ...standaloneMods,
    ...worlds.flatMap((world) => world.datapacks.map((pack) => ({
      id: `datapack:${world.name}:${pack.name}`,
      type: "datapack" as const,
      fileName: pack.name,
      enabled: pack.enabled,
      size: pack.size,
      scope: world.name,
      metadata: pack.metadata,
    }))),
  ];
  const filteredContent = installedContent
    .filter((item) => {
      const q = installedQuery.trim().toLowerCase();
      const matchesQuery = !q || item.fileName.toLowerCase().includes(q) || item.scope.toLowerCase().includes(q) || (item.metadata?.title?.toLowerCase().includes(q) ?? false);
      const typeFilters = installedType ? installedType.split(",") : [];
      const matchesType = typeFilters.length === 0 || typeFilters.includes(item.type);
      const statusFilters = installedStatus ? installedStatus.split(",") : [];
      const matchesStatus = statusFilters.length === 0 ||
        (statusFilters.includes("enabled") && item.enabled) ||
        (statusFilters.includes("disabled") && !item.enabled);
      return matchesQuery && matchesType && matchesStatus;
    })
    .toSorted((a, b) => {
      if (installedSortKey === null) {
        return a.fileName.localeCompare(b.fileName);
      }
      let cmp = 0;
      if (installedSortKey === "type") cmp = a.type.localeCompare(b.type);
      else if (installedSortKey === "status") cmp = Number(a.enabled) - Number(b.enabled);
      else if (installedSortKey === "size") cmp = a.size - b.size;
      else cmp = a.fileName.localeCompare(b.fileName);
      if (cmp === 0) cmp = a.fileName.localeCompare(b.fileName);
      return installedSortDir === "asc" ? cmp : -cmp;
    });
  const selectedContent = installedContent.filter((item) => selectedInstalled.includes(item.id));
  const allFilteredSelected = filteredContent.length > 0 && filteredContent.every((item) => selectedInstalled.includes(item.id));
  const installedVisibleCount = (installedPage + 1) * INSTALLED_PAGE_SIZE;
  const pagedContent = filteredContent.slice(0, installedVisibleCount);
  const hasMoreInstalled = filteredContent.length > installedVisibleCount;
  const visibleResults = results;
  const installedProjectStatus = new Map<string, string>();
  for (const mod of mods) {
    if ((mod.metadata?.source === "modrinth" || mod.metadata?.source === "modrinth-modpack") && mod.metadata.projectId) {
      installedProjectStatus.set(mod.metadata.projectId, mod.enabled ? "enabled" : "disabled");
    }
  }
  for (const world of worlds) {
    for (const pack of world.datapacks) {
      if (pack.metadata?.source === "modrinth" && pack.metadata.projectId) {
        installedProjectStatus.set(pack.metadata.projectId, pack.enabled ? "enabled" : "disabled");
      }
    }
  }

  function handleInstalledSort(key: string) {
    const k = key as "file" | "type" | "status" | "size";
    setInstalledPage(0);
    if (installedSortKey === null) {
      setInstalledSortKey(k);
      setInstalledSortDir("asc");
    } else if (installedSortKey === k) {
      if (installedSortDir === "asc") {
        setInstalledSortDir("desc");
      } else {
        setInstalledSortKey(null);
        setInstalledSortDir("asc");
      }
    } else {
      setInstalledSortKey(k);
      setInstalledSortDir("asc");
    }
  }

  useEffect(() => {
    if (view === "discover" && source !== "upload" && results.length === 0 && !query) {
      search(true, "", false, true);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [view, source, server.id]);

  // Sync source from URL hash on back/forward navigation
  useEffect(() => {
    if (view !== "discover") return;
    const onHashChange = () => {
      const hash = window.location.hash.replace("#", "");
      const next = hash === "upload" ? "upload" : "marketplace";
      setSource((prev) => prev !== next ? next : prev);
    };
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, [view]);

  useEffect(() => {
    if (view !== "discover" || source === "upload") return;
    search(true, query.trim(), false, true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filters, view, selectedWorld]);

  useEffect(() => {
    loadWorlds().catch((error) => onMessage(error.message));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [server.id]);

  async function loadWorlds() {
    const payload = await fetchServerWorlds(server.id);
    setWorldsData(payload);
    setSelectedWorld((current) => (current && payload.worlds.some((world) => world.name === current) ? current : payload.activeWorld || payload.worlds[0]?.name || ""));
  }

  function updateFilters(updater: (current: typeof filters) => typeof filters) {
    setResults([]);
    setHasMoreResults(false);
    setNextResultOffset(0);
    activeSearchKeyRef.current = "";
    blockedMoreKeyRef.current = "";
    setFilters(updater);
  }

  function preserveResultScroll(scrollTop: number | null) {
    if (scrollTop == null) return;
    window.setTimeout(() => {
      window.requestAnimationFrame(() => {
        if (resultListRef.current) resultListRef.current.scrollTop = scrollTop;
      });
    }, 0);
  }

  async function runModAction(body: Record<string, unknown>) {
    setBusyId(`${body.action ?? "mod"}:${body.fileName ?? body.projectId ?? body.modId ?? "action"}`);
    try {
      const data = await runServerModAction(server.id, body);
      if (data.files?.length) onMessage(`Installed ${data.files.join(", ")}`);
      await onRefresh();
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Mod action failed");
    } finally {
      setBusyId("");
    }
  }

  function dependencyPromptContent(dependencies: DependencyWarning[]) {
    return (
      <div className="dependency-prompt">
        <p>These required dependencies are needed for the mod to load correctly. If you decline, startup or gameplay errors may occur.</p>
        <div className="dependency-prompt-list">
          {dependencies.map((dependency) => (
            <div className="dependency-prompt-row" key={dependency.projectId}>
              {dependency.iconUrl ? (
                // eslint-disable-next-line @next/next/no-img-element
                <img src={dependency.iconUrl} alt="" loading="lazy" />
              ) : (
                <span className="installed-mod-fallback"><PackagePlus size={16} /></span>
              )}
              <div>
                <a href={`https://modrinth.com/mod/${dependency.slug || dependency.projectId}`} target="_blank" rel="noreferrer">{dependency.title}</a>
                <small>Modrinth{dependency.versionNumber ? ` / ${dependency.versionNumber}` : ""}</small>
              </div>
            </div>
          ))}
        </div>
      </div>
    );
  }

  async function installModrinthWithDependencyPrompt(projectId: string, versionId: string, busyKey: string) {
    if (busy) return;
    setBusyId(busyKey);
    try {
      const plan = await runServerModAction(server.id, { action: "modrinth-install-plan", projectId, versionId });
      const dependencies = plan.dependencies ?? [];
      if (dependencies.length === 0) {
        await runModAction({ action: "install-modrinth", projectId, versionId, includeDependencies: false });
        return;
      }
      setBusyId("");
      onConfirm({
        title: "Install required dependencies?",
        message: dependencyPromptContent(dependencies),
        confirmLabel: "Install all",
        cancelLabel: "Install without dependencies",
        onConfirm: () => runModAction({ action: "install-modrinth", projectId, versionId, includeDependencies: true }),
        onCancel: () => runModAction({ action: "install-modrinth", projectId, versionId, includeDependencies: false, dependencyWarnings: dependencies }),
      });
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Could not check Modrinth dependencies");
      setBusyId("");
    }
  }

  async function promptMissingDependencies(fileName: string, dependencies: DependencyWarning[]) {
    if (busy) return;
    setBusyId(`dependencies:${fileName}`);
    let detailedDependencies = dependencies;
    try {
      const data = await runServerModAction(server.id, { action: "modrinth-dependency-details", dependencies });
      detailedDependencies = data.dependencies ?? dependencies;
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Could not load dependency details");
    } finally {
      setBusyId("");
    }
    onConfirm({
      title: "Install missing dependencies?",
      message: dependencyPromptContent(detailedDependencies),
      confirmLabel: "Install dependencies",
      onConfirm: () => runModAction({ action: "install-modrinth-dependencies", fileName, dependencies: detailedDependencies }),
    });
  }

  function activeSearchSource(): string {
    if (filters.content === "modpack") return "modrinth-pack";
    return "modrinth";
  }

  async function search(allowEmpty = false, searchTerm?: string, append = false, force = false) {
    if (!force && (append ? loadingMoreRef.current || loadingMore : searching)) return;
    if (source === "upload") return;
    const term = (searchTerm ?? query).trim();
    if (!allowEmpty && !term) return;
    const offset = append ? nextResultOffset : 0;
    const appendScrollTop = append ? resultListRef.current?.scrollTop ?? null : null;
    let requestKey = "";
    let started = false;
    try {
      if (filters.content === "datapack") {
        const params = new URLSearchParams({
          q: term,
          source: "modrinth-datapack",
          version: filters.version,
          sort: filters.sort,
          limit: String(modSearchPageSize),
          offset: String(offset),
        });
        requestKey = `worlds:${params.toString()}`;
        if (activeSearchKeyRef.current === requestKey || (append && blockedMoreKeyRef.current === requestKey)) return;
        activeSearchKeyRef.current = requestKey;
        started = true;
        if (append) {
          loadingMoreRef.current = true;
          setLoadingMore(true);
        } else {
          setResults([]);
          setSearching(true);
          setHasMoreResults(false);
          blockedMoreKeyRef.current = "";
        }
        const data = await searchWorldDatapacks(server.id, params.toString());
        const nextResults = data.results ?? [];
        if (activeSearchKeyRef.current === requestKey) {
          setResults((current) => append ? appendUniqueResults(current, nextResults) : nextResults);
          setHasMoreResults(nextResults.length >= modSearchPageSize);
          setNextResultOffset(offset + modSearchPageSize);
          if (append) preserveResultScroll(appendScrollTop);
          if (blockedMoreKeyRef.current === requestKey) blockedMoreKeyRef.current = "";
        }
        return;
      }
      const searchSource = activeSearchSource();
      const params = new URLSearchParams({
        q: term,
        source: searchSource,
        version: filters.version,
        loader: filters.loader,
        sort: filters.sort,
        side: filters.side,
        limit: String(modSearchPageSize),
        offset: String(offset),
      });
      if (filters.content === "plugin") params.set("projectType", "plugin");
      if (filters.category) params.set("category", filters.category);
      requestKey = `mods:${params.toString()}`;
      if (activeSearchKeyRef.current === requestKey || (append && blockedMoreKeyRef.current === requestKey)) return;
      activeSearchKeyRef.current = requestKey;
      started = true;
        if (append) {
          loadingMoreRef.current = true;
          setLoadingMore(true);
        } else {
          setResults([]);
          setSearching(true);
          setHasMoreResults(false);
          blockedMoreKeyRef.current = "";
        }
      const data = await searchServerMods(server.id, params.toString());
      if (data.disabled) onMessage("Marketplace search is not available.");
      const nextResults = data.results ?? [];
      if (activeSearchKeyRef.current === requestKey) {
        setResults((current) => append ? appendUniqueResults(current, nextResults) : nextResults);
        setHasMoreResults(nextResults.length >= modSearchPageSize);
        setNextResultOffset(data.nextOffset ?? offset + modSearchPageSize);
        if (append) preserveResultScroll(appendScrollTop);
        if (blockedMoreKeyRef.current === requestKey) blockedMoreKeyRef.current = "";
      }
    } catch (error) {
      if (started && activeSearchKeyRef.current === requestKey) {
        if (append) {
          blockedMoreKeyRef.current = requestKey;
          setHasMoreResults(false);
        }
        onMessage(error instanceof Error ? error.message : "Search failed");
      }
    } finally {
      if (started && activeSearchKeyRef.current === requestKey) {
        activeSearchKeyRef.current = "";
        if (append) {
          loadingMoreRef.current = false;
          setLoadingMore(false);
        } else setSearching(false);
      }
    }
  }

  function loadMoreResults() {
    if (!hasMoreResults || searching || loadingMoreRef.current || loadingMore || source === "upload") return;
    search(true, query.trim(), true);
  }

  async function datapackAction(body: Record<string, string>) {
    if (busy) return;
    setBusyId(`${body.action}:${body.versionId || body.fileName || body.projectId || "datapack"}`);
    try {
      const data = await runWorldAction(server.id, body);
      setWorldsData(data);
      if (data.files?.length) onMessage(`Installed ${data.files.join(", ")}`);
      else onMessage("Datapack updated");
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Datapack action failed");
    } finally {
      setBusyId("");
    }
  }

  async function installDatapack(projectId: string, versionId = "") {
    await datapackAction({ action: "install-modrinth-datapack", worldName: selectedWorld, projectId, versionId });
  }

  async function bulkInstalledAction(action: "enable" | "disable" | "delete") {
    if (busy || selectedContent.length === 0) return;
    setBusyId(`bulk:${action}`);
    try {
      const selectedMods = selectedContent.flatMap((item) => item.type === "modpack" ? item.children ?? [] : item.type === "mod" ? [item] : []);
      if (selectedMods.length > 0) {
        await runServerModAction(server.id, {
          action: action === "enable" ? "enable-selected" : action === "disable" ? "disable-selected" : "delete-selected",
          mods: selectedMods.map((item) => ({ fileName: item.fileName, enabled: item.enabled })),
        });
      }
      const datapacksByWorld = new Map<string, string[]>();
      for (const item of selectedContent.filter((entry) => entry.type === "datapack")) {
        datapacksByWorld.set(item.scope, [...(datapacksByWorld.get(item.scope) ?? []), item.fileName]);
      }
      for (const [worldName, fileNames] of datapacksByWorld) {
        await runWorldAction(server.id, {
          action: action === "enable" ? "enable-selected-datapacks" : action === "disable" ? "disable-selected-datapacks" : "delete-selected-datapacks",
          worldName,
          fileNames,
        });
      }
      setSelectedInstalled([]);
      await loadWorlds();
      await onRefresh();
      onMessage(`${action === "delete" ? "Deleted" : action === "enable" ? "Enabled" : "Disabled"} selected content`);
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Bulk action failed");
    } finally {
      setBusyId("");
    }
  }

  async function installFromCard(result: ModSearchResult) {
    const projectId = result.project_id ?? result.projectId;
    if (!projectId) return;
    const busyKey = `card-install:${projectId}`;
    setBusyId(busyKey);
    try {
      if (filters.content === "modpack") {
        const data = await runServerModAction(server.id, { action: "install-modrinth-modpack", projectId: String(projectId) });
        if (data.files?.length) onMessage(`Installed modpack content (${data.files.length} files)`);
        else onMessage("Modpack installed");
        await onRefresh();
        return;
      }
      const params = filters.content === "datapack"
        ? new URLSearchParams({
          projectId: String(projectId),
          source: "modrinth-datapack",
          details: "1",
          version: filters.version,
        })
        : new URLSearchParams({
          projectId: String(projectId),
          source: "modrinth",
          details: "1",
          version: filters.version,
          loader: filters.loader,
        });
      if (filters.content === "plugin") params.set("projectType", "plugin");
      const data = filters.content === "datapack"
        ? await fetchWorldDatapackDetails(server.id, params.toString())
        : await fetchModrinthProjectDetails(server.id, params.toString());
      const version = data.versions?.[0];
      if (!version) {
        onMessage(`No compatible versions found for ${filters.version}`);
        return;
      }
      const pid = String(projectId);
      if (filters.content === "datapack") installDatapack(pid, version.id);
      else installModrinthWithDependencyPrompt(pid, version.id, version.id);
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Failed to load versions for install");
    } finally {
      setBusyId((current) => current === busyKey ? "" : current);
    }
  }

  async function uploadMod() {
    if (!uploadFile || busy || !uploadType) return;
    setUploading(true);
    try {
      const form = new FormData();
      const uploadingDatapack = uploadType === "datapack";
      form.set("action", uploadingDatapack ? "upload-datapack" : "upload");
      if (uploadingDatapack) form.set("worldName", selectedWorld);
      form.set("file", uploadFile);
      const data = uploadingDatapack ? await uploadWorldFile(server.id, form) : await uploadServerMod(server.id, form);
      setUploadFile(null);
      setUploadType(null);
      if ("files" in data && data.files?.length) onMessage(`Uploaded ${data.files.join(", ")}`);
      else onMessage(uploadingDatapack ? "Uploaded datapack" : `Uploaded ${pluginProfile ? "plugin" : "mod"}`);
      if (uploadingDatapack) setWorldsData(data as WorldsPayload);
      else await onRefresh();
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Upload failed");
    } finally {
      setUploading(false);
    }
  }

  function handleUploadFileSelected(file: File | null) {
    if (!file) {
      setUploadFile(null);
      setUploadType(null);
      return;
    }
    const isZip = file.name.toLowerCase().endsWith(".zip");
    const isJar = file.name.toLowerCase().endsWith(".jar");
    setUploadFile(file);
    setUploadType(isZip ? "datapack" : isJar ? "mod" : null);
  }

  function handleUploadDrop(event: React.DragEvent) {
    event.preventDefault();
    event.stopPropagation();
    setUploadDragActive(false);
    if (busy) return;
    const file = event.dataTransfer.files?.[0] ?? null;
    handleUploadFileSelected(file);
  }

  function switchSource(next: DiscoverSource) {
    if (next === source) return;
    setSource(next);
    if (typeof window !== "undefined" && view === "discover") {
      window.history.replaceState(null, "", next === "marketplace" ? window.location.pathname : `${window.location.pathname}#${next}`);
    }
  }

  const sourceLabel: Record<DiscoverSource, string> = {
    marketplace: "Marketplace",
    upload: "Upload",
  };

  return (
    <section className="mods-workspace">
      <div className="mods-page-header">
        <div className="mods-page-title">
          <h1><span className="workspace-page-icon"><Puzzle /></span>{pluginProfile ? "Plugins" : "Mods / Plugins"}</h1>
          <p className="mods-page-subtitle">{pluginProfile ? "Install, enable, and discover plugins and datapacks." : "Install, enable, and discover mods and datapacks."}</p>
        </div>
      </div>

      {vanillaProfile && (
        <Hint variant="source" warn>
          <span>This server type does not load Fabric, Forge, or NeoForge mods. Datapacks still work.</span>
        </Hint>
      )}
      {isRunning && (
        <Hint variant="source" warn>
          <span>Server is running — mod and datapack deletion is disabled. Restart the server for mod changes to take effect.</span>
        </Hint>
      )}

      {view === "installed" && (
        <Panel className="mods-list-panel">
          <FilterBar
            fields={[
              {
                key: "search",
                label: "Search installed content",
                type: "text",
                placeholder: "Search by file name or world",
                value: installedQuery,
                onChange: (value) => {
                  setInstalledQuery(value);
                  setInstalledPage(0);
                },
              },
              {
                key: "type",
                label: "Type",
                type: "select",
                multi: true,
                value: installedType,
                onChange: (value) => {
                  setInstalledType(value);
                  setInstalledPage(0);
                },
                options: pluginProfile ? [
                  { value: "all", label: "All" },
                  { value: "mod", label: "Plugins" },
                  { value: "datapack", label: "Datapacks" },
                ] : [
                  { value: "all", label: "All" },
                  { value: "mod", label: "Mods" },
                  { value: "modpack", label: "Modpacks" },
                  { value: "datapack", label: "Datapacks" },
                ],
              },
              {
                key: "status",
                label: "Status",
                type: "select",
                multi: true,
                value: installedStatus,
                onChange: (value) => {
                  setInstalledStatus(value);
                  setInstalledPage(0);
                },
                options: [
                  { value: "all", label: "All" },
                  { value: "enabled", label: "Enabled" },
                  { value: "disabled", label: "Disabled" },
                ],
              },
            ]}
            actions={<Button variant="primary" onClick={onNavigateDiscover}><PackagePlus size={14} />{pluginProfile ? "Add plugins" : "Add mods"}</Button>}
          />

          {selectedInstalled.length > 0 && (
            <SelectionBar
              selectedCount={selectedInstalled.length}
              actions={[
                { label: "Enable selected", disabled: busy, onClick: () => bulkInstalledAction("enable") },
                { label: "Disable selected", disabled: busy, onClick: () => bulkInstalledAction("disable") },
                { label: "Download", disabled: selectedInstalled.length !== 1, onClick: () => {
                  const item = installedContent.find((i) => selectedInstalled.includes(i.id));
                  const downloadTarget = item?.type === "modpack" ? item.children?.[0] : item;
                  if (downloadTarget) window.open(downloadTarget.type === "mod"
                    ? modUrl(server.id, `?download=${encodeURIComponent(downloadTarget.fileName)}&enabled=${downloadTarget.enabled ? "1" : "0"}`)
                    : worldUrl(server.id, `?world=${encodeURIComponent(downloadTarget.scope)}&datapack=${encodeURIComponent(downloadTarget.fileName)}`), "_blank");
                }},
                {
                  label: "Delete selected",
                  variant: "danger",
                  disabled: busy || isRunning,
                  onClick: () => onConfirm({
                    title: "Delete selected content",
                    message: `${selectedInstalled.length} selected item${selectedInstalled.length === 1 ? "" : "s"} will be removed.`,
                    confirmLabel: "Delete selected",
                    dangerous: true,
                    onConfirm: () => bulkInstalledAction("delete"),
                  }),
                },
              ]}
            />
          )}
          <Table wrapperClassName="mods-installed-table">
            <colgroup>
              <col style={{ width: "40px" }} />
              <col style={{ width: "90px" }} />
              <col />
              <col style={{ width: "120px" }} />
              <col style={{ width: "100px" }} />
              <col style={{ width: "90px" }} />
              <col style={{ width: "60px" }} />
            </colgroup>
            <thead>
              <tr><th><Input type="checkbox" aria-label="Select all installed content" checked={allFilteredSelected} onChange={(event) => setSelectedInstalled(event.target.checked ? filteredContent.map((item) => item.id) : [])} /></th><SortableTh label="Type" sortKey="type" activeSort={installedSortKey} sortDir={installedSortDir} onSort={handleInstalledSort} /><SortableTh label="File" sortKey="file" activeSort={installedSortKey} sortDir={installedSortDir} onSort={handleInstalledSort} /><th>Scope</th><SortableTh label="Status" sortKey="status" activeSort={installedSortKey} sortDir={installedSortDir} onSort={handleInstalledSort} /><SortableTh label="Size" sortKey="size" activeSort={installedSortKey} sortDir={installedSortDir} onSort={handleInstalledSort} /><th><span className="table-count">{filteredContent.length} of {installedContent.length}</span></th></tr>
            </thead>
            <tbody>
              {pagedContent.flatMap((item) => {
                const isExpanded = expandedModpacks.has(item.id);
                const rows = [
                  <tr key={item.id}>
                    <td><Input type="checkbox" aria-label={`Select ${item.fileName}`} checked={selectedInstalled.includes(item.id)} onChange={(event) => setSelectedInstalled((current) => event.target.checked ? [...current, item.id] : current.filter((id) => id !== item.id))} /></td>
                    <td><Pill variant={item.type === "mod" ? "success" : item.type === "modpack" ? "accent" : "warning"}>{item.type}</Pill></td>
                    <td>
                      <div className="installed-mod-cell">
                        {item.type === "modpack" && item.children?.length ? (
                          <button
                            type="button"
                            className="modpack-expand-button"
                            aria-label={isExpanded ? "Collapse modpack files" : "Expand modpack files"}
                            onClick={() => setExpandedModpacks((current) => {
                              const next = new Set(current);
                              if (next.has(item.id)) next.delete(item.id);
                              else next.add(item.id);
                              return next;
                            })}
                          >
                            {isExpanded ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
                          </button>
                        ) : null}
                        {item.metadata?.iconUrl ? (
                          // eslint-disable-next-line @next/next/no-img-element
                          <img src={item.metadata.iconUrl} alt="" loading="lazy" />
                        ) : (
                          <span className="installed-mod-fallback"><PackagePlus size={16} /></span>
                        )}
                        <span>
                          <span className="installed-mod-title-row">
                            {item.metadata?.pageUrl ? (
                              <a href={item.metadata.pageUrl} target="_blank" rel="noreferrer">{item.metadata.title}</a>
                            ) : (
                              <strong>{item.metadata?.title ?? item.fileName}</strong>
                            )}
                            {item.type === "mod" && item.metadata?.dependencyWarnings?.length ? (
                              <Button
                                type="button"
                                className="dependency-warning-button"
                                title={`Missing required dependencies: ${item.metadata.dependencyWarnings.map((dependency) => dependency.title).join(", ")}`}
                                aria-label={`Install missing dependencies for ${item.metadata.title}`}
                                disabled={busy}
                                onClick={() => promptMissingDependencies(item.fileName, item.metadata?.dependencyWarnings ?? [])}
                              >
                                <TriangleAlert size={15} />
                              </Button>
                            ) : null}
                          </span>
                          <small>{item.metadata ? `${item.metadata.source}${item.metadata.versionNumber ? ` / ${item.metadata.versionNumber}` : ""}` : item.fileName}</small>
                        </span>
                      </div>
                    </td>
                    <td>{item.scope}</td>
                    <td><Pill variant={item.enabled ? "success" : "default"}>{item.enabled ? "enabled" : "disabled"}</Pill></td>
                    <td>{formatBytes(item.size)}</td>
                    <td></td>
                  </tr>,
                ];
                if (isExpanded && item.type === "modpack" && item.children?.length) {
                  for (const child of item.children) {
                    rows.push(
                      <tr key={child.id} className="modpack-child-row">
                        <td></td>
                        <td></td>
                        <td>
                          <div className="installed-mod-cell modpack-child-cell">
                            <span className="installed-mod-fallback small"><Package size={14} /></span>
                            <span>
                              <strong>{child.fileName}</strong>
                              <small>{child.metadata?.versionNumber ?? child.fileName}</small>
                            </span>
                          </div>
                        </td>
                        <td>{child.scope}</td>
                        <td><Pill variant={child.enabled ? "success" : "default"}>{child.enabled ? "enabled" : "disabled"}</Pill></td>
                        <td>{formatBytes(child.size)}</td>
                        <td></td>
                      </tr>,
                    );
                  }
                }
                return rows;
              })}
              {installedContent.length === 0 && <tr><td colSpan={7} className="muted">No {pluginProfile ? "plugins" : "mods"} or datapacks yet. Switch to Discover to add some.</td></tr>}
              {installedContent.length > 0 && filteredContent.length === 0 && <tr><td colSpan={7} className="muted">No installed content matches.</td></tr>}
              {hasMoreInstalled && (
                <tr><td colSpan={7} className="table-load-more"><Button onClick={() => setInstalledPage((p) => p + 1)}>Load more ({filteredContent.length - installedVisibleCount} remaining)</Button></td></tr>
              )}
              </tbody>
            </Table>
          </Panel>
      )}

      {view === "discover" && (
        <div className="mods-discover">
          <Tabs
            items={[
              { id: "marketplace", label: "Marketplace" },
              { id: "upload", label: "Upload" },
            ]}
            activeId={source}
            onChange={(id) => switchSource(id as DiscoverSource)}
          />

          {source === "upload" && (
            <div className="discover-upload">
              <div className="discover-upload-head">
                <div>
                  <h2>Upload</h2>
                  <p className="muted">Add a local .jar {pluginProfile ? "plugin" : "mod"} or .zip datapack to this server.</p>
                </div>
                <Button variant="primary" className="icon-button" disabled={!uploadFile || !uploadType || busy || (uploadType === "datapack" && !selectedWorld)} onClick={uploadMod}><Upload size={16} />{uploading ? "Uploading..." : uploadType ? `Upload ${uploadType === "mod" && pluginProfile ? "plugin" : uploadType}` : "Upload"}</Button>
              </div>
              {uploadType === "datapack" && (
                <label className="discover-filter-field">
                  <span>Target world</span>
                  <Select value={selectedWorld} onChange={(event) => setSelectedWorld(event.target.value)}>
                    {worlds.map((world) => <option key={world.name} value={world.name}>{world.name}</option>)}
                  </Select>
                </label>
              )}
              <div
                className={`mod-upload-drop ${uploadDragActive ? "drag-active" : ""}`}
                onDragOver={(e) => { e.preventDefault(); e.stopPropagation(); if (!busy) setUploadDragActive(true); }}
                onDragLeave={(e) => { e.preventDefault(); e.stopPropagation(); setUploadDragActive(false); }}
                onDrop={handleUploadDrop}
                onClick={() => { if (!busy) uploadInputRef.current?.click(); }}
              >
                <Input ref={uploadInputRef} type="file" accept=".jar,.zip" disabled={busy} onChange={(event) => handleUploadFileSelected(event.target.files?.[0] ?? null)} />
                <strong>{uploadFile ? uploadFile.name : "Drag and drop a .jar or .zip"}</strong>
                <span className="muted">{uploadFile ? (uploadType ? `${uploadType === "datapack" ? "Datapack" : pluginProfile ? "Plugin" : "Mod"} detected` : "Unrecognized file type") : "or click to browse"}</span>
              </div>
            </div>
          )}

          {source !== "upload" && (
            <div className="discover-marketplace-layout">
              <aside className="discover-filter-sidebar" aria-label="Discover filters">
                <div className="discover-filters">
                  <DiscoverFilters
                    filters={filters}
                    metadata={metadata}
                    metadataError={metadataError}
                    worlds={worlds}
                    selectedWorld={selectedWorld}
                    updateFilters={updateFilters}
                    setSelectedWorld={setSelectedWorld}
                    pluginProfile={pluginProfile}
                  />
                </div>
              </aside>

              <div className="discover-results-pane">
                <div className="discover-sticky-bar">
                  <div className="discover-search-row">
                    <form className="discover-search-bar" onSubmit={(event) => { event.preventDefault(); search(true, query.trim()); }}>
                      <Search size={18} className="discover-search-icon" />
                      <Input placeholder={`Search ${sourceLabel[source]} ${filters.content === "modpack" ? "modpacks" : filters.content === "datapack" ? "datapacks" : pluginProfile ? "plugins" : "mods"} for ${server.minecraftVersion}`} value={query} disabled={busy} onChange={(event) => setQuery(event.target.value)} />
                    </form>
                    <button
                      type="button"
                      className={`discover-mobile-filter-toggle ${mobileFilterCount > 0 ? "has-active" : ""}`}
                      onClick={() => setMobileFiltersOpen((v) => !v)}
                      aria-expanded={mobileFiltersOpen}
                      aria-controls="discover-mobile-filters"
                    >
                      <SlidersHorizontal size={16} />
                      Filters
                      {mobileFilterCount > 0 && <span className="filter-menu-badge">{mobileFilterCount}</span>}
                    </button>
                    <div className="discover-view-toggle" role="group" aria-label="Results view">
                      <button type="button" className={`discover-view-btn ${viewMode === "list" ? "active" : ""}`} aria-label="List view" aria-pressed={viewMode === "list"} onClick={() => setViewMode("list")}><List size={16} /></button>
                      <button type="button" className={`discover-view-btn ${viewMode === "grid" ? "active" : ""}`} aria-label="Grid view" aria-pressed={viewMode === "grid"} onClick={() => setViewMode("grid")}><LayoutGrid size={16} /></button>
                    </div>
                  </div>

                  {/* Mobile filter panel — collapsible, stays visible while sticky */}
                  <div id="discover-mobile-filters" className={`discover-mobile-filters ${mobileFiltersOpen ? "open" : ""}`}>
                    <DiscoverFilters
                      filters={filters}
                      metadata={metadata}
                      metadataError={metadataError}
                      worlds={worlds}
                      selectedWorld={selectedWorld}
                      updateFilters={updateFilters}
                      setSelectedWorld={setSelectedWorld}
                      pluginProfile={pluginProfile}
                    />
                  </div>
                </div>

                <div className={`discover-results ${viewMode === "list" ? "list-view" : "grid-view"}`} ref={resultListRef}>
                {searching && (
                  <div className="discover-skeletons">
                    {[0, 1, 2, 3, 4, 5].map((i) => (
                      <div key={i} className="discover-card discover-card-skeleton" aria-hidden="true">
                        <span className="skeleton skeleton-mod-icon" />
                        <div className="discover-card-body">
                          <span className="skeleton skeleton-line wide" />
                          <span className="skeleton skeleton-line medium" />
                          <span className="skeleton skeleton-line short" />
                        </div>
                        <span className="skeleton skeleton-button" />
                      </div>
                    ))}
                  </div>
                )}
                {!searching && visibleResults.map((result) => {
                  const resultId = searchResultKey(result);
                  const projectId = searchResultProjectId(result);
                  const icon = result.icon_url || result.logo?.thumbnailUrl;
                  const title = result.title ?? result.name ?? "Untitled project";
                  const canOpen = Boolean(projectId);
                  const installedStatus = projectId ? installedProjectStatus.get(projectId) : undefined;
                  const isInstalled = Boolean(installedStatus);
                  const canInstall = Boolean(projectId);
                  const cats = (result.categories ?? []).filter((c) => !["fabric", "forge", "neoforge", "quilt", "datapack", "modpack"].includes(c)).slice(0, 3);
                  const cardBusyKey = `card-install:${projectId}`;
                  const isCardBusy = busyId === cardBusyKey;
                  return (
                    <div
                      key={resultId}
                      className={`discover-card ${viewMode === "list" ? "list-view" : "grid-view"} ${canOpen ? "clickable" : ""}`}
                      role={canOpen ? "link" : undefined}
                      tabIndex={canOpen ? 0 : undefined}
                      onClick={canOpen ? () => window.open(modrinthUrl(result), "_blank", "noopener,noreferrer") : undefined}
                      onKeyDown={canOpen ? (event) => {
                        if (event.key === "Enter" || event.key === " ") {
                          event.preventDefault();
                          window.open(modrinthUrl(result), "_blank", "noopener,noreferrer");
                        }
                      } : undefined}
                    >
                      <div className="discover-card-icon">
                        <ModIcon url={icon} size={viewMode === "list" ? 40 : 56} />
                      </div>
                      <div className="discover-card-body">
                        <div className="discover-card-head">
                          <strong>{title}</strong>
                        </div>
                        {result.author && <span className="discover-card-author">by {result.author}</span>}
                        {(result.description || result.summary) && <p className="discover-card-desc">{result.description ?? result.summary}</p>}
                        <div className="discover-card-footer">
                          <div className="discover-card-stats">
                            <span><Download size={12} />{compactNumber(result.downloads ?? result.downloadCount)}</span>
                            {result.follows !== undefined && <span><Users size={12} />{compactNumber(result.follows)}</span>}
                          </div>
                          {cats.length > 0 && (
                            <div className="discover-card-tags">
                              {cats.map((cat) => <span key={cat} className="discover-card-tag">{cat}</span>)}
                            </div>
                          )}
                        </div>
                      </div>
                      {canInstall && (
                        isInstalled ? (
                          <span className="discover-card-install installed" title={`${title} is already installed`} onClick={(event) => event.stopPropagation()}>
                            <CircleCheck size={14} />Installed
                          </span>
                        ) : (
                          <Button
                            variant="primary"
                            className="discover-card-install"
                            disabled={busy}
                            onClick={(event) => { event.stopPropagation(); installFromCard(result); }}
                            onKeyDown={(event) => event.stopPropagation()}
                            title={filters.content === "datapack" && !selectedWorld ? "Select a target world first" : filters.content === "modpack" ? `Download ${title} modpack` : `Install ${title}`}
                          >
                            <Download size={14} />{isCardBusy ? "..." : "Install"}
                          </Button>
                        )
                      )}
                    </div>
                  );
                })}
                {loadingMore && <div className="mods-empty mini"><strong>Loading more...</strong></div>}
                {!searching && !loadingMore && hasMoreResults && visibleResults.length > 0 && (
                  <Button className="load-more-results" type="button" onClick={loadMoreResults}>Load more</Button>
                )}
                {!searching && !loadingMore && query.trim() && visibleResults.length === 0 && results.length === 0 && (
                  <div className="mods-empty">
                    <Search size={36} />
                    <h3>No results</h3>
                    <p>Try a different search term or adjust your filters.</p>
                  </div>
                )}
                {!searching && !loadingMore && visibleResults.length === 0 && results.length > 0 && (
                  <div className="mods-empty">
                    <Search size={36} />
                    <h3>All results filtered out</h3>
                    <p>No results match the selected side filter. Try changing the Side filter.</p>
                  </div>
                )}
                </div>
              </div>
            </div>
          )}
        </div>
      )}
    </section>
  );
}
