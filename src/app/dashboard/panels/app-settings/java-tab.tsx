"use client";

import { RefreshCw } from "lucide-react";
import type { ConfirmRequest, JavaRuntimeInfo } from "../../lib/types";
import { Button, Card, Pill, SettingRow, SkeletonRows } from "../../components/ui";

export function JavaTab({
  runtimes,
  loaded,
  busy,
  onInstall,
  onUninstall,
  onRefresh,
  onConfirm,
}: {
  runtimes: JavaRuntimeInfo[];
  loaded: boolean;
  busy: number | null;
  onInstall: (major: number) => void;
  onUninstall: (major: number) => Promise<void> | void;
  onRefresh: () => void;
  onConfirm: (request: ConfirmRequest) => void;
}) {
  return (
    <Card
      title="Java runtimes"
      description="Cliff installs the Java versions your servers need. Required runtimes cannot be removed while servers depend on them."
      actions={
        <Button size="sm" iconLeft={<RefreshCw size={14} />} disabled={busy !== null} onClick={onRefresh}>
          {busy === -1 ? "Refreshing..." : "Refresh"}
        </Button>
      }
    >
      {!loaded && runtimes.length === 0 ? (
        <SkeletonRows rows={3} label="Loading Java runtimes" />
      ) : (
        <div>
          {runtimes.map((runtime) => (
            <SettingRow
              key={runtime.major}
              label={runtime.label}
              description={
                <>
                  {runtime.required ? "Required by one or more server profiles" : "Available for manual selection"}
                  {runtime.usedBy.length > 0 && <> · Used by {runtime.usedBy.join(", ")}</>}
                </>
              }
            >
              <Pill variant={runtime.installed ? "success" : "default"}>{runtime.installed ? "Installed" : "Not installed"}</Pill>
              {runtime.installed ? (
                <Button
                  size="sm"
                  variant="danger"
                  disabled={busy !== null || runtime.required}
                  onClick={() =>
                    onConfirm({
                      title: `Uninstall ${runtime.label}`,
                      message:
                        runtime.usedBy.length > 0
                          ? `This Java version is used by: ${runtime.usedBy.join(", ")}. Uninstalling will break those servers until they are reconfigured.`
                          : `This will remove the ${runtime.label} runtime from disk. Servers using it will need to be reconfigured.`,
                      confirmLabel: "Uninstall",
                      dangerous: true,
                      onConfirm: async () => {
                        await onUninstall(runtime.major);
                      },
                    })
                  }
                >
                  {busy === runtime.major ? "Uninstalling..." : "Uninstall"}
                </Button>
              ) : (
                <Button size="sm" disabled={busy !== null} onClick={() => onInstall(runtime.major)}>
                  {busy === runtime.major ? "Installing..." : "Install"}
                </Button>
              )}
            </SettingRow>
          ))}
        </div>
      )}
    </Card>
  );
}
