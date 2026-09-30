"use client";

import { RefreshCw } from "lucide-react";
import { serverTypeNeedsLoader } from "../../lib/utils";
import type { MinecraftMetadata, ServerType } from "../../lib/types";
import { Banner, Button, Card, KeyValueList, type KeyValueItem } from "../../components/ui";

const typeLabels: Record<string, string> = {
  vanilla: "Vanilla",
  paper: "Paper",
  purpur: "Purpur",
  folia: "Folia",
  fabric: "Fabric",
  forge: "Forge",
  neoforge: "NeoForge",
};

const catalogOrder: ServerType[] = ["vanilla", "paper", "purpur", "folia", "fabric", "forge", "neoforge"];

export function GeneralTab({
  metadata,
  metadataError,
  counts,
  experimental,
  refreshing,
  onRefresh,
}: {
  metadata: MinecraftMetadata | null;
  metadataError: string;
  counts: Record<string, number>;
  experimental: Record<string, number>;
  refreshing: boolean;
  onRefresh: () => void;
}) {
  const loaderCatalog = (metadata?.loaderCatalog ?? metadata?.loaders ?? {}) as Partial<Record<ServerType, { version: string }[]>>;

  const minecraftItems: KeyValueItem[] = [
    { key: "release", label: "Latest release", value: metadata?.latest.release ?? "Loading...", mono: true },
    { key: "snapshot", label: "Latest snapshot", value: metadata?.latest.snapshot ?? "Loading...", mono: true },
    { key: "total", label: "Mojang versions", value: metadata?.minecraftVersions.length ?? 0 },
  ];

  const typeItems: KeyValueItem[] = catalogOrder.map((type) => {
    const notes: string[] = ["supported versions"];
    if ((experimental[type] ?? 0) > 0) notes.push(`+${experimental[type]} experimental`);
    const latestLoader = serverTypeNeedsLoader(type) ? loaderCatalog[type]?.[0]?.version : undefined;
    if (latestLoader) notes.push(`latest loader ${latestLoader}`);
    return {
      key: type,
      label: typeLabels[type] ?? type,
      value: counts[type] ?? "...",
      note: notes.join(" · "),
    };
  });

  return (
    <>
      <Banner variant="info">Mod and plugin discovery uses Modrinth. CurseForge support is not available yet.</Banner>
      {metadataError && <Banner variant="warning">{metadataError}</Banner>}
      <Card
        title="Minecraft versions"
        description="Version data Cliff uses when creating and updating servers."
        actions={
          <Button iconLeft={<RefreshCw size={14} />} disabled={refreshing} onClick={onRefresh}>
            {refreshing ? "Refreshing..." : "Refresh"}
          </Button>
        }
      >
        <KeyValueList items={minecraftItems} />
      </Card>
      <Card title="Server types" description="How many Minecraft versions each server type currently supports.">
        <KeyValueList items={typeItems} />
      </Card>
    </>
  );
}
