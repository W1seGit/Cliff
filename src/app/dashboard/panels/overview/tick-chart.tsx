"use client";

import type { RuntimeUsage, ServerRecord } from "../../lib/types";
import { AreaChart } from "../../components/ui/area-chart";
import { Pill } from "../../components/ui/pill";

export type TickWindow = { key: string; label: string };

type Props = {
  server: ServerRecord;
  usage: RuntimeUsage | null | undefined;
  isRunning: boolean;
  windows: TickWindow[];
  windowKey: string;
  onWindowChange: (key: string) => void;
  timeStart: number;
  timeEnd: number;
  xTicks: number[];
  xFormat: (time: number) => string;
  stoppedAt: number | null;
  startedAt: number | null;
};

const PAPER_FAMILY = new Set(["paper", "purpur", "folia"]);

/** Vanilla reports tick speed through "tick query", added in Minecraft 1.20.3. Paper-family servers always can. */
export function canReportTicks(server: Pick<ServerRecord, "type" | "minecraftVersion">) {
  if (PAPER_FAMILY.has(server.type)) return true;
  const match = /^(\d+)\.(\d+)(?:\.(\d+))?/.exec(server.minecraftVersion);
  if (!match) return true; // snapshots and odd version strings: assume yes
  const [major, minor, patch] = [Number(match[1]), Number(match[2]), Number(match[3] ?? 0)];
  if (major !== 1) return major > 1;
  return minor > 20 || (minor === 20 && patch >= 3);
}

function tpsVariant(tps: number): "success" | "warning" | "danger" {
  return tps >= 19 ? "success" : tps >= 15 ? "warning" : "danger";
}

function tpsWord(tps: number) {
  return tps >= 19 ? "Smooth" : tps >= 15 ? "Slowing down" : "Lagging";
}

function niceMax(value: number) {
  const steps = [50, 100, 200, 500, 1000];
  return steps.find((step) => value <= step) ?? Math.ceil(value / 500) * 500;
}

export function TickChart({ server, usage, isRunning, windows, windowKey, onWindowChange, timeStart, timeEnd, xTicks, xFormat, stoppedAt, startedAt }: Props) {
  const samples = (usage?.tickSamples ?? []).filter((sample) => sample.tps !== null || sample.mspt !== null);
  const latest = isRunning ? (usage?.tick ?? samples[samples.length - 1] ?? null) : null;
  const hasHistory = samples.length > 0 || Boolean(usage?.tick);

  // Nothing to say about a stopped server that never reported a tick speed.
  if (!isRunning && !hasHistory) return null;

  const supported = canReportTicks(server);
  const tpsPoints = samples.filter((sample) => sample.tps !== null);
  const msptPoints = samples.filter((sample) => sample.mspt !== null);
  const tpsTimes = tpsPoints.map((sample) => new Date(sample.at).getTime());
  const msptTimes = msptPoints.map((sample) => new Date(sample.at).getTime());
  const msptValues = msptPoints.map((sample) => sample.mspt as number);
  const msptMax = niceMax(Math.max(50, ...msptValues));
  const currentTps = latest?.tps ?? null;
  const currentMspt = latest?.mspt ?? null;

  let status: string | null = null;
  if (!hasHistory) {
    status = supported
      ? "Waiting for the first reading (about 30 seconds after start)"
      : "This server version cannot report tick speed. It needs Minecraft 1.20.3 or newer, or a Paper-based server.";
  }

  return (
    <section className="chart-card tick-chart" aria-label="Tick speed">
      <div className="chart-card-head">
        <div className="chart-card-heading">
          <h2>Tick speed</h2>
          <p>{isRunning ? "live" : "historical"}</p>
        </div>
        <div className="chart-card-controls">
          <div className="tick-chart-readout">
            {currentTps !== null && (
              <span className="tick-chart-stat">
                TPS <strong>{currentTps.toFixed(1)}</strong>
                <Pill variant={tpsVariant(currentTps)}>{tpsWord(currentTps)}</Pill>
              </span>
            )}
            {currentMspt !== null && (
              <span className="tick-chart-stat">
                MSPT <strong>{currentMspt.toFixed(1)} ms</strong>
              </span>
            )}
          </div>
          <div className="window-switcher" role="group" aria-label="Time window">
            {windows.map((w) => (
              <button
                key={w.key}
                type="button"
                className={`window-switcher-btn ${windowKey === w.key ? "active" : ""}`}
                aria-pressed={windowKey === w.key}
                onClick={() => onWindowChange(w.key)}
              >
                {w.label}
              </button>
            ))}
          </div>
        </div>
      </div>

      {status ? (
        <p className="chart-empty" role="status">{status}</p>
      ) : (
        <div className="tick-chart-grid">
          <figure className="tick-chart-figure">
            <figcaption>TPS <span className="muted">(20 is perfect)</span></figcaption>
            <AreaChart
              height={140}
              max={20}
              timeStart={timeStart}
              timeEnd={timeEnd}
              yTicks={[0, 5, 10, 15, 20]}
              yFormat={(value) => String(value)}
              xTicks={xTicks}
              xFormat={xFormat}
              stoppedAt={stoppedAt}
              startedAt={startedAt}
              emptyLabel="No TPS readings in this window"
              series={[{ values: tpsPoints.map((sample) => sample.tps as number), times: tpsTimes, color: "var(--chart-tps)", label: "TPS", format: (v: number) => v.toFixed(1) }]}
            />
          </figure>
          <figure className="tick-chart-figure">
            <figcaption>MSPT <span className="muted">(under 50 ms keeps up)</span></figcaption>
            <AreaChart
              height={140}
              max={msptMax}
              timeStart={timeStart}
              timeEnd={timeEnd}
              yTicks={[0, msptMax / 2, msptMax]}
              yFormat={(value) => `${value} ms`}
              xTicks={xTicks}
              xFormat={xFormat}
              stoppedAt={stoppedAt}
              startedAt={startedAt}
              emptyLabel="No MSPT readings in this window"
              series={[{ values: msptValues, times: msptTimes, color: "var(--chart-cpu)", label: "MSPT", format: (v: number) => `${v.toFixed(1)} ms` }]}
            />
          </figure>
        </div>
      )}
    </section>
  );
}
