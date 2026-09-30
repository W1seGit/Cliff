"use client";

import { Loader2, Menu as MenuIcon, MoreHorizontal, Play, RefreshCw, RotateCw, Square, Zap } from "lucide-react";
import type { ServerRecord } from "../lib/types";
import { copyTextToClipboard } from "../lib/clipboard";
import { joinAddressFor } from "../lib/utils";
import { ServerAvatar } from "./server-avatar";
import { Menu, MenuItem } from "./ui/menu";

type Lifecycle = "stopped" | "starting" | "running" | "stopping";

const pendingLabels: Record<string, string> = {
  start: "Starting...",
  stop: "Stopping...",
  force: "Stopping...",
  restart: "Restarting...",
};

export function ServerHeader({
  selected,
  isRunning,
  lifecycle,
  anotherServerRunning,
  runningServer,
  busyAction,
  onAction,
  refreshBusy,
  onRefresh,
  onMessage,
  onOpenSidebar,
}: {
  selected: ServerRecord;
  isRunning: boolean;
  lifecycle: Lifecycle;
  anotherServerRunning: boolean;
  runningServer?: ServerRecord;
  busyAction: string;
  onAction: (path: string, body?: Record<string, unknown>, busyLabel?: string) => void;
  refreshBusy: boolean;
  onRefresh: () => void;
  onMessage: (message: string) => void;
  onOpenSidebar: () => void;
}) {
  // What is in flight right now, from either a click or the server's own lifecycle.
  const pending = busyAction || (lifecycle === "starting" ? "start" : lifecycle === "stopping" ? "stop" : "");
  const controlsLocked = Boolean(pending);
  const forceDisabled = !isRunning || controlsLocked;

  const joinAddress = joinAddressFor(selected);
  async function copyAddress() {
    try {
      await copyTextToClipboard(joinAddress);
      onMessage("Join address copied");
    } catch {
      onMessage("Clipboard copy failed");
    }
  }

  const statusLabel = isRunning
    ? lifecycle === "starting"
      ? "Starting"
      : lifecycle === "stopping"
        ? "Stopping"
        : "Running"
    : anotherServerRunning
      ? "Blocked"
      : "Stopped";
  const statusClass = isRunning ? (lifecycle === "running" ? "on" : "busy") : anotherServerRunning ? "busy" : "";

  return (
    <header className={`server-header ${isRunning && lifecycle === "running" ? "is-running" : ""}`.trim()} aria-label="Server context">
      <button className="mobile-sidebar-button" aria-label="Open sidebar" onClick={onOpenSidebar}>
        <MenuIcon size={18} />
      </button>
      <div className="server-header-id">
        <ServerAvatar server={selected} on={isRunning} className="server-header-avatar" />
        <div className="server-header-meta">
          <div className="server-header-title">
            <h1>{selected.name}</h1>
            <span className={`status ${statusClass}`}>{statusLabel}</span>
          </div>
          <div className="server-header-sub">
            <span>{selected.type}</span>
            <span aria-hidden="true">•</span>
            <span>{selected.minecraftVersion}</span>
            <span aria-hidden="true">•</span>
            <button className="server-address-link" onClick={copyAddress} title="Copy join address">
              {joinAddress}
            </button>
          </div>
        </div>
      </div>
      <div className="server-header-actions">
        {controlsLocked ? (
          <button className="icon-button server-header-pending" disabled aria-busy="true" aria-label={pendingLabels[pending] ?? "Working"}>
            <Loader2 size={16} className="spin" />
            <span className="server-header-action-label">{pendingLabels[pending] ?? "Working..."}</span>
          </button>
        ) : isRunning ? (
          <>
            <button className="icon-button" aria-label="Restart" onClick={() => onAction("restart", {}, "restart")}>
              <RotateCw size={16} />
              <span className="server-header-action-label">Restart</span>
            </button>
            <button className="danger-button icon-button" aria-label="Stop" onClick={() => onAction("stop", {}, "stop")}>
              <Square size={15} />
              <span className="server-header-action-label">Stop</span>
            </button>
          </>
        ) : (
          <button className="primary icon-button" disabled={anotherServerRunning} aria-label="Start" onClick={() => onAction("start", {}, "start")}>
            <Play size={16} />
            <span className="server-header-action-label">Start</span>
          </button>
        )}
        <Menu
          trigger={
            <button className="icon-button more-menu-trigger" aria-label="More actions">
              <MoreHorizontal size={16} />
              <span className="server-header-action-label">More</span>
            </button>
          }
        >
          <MenuItem danger icon={<Zap size={15} />} disabled={forceDisabled} onSelect={() => onAction("stop", { force: true }, "force")}>
            Force stop
          </MenuItem>
          <MenuItem icon={<RefreshCw size={15} />} disabled={refreshBusy} onSelect={onRefresh}>
            {refreshBusy ? "Refreshing..." : "Refresh"}
          </MenuItem>
        </Menu>
        {anotherServerRunning && !isRunning && <small className="control-strip-note">Stop {runningServer?.name ?? "running server"} first</small>}
      </div>
    </header>
  );
}
