"use client";

import { RefreshCw, ArrowUpCircle } from "lucide-react";
import type { ModUpdateInfo } from "../../lib/types";
import { Banner, Button, Pill } from "../../components/ui";
import type { ModUpdatesState } from "./use-mod-updates";

const runningReason = "Stop the server first. Mods cannot be changed while it is running.";

function plural(count: number, one: string, many: string) {
  return `${count} ${count === 1 ? one : many}`;
}

/** The "Check for updates" button and the summary under it. */
export function ModUpdatesBar({ updates, isRunning, busy }: { updates: ModUpdatesState; isRunning: boolean; busy: boolean }) {
  const { byFile, counts, checking, updating, error, result, outdated } = updates;
  const updatingAll = updating.length > 0;
  const unknown = counts.unknown ?? 0;
  const checked = byFile !== null;

  return (
    <div className="mod-updates">
      <div className="mod-updates-actions">
        <Button
          iconLeft={<RefreshCw size={14} />}
          loading={checking}
          loadingText="Checking..."
          disabled={busy || updatingAll}
          onClick={() => void updates.check()}
        >
          Check for updates
        </Button>
        {outdated.length > 0 && (
          <Button
            variant="primary"
            iconLeft={<ArrowUpCircle size={14} />}
            loading={updatingAll && updating[0] === "*"}
            loadingText="Updating..."
            disabled={busy || checking || updatingAll || isRunning}
            title={isRunning ? runningReason : "Updates every mod listed as outdated. A snapshot is taken first."}
            onClick={() => void updates.update([])}
          >
            Update all ({outdated.length})
          </Button>
        )}
      </div>
      <p className="muted mod-updates-hint">
        {isRunning
          ? runningReason
          : "Checking asks Modrinth about each mod. Updating takes a safety snapshot first."}
      </p>

      {error && <Banner variant="danger" title="Update problem">{error}</Banner>}

      {checked && !error && !checking && (
        outdated.length > 0 ? (
          <Banner variant="info" title={`${plural(outdated.length, "update", "updates")} available`}>
            {unknown > 0 ? `${plural(unknown, "mod", "mods")} could not be checked because they are not on Modrinth.` : "Newer files exist for this server's Minecraft version."}
          </Banner>
        ) : unknown > 0 ? (
          <Banner variant="info" title="Everything that can be checked is up to date">
            {plural(unknown, "mod", "mods")} could not be checked because they are not on Modrinth.
          </Banner>
        ) : (
          <Banner variant="info" title="Everything is up to date" />
        )
      )}

      {result && (
        <div role="status">
          <Banner
            variant={result.failed.length > 0 ? "warning" : "info"}
            title={result.updated.length > 0 ? `Updated ${plural(result.updated.length, "mod", "mods")}` : "Nothing was updated"}
            action={<Button size="sm" onClick={updates.dismissResult}>Dismiss</Button>}
          >
            {result.failed.length > 0 ? `${result.failed.length} could not be updated.` : "A snapshot was taken before the change."}
          </Banner>
          {(result.updated.length > 0 || result.failed.length > 0) && (
            <ul className="mod-updates-result">
              {result.updated.map((name) => <li key={`u:${name}`}><Pill variant="success">Updated</Pill> <span>{name}</span></li>)}
              {result.failed.map((name) => <li key={`f:${name}`}><Pill variant="danger">Failed</Pill> <span>{name}</span></li>)}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}

/** The status badge shown under a mod's name once updates have been checked. */
export function ModUpdateBadge({ info }: { info: ModUpdateInfo | undefined }) {
  if (!info) return null;
  if (info.status === "update") {
    return <Pill variant="accent">{info.latestVersionNumber ? `Update to ${info.latestVersionNumber}` : "Update available"}</Pill>;
  }
  if (info.status === "current") return <Pill variant="success">Up to date</Pill>;
  if (info.status === "unknown") return <Pill title="Not found on Modrinth, so it cannot be checked.">Not checked</Pill>;
  return null;
}

/** The per-row "Update" action. Renders nothing unless an update exists. */
export function ModUpdateAction({
  info, updates, isRunning, busy,
}: { info: ModUpdateInfo | undefined; updates: ModUpdatesState; isRunning: boolean; busy: boolean }) {
  if (info?.status !== "update") return null;
  const working = updates.updating.includes(info.fileName) || updates.updating[0] === "*";
  return (
    <Button
      size="sm"
      loading={working}
      loadingText="Updating..."
      disabled={busy || updates.checking || updates.updating.length > 0 || isRunning}
      title={isRunning ? runningReason : `Update ${info.title}`}
      aria-label={`Update ${info.title}${info.latestVersionNumber ? ` to ${info.latestVersionNumber}` : ""}`}
      onClick={() => void updates.update([info.fileName])}
    >
      Update
    </Button>
  );
}
