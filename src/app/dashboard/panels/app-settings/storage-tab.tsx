"use client";

import { formatBytes } from "../../lib/utils";
import type { Settings } from "../../lib/types";
import { Banner, Card, CopyButton, KeyValueList, type KeyValueItem } from "../../components/ui";

function pathItem(key: string, label: string, value: string | undefined, onMessage: (message: string) => void): KeyValueItem {
  if (!value) return { key, label, value: "not set" };
  return {
    key,
    label,
    mono: true,
    value: (
      <>
        <span className="kv-path">{value}</span>
        <CopyButton text={value} label={`Copy ${label.toLowerCase()}`} onError={() => onMessage("Copy failed")} />
      </>
    ),
  };
}

export function StorageTab({
  settings,
  lanAddresses,
  dashboardPort,
  backendUrl,
  onMessage,
}: {
  settings: Settings;
  lanAddresses: string[];
  dashboardPort: string;
  backendUrl: string;
  onMessage: (message: string) => void;
}) {
  const storage = settings.storage;
  const total = storage?.totalBytes ?? null;
  const free = storage?.freeBytes ?? null;
  const usedPercent = total && free != null ? Math.min(100, Math.max(0, Math.round(((total - free) / total) * 100))) : null;

  const usageItems: KeyValueItem[] = [
    { key: "root", label: "Server storage", value: storage?.rootExists ? formatBytes(storage.serverRootSizeBytes) : "missing" },
    { key: "registered", label: "Registered servers", value: formatBytes(storage?.registeredServerSizeBytes ?? 0) },
    { key: "snapshots", label: "Snapshots", value: formatBytes(storage?.snapshotsSizeBytes ?? 0) },
    { key: "backups", label: "Backups", value: storage?.backupCount ?? 0 },
    { key: "free", label: "Disk free", value: free == null ? "unknown" : formatBytes(free) },
    { key: "total", label: "Disk total", value: total == null ? "unknown" : formatBytes(total) },
  ];

  const networkItems: KeyValueItem[] = [
    ...lanAddresses.map((ip) => {
      const address = `${ip}:${dashboardPort}`;
      return pathItem(`lan-${ip}`, `Dashboard (${ip})`, address, onMessage);
    }),
    pathItem("backend", "Backend", backendUrl, onMessage),
  ];

  return (
    <>
      <Card title="Locations" description="Where Cliff keeps your servers and its own data.">
        <KeyValueList
          items={[
            pathItem("serverRoot", "Server storage root", settings.serverRoot, onMessage),
            pathItem("dataDir", "Panel data directory", settings.dataDir, onMessage),
          ]}
        />
      </Card>
      <Card title="Disk usage">
        {usedPercent != null && (
          <div className="meter" role="img" aria-label={`Disk ${usedPercent}% used`}>
            <div className="meter-fill" style={{ width: `${usedPercent}%` }} data-high={usedPercent >= 90 ? "true" : undefined} />
          </div>
        )}
        <KeyValueList items={usageItems} />
      </Card>
      <Card title="Network access" description="Addresses other devices on your network can use to open this dashboard.">
        {lanAddresses.length === 0 && <Banner variant="warning">No LAN IPv4 address detected.</Banner>}
        <KeyValueList items={networkItems} />
      </Card>
    </>
  );
}
