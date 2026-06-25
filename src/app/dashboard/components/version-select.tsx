"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { fetchTypeVersions } from "../lib/runtime-client";
import type { MinecraftMetadata, ServerType } from "../lib/types";

// Compare Minecraft version strings like "1.21.4", "26.2", "1.7.10".
// Splits on "." and "-" and compares numeric parts, with non-numeric suffixes
// (e.g., "rc1", "pre5") sorted after numeric versions.
function compareMinecraftVersions(a: string, b: string): number {
  const partsA = a.split(/[.\-]/);
  const partsB = b.split(/[.\-]/);
  const maxLen = Math.max(partsA.length, partsB.length);
  for (let i = 0; i < maxLen; i++) {
    const pa = partsA[i] ?? "";
    const pb = partsB[i] ?? "";
    const na = Number(pa);
    const nb = Number(pb);
    const aIsNum = !isNaN(na) && pa !== "";
    const bIsNum = !isNaN(nb) && pb !== "";
    if (aIsNum && bIsNum) {
      if (na !== nb) return nb - na; // descending: newer first
    } else if (aIsNum && !bIsNum) {
      return -1; // numeric before non-numeric
    } else if (!aIsNum && bIsNum) {
      return 1;
    } else {
      // Both non-numeric (e.g., "rc1" vs "pre5"), compare as strings descending
      if (pa !== pb) return pa > pb ? -1 : 1;
    }
  }
  return 0;
}

export function VersionSelect({
  value,
  serverType,
  metadata,
  metadataError,
  onChange,
}: {
  value: string;
  serverType?: ServerType;
  metadata: MinecraftMetadata | null;
  metadataError: string;
  onChange: (value: string) => void;
}) {
  const [typeVersions, setTypeVersions] = useState<string[] | null>(null);
  const [typeExpVersions, setTypeExpVersions] = useState<string[]>([]);
  const [typeVersionsError, setTypeVersionsError] = useState("");
  const [typeVersionsLoading, setTypeVersionsLoading] = useState(false);
  const fetchedTypeRef = useRef("");
  const onChangeRef = useRef(onChange);
  const valueRef = useRef(value);

  useEffect(() => {
    onChangeRef.current = onChange;
    valueRef.current = value;
  }, [onChange, value]);

  const usingTypeVersions = Boolean(serverType && serverType !== "vanilla");

  const loadTypeVersions = useCallback((type: ServerType, refresh: boolean) => {
    setTypeVersionsLoading(true);
    setTypeVersionsError("");
    let cancelled = false;
    fetchTypeVersions(type, refresh)
      .then((data) => {
        if (cancelled) return;
        const versions = data.versions ?? [];
        setTypeVersions(versions);
        setTypeExpVersions(data.experimentalVersions ?? []);
        setTypeVersionsLoading(false);
        if (versions.length > 0 && !versions.includes(valueRef.current)) {
          // Pick the newest stable version as default
          const sorted = [...versions].sort(compareMinecraftVersions);
          onChangeRef.current(sorted[0]);
        }
      })
      .catch((error) => {
        if (cancelled) return;
        setTypeVersionsError(error instanceof Error ? error.message : "Failed to load versions");
        setTypeVersionsLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Fetch type-specific versions when serverType changes
  useEffect(() => {
    if (!serverType || serverType === "vanilla") {
      // Reset the fetch tracker so switching back to this type re-fetches.
      // Stale typeVersions/typeVersionsError are harmless here because
      // usingTypeVersions is false for vanilla, so the render path ignores them.
      fetchedTypeRef.current = "";
      return;
    }
    if (fetchedTypeRef.current === serverType) return;
    fetchedTypeRef.current = serverType;
    const cancel = loadTypeVersions(serverType, false);
    return () => {
      cancel();
      if (fetchedTypeRef.current === serverType) fetchedTypeRef.current = "";
    };
  }, [serverType, loadTypeVersions]);

  function refreshTypeVersions() {
    if (!serverType || serverType === "vanilla") return;
    loadTypeVersions(serverType, true);
  }

  // Build version groups from type-specific versions or Mojang metadata
  const groups = useMemo(() => {
    if (usingTypeVersions) {
      if (!typeVersions || typeVersions.length === 0) return null;
      const sorted = [...typeVersions].sort(compareMinecraftVersions);
      const pinnedIds = metadata ? new Set([metadata.latest.release, metadata.latest.snapshot].filter((id) => sorted.includes(id))) : new Set<string>();
      const current = sorted.filter((id) => pinnedIds.has(id));
      const rest = sorted.filter((id) => !pinnedIds.has(id));
      const expSorted = [...typeExpVersions].sort(compareMinecraftVersions);
      return { current, releases: rest, snapshots: [] as string[], experimental: expSorted };
    }
    if (!metadata) return null;
    const pinnedIds = new Set([metadata.latest.release, metadata.latest.snapshot]);
    return {
      current: metadata.minecraftVersions.filter((v) => pinnedIds.has(v.id)).map((v) => v.id),
      releases: metadata.minecraftVersions.filter((v) => v.type === "release" && !pinnedIds.has(v.id)).map((v) => v.id),
      snapshots: metadata.minecraftVersions.filter((v) => v.type === "snapshot" && !pinnedIds.has(v.id)).map((v) => v.id),
      experimental: [] as string[],
    };
  }, [usingTypeVersions, typeVersions, typeExpVersions, metadata]);

  const loading = usingTypeVersions ? typeVersionsLoading : !metadata;
  const error = usingTypeVersions ? typeVersionsError : metadataError;
  const noVersions = usingTypeVersions && !typeVersionsLoading && (typeVersions === null || typeVersions.length === 0);

  return (
    <div className="version-combobox">
      <div className="version-combobox-row">
        <select
          className="native-select"
          value={value}
          disabled={loading || noVersions}
          onChange={(e) => onChange(e.target.value)}
        >
          {(loading || noVersions) && <option value="">{noVersions ? "No versions available" : error || "Loading versions..."}</option>}
          {groups && !loading && (
            <>
              {groups.current.length > 0 && (
                <optgroup label="Current">
                  {groups.current.map((id) => <option key={id} value={id}>{metadata && id === metadata.latest.release ? `${id} / latest release` : id}</option>)}
                </optgroup>
              )}
              {groups.releases.length > 0 && (
                <optgroup label={usingTypeVersions ? "Versions" : "Releases"}>
                  {groups.releases.map((id) => <option key={id} value={id}>{id}</option>)}
                </optgroup>
              )}
              {groups.snapshots.length > 0 && (
                <optgroup label="Snapshots">
                  {groups.snapshots.map((id) => <option key={id} value={id}>{id}</option>)}
                </optgroup>
              )}
              {groups.experimental.length > 0 && (
                <optgroup label="Experimental (alpha builds)">
                  {groups.experimental.map((id) => <option key={id} value={id}>{id} / experimental</option>)}
                </optgroup>
              )}
            </>
          )}
        </select>
        {usingTypeVersions && (
          <button
            type="button"
            className="version-combobox-refresh"
            disabled={typeVersionsLoading}
            onClick={refreshTypeVersions}
            title="Refresh version list"
          >
            {typeVersionsLoading ? "..." : "Refresh"}
          </button>
        )}
      </div>
      {loading && <p className="field-note warn">{error || "Loading version metadata..."}</p>}
      {noVersions && <p className="field-note warn">No supported versions found for {serverType}. Try refreshing.</p>}
      {usingTypeVersions && !loading && !noVersions && typeVersions && typeVersions.length > 0 && (
        <p className="field-note">{typeVersions.length} versions supported by {serverType}</p>
      )}
    </div>
  );
}
