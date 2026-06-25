"use client";

import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { Menu, MoreHorizontal, Play, RefreshCw, RotateCw, Square, Zap } from "lucide-react";
import type { ServerRecord } from "../lib/types";
import { copyTextToClipboard } from "../lib/clipboard";
import { joinAddressFor } from "../lib/utils";
import { ServerAvatar } from "./server-avatar";

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
  lifecycle: "stopped" | "starting" | "running" | "stopping";
  anotherServerRunning: boolean;
  runningServer?: ServerRecord;
  busyAction: string;
  onAction: (path: string, body?: Record<string, unknown>, busyLabel?: string) => void;
  refreshBusy: boolean;
  onRefresh: () => void;
  onMessage: (message: string) => void;
  onOpenSidebar: () => void;
}) {
  const [moreOpen, setMoreOpen] = useState(false);
  const [menuPosition, setMenuPosition] = useState({ top: 0, left: 0 });
  const transitioning = lifecycle === "starting" || lifecycle === "stopping";
  const startDisabled = !selected || isRunning || anotherServerRunning || Boolean(busyAction) || transitioning;
  const runningActionDisabled = !selected || !isRunning || Boolean(busyAction) || transitioning;

  const joinAddress = joinAddressFor(selected);
  async function copyAddress() {
    try {
      await copyTextToClipboard(joinAddress);
      onMessage("Join address copied");
    } catch {
      onMessage("Clipboard copy failed");
    }
  }

  function toggleMore(button: HTMLButtonElement) {
    const rect = button.getBoundingClientRect();
    const width = 200;
    const height = 110;
    setMenuPosition({
      top: Math.max(8, Math.min(rect.bottom + 8, window.innerHeight - height - 8)),
      left: Math.max(8, Math.min(rect.left, window.innerWidth - width - 8)),
    });
    setMoreOpen((open) => !open);
  }

  useEffect(() => {
    if (!moreOpen) return;
    function closeOnOutside(event: PointerEvent) {
      const target = event.target as HTMLElement | null;
      if (target?.closest(".more-menu, .more-menu-trigger")) return;
      setMoreOpen(false);
    }
    function closeOnEscape(event: KeyboardEvent) {
      if (event.key === "Escape") setMoreOpen(false);
    }
    function closeOnViewportChange() {
      setMoreOpen(false);
    }
    document.addEventListener("pointerdown", closeOnOutside);
    document.addEventListener("keydown", closeOnEscape);
    window.addEventListener("resize", closeOnViewportChange);
    window.addEventListener("scroll", closeOnViewportChange, true);
    return () => {
      document.removeEventListener("pointerdown", closeOnOutside);
      document.removeEventListener("keydown", closeOnEscape);
      window.removeEventListener("resize", closeOnViewportChange);
      window.removeEventListener("scroll", closeOnViewportChange, true);
    };
  }, [moreOpen]);

  const statusLabel = isRunning
    ? lifecycle === "starting"
      ? "Starting"
      : lifecycle === "stopping"
        ? "Stopping"
        : "Running"
    : anotherServerRunning
      ? "Blocked"
      : "Stopped";
  const statusClass = isRunning
    ? lifecycle === "running"
      ? "on"
      : "busy"
    : anotherServerRunning
      ? "busy"
      : "";

  return (
    <header className="server-header" aria-label="Server context">
      <button className="mobile-sidebar-button" aria-label="Open sidebar" onClick={onOpenSidebar}>
        <Menu size={18} />
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
        <button className="primary icon-button" disabled={startDisabled} aria-label={busyAction === "start" || lifecycle === "starting" ? "Starting" : "Start"} onClick={() => onAction("start", {}, "start")}><Play size={16} /><span className="server-header-action-label">{busyAction === "start" || lifecycle === "starting" ? "Starting..." : "Start"}</span></button>
        <button className="danger-button icon-button" disabled={runningActionDisabled} aria-label={busyAction === "stop" || lifecycle === "stopping" ? "Stopping" : "Stop"} onClick={() => onAction("stop", {}, "stop")}><Square size={15} /><span className="server-header-action-label">{busyAction === "stop" || lifecycle === "stopping" ? "Stopping..." : "Stop"}</span></button>
        <button className="icon-button" disabled={runningActionDisabled} aria-label={busyAction === "restart" ? "Restarting" : "Restart"} onClick={() => onAction("restart", {}, "restart")}><RotateCw size={16} /><span className="server-header-action-label">{busyAction === "restart" ? "Restarting..." : "Restart"}</span></button>
        <div className="more-menu-wrap">
          <button className="icon-button more-menu-trigger" aria-label="More actions" aria-haspopup="menu" aria-expanded={moreOpen} onClick={(event) => toggleMore(event.currentTarget)}><MoreHorizontal size={16} /><span className="server-header-action-label">More</span></button>
        </div>
        {anotherServerRunning && <small className="control-strip-note">Stop {runningServer?.name ?? "running server"} first</small>}
      </div>
      {moreOpen && typeof document !== "undefined" && createPortal(
        <div className="more-menu" role="menu" style={{ top: menuPosition.top, left: menuPosition.left }}>
          <button className="danger-button icon-button" role="menuitem" disabled={runningActionDisabled} onClick={() => { setMoreOpen(false); onAction("stop", { force: true }, "force"); }}><Zap size={15} />{busyAction === "force" ? "Killing..." : "Force stop"}</button>
          <button className="icon-button" role="menuitem" disabled={refreshBusy} onClick={() => { setMoreOpen(false); onRefresh(); }}><RefreshCw size={15} />{refreshBusy ? "Refreshing..." : "Refresh"}</button>
        </div>,
        document.body,
      )}
    </header>
  );
}
