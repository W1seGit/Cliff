"use client";

import { Copy, Download, RefreshCw } from "lucide-react";
import { daemonLogsUrl } from "../../lib/runtime-client";
import type { Settings } from "../../lib/types";
import { Button, Card, ConsoleView, Select } from "../../components/ui";
import type { LogMode } from "./hooks";

export function LogsTab({
  settings,
  lines,
  loaded,
  busy,
  mode,
  onModeChange,
  onRefresh,
  onCopy,
}: {
  settings: Settings;
  lines: string[];
  loaded: boolean;
  busy: boolean;
  mode: LogMode;
  onModeChange: (mode: LogMode) => void;
  onRefresh: () => void;
  onCopy: () => void;
}) {
  return (
    <Card
      title="Daemon logs"
      description={mode === "live" ? "Recent in-memory buffer" : "Complete log file from disk"}
      actions={
        <>
          <Select value={mode} onChange={(event) => onModeChange(event.target.value as LogMode)} aria-label="Log mode">
            <option value="live">Live buffer</option>
            <option value="full">Full log file</option>
          </Select>
          <Button iconLeft={<Copy size={14} />} disabled={busy || lines.length === 0} onClick={onCopy}>
            Copy
          </Button>
          <Button iconLeft={<RefreshCw size={14} />} disabled={busy} onClick={onRefresh}>
            {busy ? "Loading..." : "Refresh"}
          </Button>
          <Button
            iconLeft={<Download size={14} />}
            disabled={lines.length === 0}
            onClick={() => window.open(daemonLogsUrl(mode === "full" ? "?full=1&download=1" : "?download=1"), "_blank")}
          >
            Download
          </Button>
        </>
      }
    >
      {settings.logFile && (
        <p className="log-file-path">
          Log file: <code>{settings.logFile}</code>
        </p>
      )}
      <ConsoleView lines={lines} emptyMessage={!loaded ? "Loading logs..." : "No daemon logs recorded yet."} />
    </Card>
  );
}
