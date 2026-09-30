"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { copyTextToClipboard } from "../../lib/clipboard";
import { formatBytes } from "../../lib/utils";
import { useUpdateInstaller } from "../../components/update-progress";
import {
  checkForUpdates,
  clearUpdateSafety,
  fetchDaemonLogs,
  fetchDaemonLogsFull,
  fetchJavaRuntimes,
  fetchTypeVersions,
  fetchUpdateSafety,
  installJavaRuntime,
  uninstallJavaRuntime,
} from "../../lib/runtime-client";
import type { JavaRuntimeInfo, ServerType, UpdateCheckResult, UpdateSafetyInfo } from "../../lib/types";

type Notify = (message: string) => void;

const catalogTypes: ServerType[] = ["vanilla", "paper", "purpur", "folia", "fabric", "forge", "neoforge"];

export function useJavaRuntimes(enabled: boolean, onMessage: Notify) {
  const [runtimes, setRuntimes] = useState<JavaRuntimeInfo[]>([]);
  const [loaded, setLoaded] = useState(false);
  /** Major version being changed, or -1 while refreshing. */
  const [busy, setBusy] = useState<number | null>(null);

  useEffect(() => {
    if (!enabled) return;
    let alive = true;
    fetchJavaRuntimes()
      .then((data) => {
        if (!alive) return;
        setRuntimes(data.runtimes ?? []);
        setLoaded(true);
      })
      .catch((error) => {
        if (alive) onMessage(error instanceof Error ? error.message : "Java runtime check failed");
      });
    return () => {
      alive = false;
    };
  }, [enabled, onMessage]);

  const run = useCallback(
    async (marker: number, action: () => Promise<{ runtimes?: JavaRuntimeInfo[] }>, success: string, failure: string) => {
      setBusy(marker);
      try {
        const data = await action();
        setRuntimes(data.runtimes ?? []);
        onMessage(success);
      } catch (error) {
        onMessage(error instanceof Error ? error.message : failure);
      } finally {
        setBusy(null);
      }
    },
    [onMessage],
  );

  return {
    runtimes,
    loaded,
    busy,
    install: (major: number) => (busy === null ? run(major, () => installJavaRuntime(major), `Java ${major} installed`, "Java install failed") : undefined),
    uninstall: (major: number) => (busy === null ? run(major, () => uninstallJavaRuntime(major), `Java ${major} uninstalled`, "Java uninstall failed") : undefined),
    refresh: () => (busy === null ? run(-1, () => fetchJavaRuntimes(), "Java runtimes refreshed", "Java refresh failed") : undefined),
  };
}

export function useTypeVersionCounts(enabled: boolean) {
  const [counts, setCounts] = useState<Record<string, number>>({});
  const [experimental, setExperimental] = useState<Record<string, number>>({});
  const [busy, setBusy] = useState(false);
  const cancelled = useRef(false);

  const load = useCallback(async () => {
    const nextCounts: Record<string, number> = {};
    const nextExperimental: Record<string, number> = {};
    await Promise.all(
      catalogTypes.map(async (type) => {
        try {
          const data = await fetchTypeVersions(type);
          nextCounts[type] = data.versions?.length ?? 0;
          nextExperimental[type] = data.experimentalVersions?.length ?? 0;
        } catch {
          nextCounts[type] = 0;
          nextExperimental[type] = 0;
        }
      }),
    );
    if (cancelled.current) return;
    setCounts(nextCounts);
    setExperimental(nextExperimental);
  }, []);

  useEffect(() => {
    if (!enabled) return;
    cancelled.current = false;
    void load();
    return () => {
      cancelled.current = true;
    };
  }, [enabled, load]);

  const refresh = useCallback(async () => {
    setBusy(true);
    try {
      await load();
    } finally {
      setBusy(false);
    }
  }, [load]);

  return { counts, experimental, busy, refresh };
}

export type LogMode = "live" | "full";

export function useDaemonLogs(active: boolean, onMessage: Notify) {
  const [lines, setLines] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [mode, setMode] = useState<LogMode>("live");
  const loadedModeRef = useRef<LogMode | null>(null);

  useEffect(() => {
    if (!active || loadedModeRef.current === mode) return;
    let alive = true;
    const fetcher = mode === "full" ? fetchDaemonLogsFull : fetchDaemonLogs;
    setLoaded(false);
    fetcher()
      .then((data) => {
        if (!alive) return;
        setLines(data.logs ?? []);
        setLoaded(true);
        loadedModeRef.current = mode;
      })
      .catch((error) => {
        if (alive) onMessage(error instanceof Error ? error.message : "Failed to load daemon logs");
      });
    return () => {
      alive = false;
    };
  }, [active, mode, onMessage]);

  const refresh = useCallback(async () => {
    setBusy(true);
    try {
      const fetcher = mode === "full" ? fetchDaemonLogsFull : fetchDaemonLogs;
      const data = await fetcher();
      setLines(data.logs ?? []);
      setLoaded(true);
      loadedModeRef.current = mode;
      onMessage("Logs refreshed");
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Failed to load daemon logs");
    } finally {
      setBusy(false);
    }
  }, [mode, onMessage]);

  const copy = useCallback(async () => {
    try {
      await copyTextToClipboard(lines.join("\n"));
      onMessage("Logs copied");
    } catch {
      onMessage("Copy failed");
    }
  }, [lines, onMessage]);

  return { lines, busy, loaded, mode, setMode, refresh, copy };
}

export function useUpdates(initial: UpdateCheckResult | null | undefined, onMessage: Notify) {
  const [check, setCheck] = useState<UpdateCheckResult | null>(initial ?? null);
  const [checking, setChecking] = useState(false);
  const installer = useUpdateInstaller(onMessage);
  const [safety, setSafety] = useState<UpdateSafetyInfo | null>(null);
  const [clearing, setClearing] = useState(false);
  const { refreshRunningServers } = installer;

  useEffect(() => {
    void refreshRunningServers();
  }, [refreshRunningServers]);

  useEffect(() => {
    let alive = true;
    fetchUpdateSafety().then((info) => { if (alive) setSafety(info); }).catch(() => undefined);
    return () => { alive = false; };
  }, []);

  const checkNow = useCallback(async () => {
    setChecking(true);
    try {
      const result = await checkForUpdates(true);
      setCheck(result);
      if (result.error) onMessage(`Update check failed: ${result.error}`);
      else if (result.updateAvailable) onMessage(`Update available: v${result.latestVersion}`);
      else onMessage("Cliff is up to date");
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Update check failed");
    } finally {
      setChecking(false);
    }
  }, [onMessage]);

  const clearSafety = useCallback(async () => {
    setClearing(true);
    try {
      const result = await clearUpdateSafety();
      setSafety(result.safety);
      onMessage(`Deleted the update safety copies and freed ${formatBytes(result.freedBytes)}`);
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Could not delete the safety copies");
      fetchUpdateSafety().then(setSafety).catch(() => undefined);
    } finally {
      setClearing(false);
    }
  }, [onMessage]);

  return {
    check: check ?? initial ?? null,
    checking,
    checkNow,
    installing: installer.installing,
    progress: installer.progress,
    installError: installer.error,
    failedStage: installer.failedStage,
    install: installer.install,
    runningServers: installer.runningServers,
    safety,
    clearing,
    clearSafety,
  };
}
