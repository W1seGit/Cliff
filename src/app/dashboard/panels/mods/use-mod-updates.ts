"use client";

import { useCallback, useState } from "react";
import { checkModUpdates, updateServerMods } from "../../lib/runtime-client";
import type { ModUpdateCheck, ModUpdateInfo, ModUpdateResult } from "../../lib/types";

export type ModUpdatesState = {
  /** Latest check result by file name; null until the user checks. */
  byFile: Map<string, ModUpdateInfo> | null;
  counts: ModUpdateCheck["counts"];
  checking: boolean;
  /** File names being updated right now; "*" means all. */
  updating: string[];
  error: string;
  result: ModUpdateResult | null;
  outdated: ModUpdateInfo[];
  check: () => Promise<void>;
  update: (fileNames: string[]) => Promise<void>;
  dismissResult: () => void;
};

/**
 * Update checking for installed mods. Nothing runs until `check` is called,
 * because a check asks Modrinth about every mod.
 */
export function useModUpdates(serverId: string, onRefresh: () => Promise<void> | void): ModUpdatesState {
  const [stored, setStored] = useState<{ serverId: string; check: ModUpdateCheck } | null>(null);
  const data = stored?.serverId === serverId ? stored.check : null;
  const [checking, setChecking] = useState(false);
  const [updating, setUpdating] = useState<string[]>([]);
  const [error, setError] = useState("");
  const [result, setResult] = useState<ModUpdateResult | null>(null);


  const check = useCallback(async () => {
    setChecking(true);
    setError("");
    try {
      setStored({ serverId, check: await checkModUpdates(serverId) });
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Could not check for updates");
    } finally {
      setChecking(false);
    }
  }, [serverId]);

  const update = useCallback(async (fileNames: string[]) => {
    setUpdating(fileNames.length ? fileNames : ["*"]);
    setError("");
    setResult(null);
    try {
      const outcome = await updateServerMods(serverId, fileNames);
      setResult(outcome);
      await onRefresh();
      // File names change after an update, so look again to show fresh badges.
      setStored({ serverId, check: await checkModUpdates(serverId) });
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Could not update mods");
    } finally {
      setUpdating([]);
    }
  }, [serverId, onRefresh]);

  const byFile = data ? new Map(data.mods.map((mod) => [mod.fileName, mod])) : null;
  return {
    byFile,
    counts: data?.counts ?? {},
    checking,
    updating,
    error,
    result,
    outdated: data ? data.mods.filter((mod) => mod.status === "update") : [],
    check,
    update,
    dismissResult: () => setResult(null),
  };
}
