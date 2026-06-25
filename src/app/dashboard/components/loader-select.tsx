"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { fetchLoaderVersions } from "../lib/runtime-client";
import type { MinecraftMetadata, LoaderOption, ServerType } from "../lib/types";

function loaderLabel(loader: LoaderOption) {
  return `${loader.version}${loader.stable === false ? " (unstable)" : ""}`;
}

export function LoaderSelect({
  type,
  minecraftVersion,
  metadata,
  metadataError,
  value,
  onChange,
}: {
  type: ServerType;
  minecraftVersion: string;
  metadata: MinecraftMetadata | null;
  metadataError: string;
  value: string;
  onChange: (value: string) => void;
}) {
  const [remoteLoaders, setRemoteLoaders] = useState<{ key: string; loaders: LoaderOption[] } | null>(null);
  const [failedKey, setFailedKey] = useState("");
  const [loaderRefreshing, setLoaderRefreshing] = useState(false);
  const [stability, setStability] = useState("all");
  const fetchedKeyRef = useRef("");
  const valueRef = useRef(value);
  const onChangeRef = useRef(onChange);
  const key = `${type}:${minecraftVersion}`;
  const metadataReady = Boolean(metadata);
  const initialLoaders = metadata && minecraftVersion === metadata.latest.release ? metadata.loaders[type] : [];
  const loaders = remoteLoaders?.key === key ? remoteLoaders.loaders : initialLoaders;

  const filteredLoaders = useMemo(() => {
    return loaders.filter((loader) => {
      const stable = loader.stable !== false;
      return stability === "all" || (stability === "stable" && stable) || (stability === "unstable" && !stable);
    });
  }, [loaders, stability]);

  const stableLoaders = useMemo(() => filteredLoaders.filter((l) => l.stable !== false), [filteredLoaders]);
  const unstableLoaders = useMemo(() => filteredLoaders.filter((l) => l.stable === false), [filteredLoaders]);

  const failed = failedKey === key;
  const loading = type !== "vanilla" && Boolean(metadata) && (loaderRefreshing || (remoteLoaders?.key !== key && loaders.length === 0 && !failed));
  const compatibleLoaderCount = remoteLoaders?.key === key ? remoteLoaders.loaders.length : 0;
  const loaderHint = !metadata
    ? metadataError || "Loading loader metadata..."
    : loaderRefreshing
      ? `Refreshing ${type} loaders compatible with Minecraft ${minecraftVersion}...`
      : loading
        ? `Checking ${type} loaders compatible with Minecraft ${minecraftVersion}...`
        : failed
          ? `Could not refresh ${type} loaders for Minecraft ${minecraftVersion}.`
          : compatibleLoaderCount > 0
            ? `${compatibleLoaderCount} ${type} loader versions available for Minecraft ${minecraftVersion}.`
            : loaders.length > 0
              ? `${loaders.length} cached ${type} loader versions available.`
              : `No ${type} loader versions found for Minecraft ${minecraftVersion}.`;

  useEffect(() => {
    valueRef.current = value;
    onChangeRef.current = onChange;
  }, [value, onChange]);

  useEffect(() => {
    if (type === "vanilla" || !minecraftVersion || !metadata) return;
    if (fetchedKeyRef.current === key) return;
    fetchedKeyRef.current = key;
    const resetTimer = window.setTimeout(() => { setStability("all"); }, 0);
    let cancelled = false;
    fetchLoaderVersions(type, minecraftVersion)
      .then((data) => {
        if (cancelled) return;
        setRemoteLoaders({ key, loaders: data.loaders });
        setFailedKey((current) => (current === key ? "" : current));
        if (!data.loaders.some((loader) => loader.version === valueRef.current)) {
          onChangeRef.current(data.loaders[0]?.version ?? "");
        }
      })
      .catch(() => {
        if (!cancelled) { fetchedKeyRef.current = ""; setFailedKey(key); }
      });
    return () => {
      window.clearTimeout(resetTimer);
      cancelled = true;
      if (fetchedKeyRef.current === key) fetchedKeyRef.current = "";
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, minecraftVersion, type, metadataReady]);

  async function refreshCompatibleLoaders() {
    if (!metadata || type === "vanilla" || loaderRefreshing) return;
    setLoaderRefreshing(true);
    try {
      const data = await fetchLoaderVersions(type, minecraftVersion, true);
      setRemoteLoaders({ key, loaders: data.loaders });
      setFailedKey((current) => (current === key ? "" : current));
      if (!data.loaders.some((loader) => loader.version === value)) {
        onChange(data.loaders[0]?.version ?? "");
      }
    } catch {
      setFailedKey(key);
    } finally {
      setLoaderRefreshing(false);
    }
  }

  if (type === "vanilla") return <input value="No loader required" disabled />;

  const disabled = !metadata || loading || loaders.length === 0;

  return (
    <div className="version-combobox">
      <div className="version-combobox-row">
        <select
          className="native-select"
          value={value}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value)}
        >
          {loaders.length === 0 && <option value="">{loading ? "Loading..." : "No loaders found"}</option>}
          {stableLoaders.length > 0 && (
            <optgroup label="Stable">
              {stableLoaders.map((l) => <option key={l.version} value={l.version}>{loaderLabel(l)}</option>)}
            </optgroup>
          )}
          {unstableLoaders.length > 0 && (
            <optgroup label="Unstable">
              {unstableLoaders.map((l) => <option key={l.version} value={l.version}>{loaderLabel(l)}</option>)}
            </optgroup>
          )}
        </select>
        <select
          className="version-combobox-stability"
          value={stability}
          onChange={(e) => setStability(e.target.value)}
          disabled={disabled}
        >
          <option value="all">All</option>
          <option value="stable">Stable</option>
          <option value="unstable">Unstable</option>
        </select>
      </div>
      <div className="version-combobox-footer">
        <p className={`field-note ${failed || loaders.length === 0 ? "warn" : ""}`}>
          {loaderHint}
        </p>
        <button type="button" disabled={!metadata || loaderRefreshing} onClick={refreshCompatibleLoaders}>{loaderRefreshing ? "Refreshing..." : "Refresh"}</button>
      </div>
    </div>
  );
}
