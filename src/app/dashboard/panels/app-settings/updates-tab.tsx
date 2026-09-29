"use client";

import { ExternalLink, RefreshCw } from "lucide-react";
import { formatBytes } from "../../lib/utils";
import type { UpdateCheckResult } from "../../lib/types";
import { Banner, Button, Card, KeyValueList, StatusDot, type KeyValueItem } from "../../components/ui";

export function UpdatesTab({
  check,
  checking,
  installing,
  onCheck,
  onInstall,
}: {
  check: UpdateCheckResult | null;
  checking: boolean;
  installing: boolean;
  onCheck: () => void;
  onInstall: () => void;
}) {
  const checkButton = (
    <Button iconLeft={<RefreshCw size={14} />} disabled={checking} onClick={onCheck}>
      {checking ? "Checking..." : "Check for updates"}
    </Button>
  );

  if (!check) {
    return (
      <Card title="Cliff updates" description="Click Check for updates to see if a new version is available." actions={checkButton}>
        <p className="muted">No update check has run in this session.</p>
      </Card>
    );
  }

  if (check.error) {
    return (
      <Card title="Cliff updates" actions={checkButton}>
        <Banner variant="warning">Update check failed: {check.error}</Banner>
      </Card>
    );
  }

  const items: KeyValueItem[] = [
    { key: "current", label: "Current version", value: `v${check.currentVersion}`, mono: true },
    { key: "latest", label: "Latest version", value: `v${check.latestVersion}`, mono: true },
  ];
  if (check.builtAt) {
    items.push({
      key: "released",
      label: "Released",
      value: new Date(check.builtAt).toLocaleDateString("en-US", { year: "numeric", month: "short", day: "numeric" }),
    });
  }
  if (check.archiveSize) items.push({ key: "size", label: "Download size", value: formatBytes(check.archiveSize) });

  return (
    <Card title="Cliff updates" actions={checkButton}>
      {check.updateAvailable ? (
        <Banner
          variant="info"
          title={`Version ${check.latestVersion} is available`}
          action={
            <Button variant="primary" disabled={installing} loading={installing} loadingText="Updating..." onClick={onInstall}>
              Install update
            </Button>
          }
        >
          Running servers are stopped gracefully, and Cliff restarts automatically after the update.
        </Banner>
      ) : (
        <p className="update-status">
          <StatusDot tone="running" /> Cliff is up to date.
        </p>
      )}
      <KeyValueList items={items} />
      {check.releaseUrl && (
        <p className="update-release-link">
          <a href={check.releaseUrl} target="_blank" rel="noopener noreferrer">
            View release notes on GitHub <ExternalLink size={12} aria-hidden="true" />
          </a>
        </p>
      )}
    </Card>
  );
}
