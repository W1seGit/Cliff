"use client";

import { useEffect, useRef, useState } from "react";
import type { RuntimeStatus, RuntimeUsage, ServerHealth, ServerRecord } from "../lib/types";
import { formatBytes, joinAddressFor, isPublicAddressActive } from "../lib/utils";
import { copyTextToClipboard } from "../lib/clipboard";
import { Button } from "../components/ui/button";
import { Pill } from "../components/ui/pill";
import { AreaChart } from "../components/ui/area-chart";
import { fetchServerProperties, fetchServerUsage } from "../lib/runtime-client";

type WindowKey = "5m" | "15m" | "1h" | "24h";

const WINDOW_DEFS: { key: WindowKey; label: string; ms: number; tickMs: number }[] = [
  { key: "5m", label: "5 min", ms: 5 * 60_000, tickMs: 60_000 },
  { key: "15m", label: "15 min", ms: 15 * 60_000, tickMs: 5 * 60_000 },
  { key: "1h", label: "1 hour", ms: 60 * 60_000, tickMs: 10 * 60_000 },
  { key: "24h", label: "24 hours", ms: 24 * 60 * 60_000, tickMs: 4 * 60 * 60_000 },
];

function formatClockTime(ms: number) {
  return new Date(ms).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}

function generateXTicks(timeStart: number, timeEnd: number, tickMs: number): number[] {
  const ticks: number[] = [];
  const firstTick = Math.ceil(timeStart / tickMs) * tickMs;
  for (let t = firstTick; t < timeEnd; t += tickMs) {
    ticks.push(t);
  }
  return ticks;
}

export function OverviewPanel({
  selected,
  health,
  isRunning,
  runtime,
  setTab,
  onMessage,
  onAcceptEula,
}: {
  selected?: ServerRecord;
  health: ServerHealth | null;
  isRunning: boolean;
  runtime: RuntimeStatus;
  setTab: (tab: string) => void;
  onMessage: (message: string) => void;
  onAcceptEula: () => void;
}) {
  const [windowKey, setWindowKey] = useState<WindowKey>("5m");
  const [windowUsage, setWindowUsage] = useState<RuntimeUsage | null>(null);
  const [nowMs, setNowMs] = useState(() => Date.now());
  const [maxPlayers, setMaxPlayers] = useState(20);
  const fetchTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const windowDef = WINDOW_DEFS.find((w) => w.key === windowKey)!;

  // Fetch usage data when window or server changes, then poll every 5s while running.
  useEffect(() => {
    if (!selected) return;
    let cancelled = false;
    const doLoad = async () => {
      try {
        const result = await fetchServerUsage(selected.id, windowKey);
        if (!cancelled) {
          setWindowUsage(result.usage);
        }
      } catch {
        // ignore
      }
    };
    doLoad();
    if (fetchTimer.current) clearTimeout(fetchTimer.current);
    if (isRunning) {
      const poll = async () => {
        await doLoad();
        if (!cancelled) fetchTimer.current = setTimeout(poll, 5000);
      };
      fetchTimer.current = setTimeout(poll, 5000);
    }
    return () => {
      cancelled = true;
      if (fetchTimer.current) clearTimeout(fetchTimer.current);
    };
  }, [selected, windowKey, isRunning]);

  // Tick nowMs every 1s so the graph scrolls smoothly instead of jumping
  // in 5s steps. Data still updates every 5s via the poll above.
  useEffect(() => {
    const interval = setInterval(() => setNowMs(Date.now()), 1000);
    return () => clearInterval(interval);
  }, []);

  // Fetch server properties for maxPlayers
  useEffect(() => {
    if (!selected) return;
    let alive = true;
    fetchServerProperties(selected.id)
      .then((props) => { if (alive && props.editable?.maxPlayers) setMaxPlayers(props.editable.maxPlayers); })
      .catch(() => { /* use default */ });
    return () => { alive = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selected?.id]);

  if (!selected) {
    return (
      <section className="welcome">
        <div>
          <h2>Set up your first server</h2>
          <p>Import an existing folder or create a fresh Java server profile.</p>
        </div>
        <div className="welcome-actions">
          <Button variant="primary" onClick={() => setTab("import")}>Import existing server</Button>
          <Button onClick={() => setTab("create")}>Create new profile</Button>
        </div>
      </section>
    );
  }

  const joinAddress = joinAddressFor(selected);
  const publicActive = isPublicAddressActive(selected.id);

  // Use windowed usage data for charts, fall back to runtime usage for live stats
  const usage = windowUsage ?? runtime.usage;
  const liveUsage = isRunning ? runtime.usage : null;

  async function copyAddress(value = joinAddress) {
    try {
      await copyTextToClipboard(value);
      onMessage("Join address copied");
    } catch {
      onMessage("Clipboard copy failed");
    }
  }

  const healthChecks = health?.checks ?? [];
  const attentionChecks = healthChecks.filter((check) => check.state !== "ok");

  const overviewStats = [
    { label: "Type", value: selected.type },
    { label: "Version", value: selected.minecraftVersion },
    { label: "Port", value: selected.port },
    { label: "Worlds", value: health?.counts.worlds ?? "—" },
    { label: "Mods", value: health ? `${health.counts.mods}${health.counts.disabledMods ? ` (${health.counts.disabledMods} off)` : ""}` : "—" },
  ];

  // Rolling window — right edge is always "now", ticking every 1s.
  const timeEnd = nowMs;
  const timeStart = timeEnd - windowDef.ms;
  const xTicks = generateXTicks(timeStart, timeEnd, windowDef.tickMs);

  // Resource usage — from windowed data
  const samples = usage?.samples ?? [];
  const memoryLimit = (usage?.memoryLimitBytes && usage.memoryLimitBytes > 0) ? usage.memoryLimitBytes : selected.maxMemoryMb * 1024 * 1024;
  const rawSampleTimes = samples.map((s) => new Date(s.at).getTime());
  const rawCpuValues = samples.map((sample) => sample.cpuPercent ?? 0);
  const rawMemoryValues = samples.map((sample) => (memoryLimit > 0 ? Math.min(100, ((sample.memoryBytes ?? 0) / memoryLimit) * 100) : 0));
  const currentCpu = isRunning ? (liveUsage?.cpuPercent ?? (rawCpuValues.length ? rawCpuValues[rawCpuValues.length - 1] : 0)) : 0;
  const currentMemoryBytes = isRunning ? (liveUsage?.memoryBytes ?? (samples.length ? samples[samples.length - 1].memoryBytes ?? 0 : 0)) : 0;
  const hasResourceData = samples.length > 1;

  // Extend data to timeEnd when running so the line reaches the "Now" marker.
  // Only extend if the last sample is within 30s of now (no gap).
  const lastSampleMs = rawSampleTimes.length > 0 ? rawSampleTimes[rawSampleTimes.length - 1] : null;
  const shouldExtendResource = isRunning && lastSampleMs !== null && (timeEnd - lastSampleMs) < 30_000 && lastSampleMs < timeEnd;
  const sampleTimes = shouldExtendResource ? [...rawSampleTimes, timeEnd] : rawSampleTimes;
  const cpuValues = shouldExtendResource ? [...rawCpuValues, rawCpuValues[rawCpuValues.length - 1]] : rawCpuValues;
  const memoryValues = shouldExtendResource ? [...rawMemoryValues, rawMemoryValues[rawMemoryValues.length - 1]] : rawMemoryValues;

  // Players online — from windowed data
  const playerSamples = usage?.playerSamples ?? [];
  const rawPlayerTimes = playerSamples.map((s) => new Date(s.at).getTime());
  const rawPlayerValues = playerSamples.map((sample) => sample.count);
  const peakPlayers = rawPlayerValues.length ? Math.max(...rawPlayerValues) : 0;
  const hasPlayerData = playerSamples.length > 1;
  const playerMax = Math.max(maxPlayers, peakPlayers + 1);
  const livePlayerCount = liveUsage?.playerSamples?.length ? liveUsage.playerSamples[liveUsage.playerSamples.length - 1].count : null;
  const playerCount = isRunning ? (livePlayerCount ?? 0) : 0;

  // Extend player data to timeEnd when running
  const lastPlayerMs = rawPlayerTimes.length > 0 ? rawPlayerTimes[rawPlayerTimes.length - 1] : null;
  const shouldExtendPlayers = isRunning && lastPlayerMs !== null && (timeEnd - lastPlayerMs) < 30_000 && lastPlayerMs < timeEnd;
  const playerTimes = shouldExtendPlayers ? [...rawPlayerTimes, timeEnd] : rawPlayerTimes;
  // Normalize player counts to 0-100% for the shared y-axis
  const playerValuesPct = (shouldExtendPlayers ? [...rawPlayerValues, rawPlayerValues[rawPlayerValues.length - 1]] : rawPlayerValues)
    .map((v) => playerMax > 0 ? (v / playerMax) * 100 : 0);

  // Combined chart has data if any series has data
  const hasChartData = hasResourceData || hasPlayerData;

  // Server stopped/start markers
  const lastSampleAt = usage?.lastSampleAt;
  const stoppedMarkerTime = (!isRunning && lastSampleAt) ? new Date(lastSampleAt).getTime() : null;
  // Start marker: use the actual server start time from runtime, not the first
  // sample in the window (which shifts as the rolling window scrolls).
  const runtimeStartMs = runtime.startedAt ? new Date(runtime.startedAt).getTime() : null;
  const startMarkerTime = (isRunning && runtimeStartMs !== null && runtimeStartMs >= timeStart && runtimeStartMs <= timeEnd) ? runtimeStartMs : null;

  return (
    <section className="overview-layout">
      <div className="overview-hero">
        <span className="overview-hero-label">{publicActive ? "Public join address" : "Join address"}</span>
        <button className="overview-hero-address" onClick={() => copyAddress()} aria-label="Copy join address">
          <strong>{joinAddress}</strong>
          <em>Copy</em>
        </button>
      </div>

      <div className="overview-stat-cards">
        {overviewStats.map((stat) => (
          <div key={String(stat.label)} className="overview-stat-card">
            <span className="overview-stat-card-label">{stat.label}</span>
            <strong className="overview-stat-card-value">{stat.value}</strong>
          </div>
        ))}
      </div>

      <div className="chart-card">
        <div className="chart-card-head">
          <div className="chart-card-heading">
            <h2>Server usage</h2>
            <p>{isRunning && hasChartData ? "live" : (hasChartData ? "historical" : "no data")}</p>
          </div>
          <div className="chart-card-controls">
            <div className="chart-legend">
              <span className="chart-legend-item">
                <span className="chart-legend-dot" style={{ background: "var(--chart-cpu)" }} />
                CPU <strong>{currentCpu.toFixed(0)}%</strong>
              </span>
              <span className="chart-legend-item">
                <span className="chart-legend-dot" style={{ background: "var(--chart-mem)" }} />
                Memory <strong>{formatBytes(currentMemoryBytes)} / {formatBytes(memoryLimit)}</strong>
              </span>
              <span className="chart-legend-item">
                <span className="chart-legend-dot" style={{ background: "var(--chart-players)" }} />
                Players <strong>{playerCount} / {maxPlayers}</strong>
              </span>
            </div>
            <div className="window-switcher">
              {WINDOW_DEFS.map((w) => (
                <button
                  key={w.key}
                  className={`window-switcher-btn ${windowKey === w.key ? "active" : ""}`}
                  onClick={() => setWindowKey(w.key)}
                >
                  {w.label}
                </button>
              ))}
            </div>
          </div>
        </div>
        <AreaChart
          height={300}
          max={100}
          timeStart={timeStart}
          timeEnd={timeEnd}
          yTicks={[0, 25, 50, 75, 100]}
          yFormat={(value) => `${value}%`}
          xTicks={xTicks}
          xFormat={formatClockTime}
          stoppedAt={stoppedMarkerTime}
          startedAt={startMarkerTime}
          emptyLabel=""
          series={hasChartData ? [
            { values: memoryValues, times: sampleTimes, color: "var(--chart-mem)", label: "Memory", format: (v: number) => formatBytes((v / 100) * memoryLimit) },
            { values: cpuValues, times: sampleTimes, color: "var(--chart-cpu)", label: "CPU", format: (v: number) => `${v.toFixed(1)}%` },
            { values: playerValuesPct, times: playerTimes, color: "var(--chart-players)", label: "Players", format: (v: number) => `${Math.round((v / 100) * playerMax)} / ${maxPlayers}` },
          ].filter((s) => s.values.length > 0) : []}
        />
      </div>

      {attentionChecks.length > 0 && (
        <div className="overview-section">
          <div className="overview-section-head">
            <h2>Health checks</h2>
            <Pill variant="warning">{attentionChecks.length} to review</Pill>
          </div>
          <div className="action-list">
            {attentionChecks.map((check) => (
              <Button key={check.id} className={`action-item ${check.state}`} onClick={() => check.id === "eula" ? onAcceptEula() : setTab(["launch", "java", "properties", "memory", "port"].includes(check.id) ? "settings" : "overview")}>
                <span>
                  <strong>{check.label}</strong>
                  <small>{check.detail}</small>
                </span>
                <em>Fix</em>
              </Button>
            ))}
          </div>
        </div>
      )}

      {!health && (
        <p className="muted overview-scanning">Scanning server folder...</p>
      )}
    </section>
  );
}
