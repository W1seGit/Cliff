"use client";

import { useId, useRef, useState } from "react";

export interface ChartSeries {
  /** y-values */
  values: number[];
  /** epoch ms timestamps, same length as values */
  times: number[];
  /** stroke + fill color */
  color: string;
  /** opacity of the gradient fill at the top of the area (default 0.28) */
  fillOpacity?: number;
  /** label shown in the tooltip for this series */
  label?: string;
  /** format a y-value for this series in the tooltip */
  format?: (value: number) => string;
}

export interface AreaChartProps {
  series: ChartSeries[];
  /** maximum value used to scale the y-axis */
  max: number;
  /** left edge of the chart in epoch ms */
  timeStart: number;
  /** right edge of the chart in epoch ms */
  timeEnd: number;
  /** rendered height of the plot area in px */
  height?: number;
  /** y-axis tick values; drawn as gridlines + labels when yFormat is given */
  yTicks?: number[];
  /** format a y-tick value into a label; omit to hide the y-axis */
  yFormat?: (value: number) => string;
  /** x-axis tick positions in epoch ms */
  xTicks?: number[];
  /** format an x-tick time into a label */
  xFormat?: (time: number) => string;
  /** message shown when there is no data to plot */
  emptyLabel?: string;
  /** if set, draws a "stopped" marker line at this timestamp */
  stoppedAt?: number | null;
  /** if set, draws a "started" marker line at this timestamp */
  startedAt?: number | null;
  className?: string;
}

const VB_W = 1000;
const VB_H = 100;

function timeToX(time: number, timeStart: number, timeEnd: number) {
  const span = timeEnd - timeStart;
  if (span <= 0) return 0;
  return ((time - timeStart) / span) * VB_W;
}

function buildPaths(values: number[], times: number[], timeStart: number, timeEnd: number, max: number, gapMs: number, breakPoints?: number[]) {
  const segments = computeSegments(times, gapMs, breakPoints);
  const safeMax = max > 0 ? max : 1;

  const lineParts: string[] = [];
  const areaParts: string[] = [];

  for (const seg of segments) {
    const segPts = seg.indices.map((idx) => {
      const x = timeToX(times[idx], timeStart, timeEnd);
      const ratio = Math.max(0, Math.min(1, values[idx] / safeMax));
      return [x, (1 - ratio) * VB_H] as const;
    });
    const first = segPts[0];
    const last = segPts[segPts.length - 1];

    if (segPts.length === 1) {
      lineParts.push(`M${first[0].toFixed(2)} ${first[1].toFixed(2)}`);
    } else {
      const line = segPts.slice(1).map((point) => `L${point[0].toFixed(2)} ${point[1].toFixed(2)}`).join(" ");
      lineParts.push(`M${first[0].toFixed(2)} ${first[1].toFixed(2)} ${line}`);
      areaParts.push(`M${first[0].toFixed(2)} ${first[1].toFixed(2)} ${line} L${last[0].toFixed(2)} ${VB_H} L${first[0].toFixed(2)} ${VB_H} Z`);
    }
  }

  return { line: lineParts.join(" "), area: areaParts.join(" ") };
}

/** A contiguous segment of data point indices (no gaps or breaks within). */
interface Segment {
  indices: number[];
  startTime: number;
  endTime: number;
}

/**
 * Split sample timestamps into contiguous segments, breaking at gaps > gapMs
 * or at any break point timestamp (e.g. server start/stop boundaries).
 */
function computeSegments(times: number[], gapMs: number, breakPoints?: number[]): Segment[] {
  const n = times.length;
  if (n === 0) return [];

  const breakSet = new Set<number>();
  if (breakPoints) {
    for (const bp of breakPoints) breakSet.add(bp);
  }

  function isInBreak(t1: number, t2: number): boolean {
    for (const bp of breakSet) {
      if (t1 < bp && t2 >= bp) return true;
    }
    return false;
  }

  const segments: Segment[] = [];
  let segStart = 0;
  for (let i = 1; i <= n; i++) {
    const isGap = i < n && (times[i] - times[i - 1]) > gapMs;
    const isBreak = i < n && isInBreak(times[i - 1], times[i]);
    const isEnd = i === n;
    if (isGap || isBreak || isEnd) {
      const indices: number[] = [];
      for (let j = segStart; j < i; j++) indices.push(j);
      if (indices.length > 0) {
        segments.push({
          indices,
          startTime: times[segStart],
          endTime: times[i - 1],
        });
      }
      segStart = i;
    }
  }
  return segments;
}

export function AreaChart({
  series,
  max,
  timeStart,
  timeEnd,
  height = 240,
  yTicks,
  yFormat,
  xTicks,
  xFormat,
  emptyLabel = "No data yet",
  stoppedAt = null,
  startedAt = null,
  className = "",
}: AreaChartProps) {
  const uid = useId().replace(/:/g, "");
  const hasData = series.some((s) => s.values.length > 0);
  const showYAxis = Boolean(yFormat && yTicks && yTicks.length > 0);
  const showXAxis = Boolean(xFormat && xTicks && xTicks.length > 0);
  const safeMax = max > 0 ? max : 1;
  const plotRef = useRef<HTMLDivElement>(null);
  const [hoverX, setHoverX] = useState<number | null>(null);
  const [plotWidth, setPlotWidth] = useState(0);
  const [plotOffset, setPlotOffset] = useState(0);

  // Collect all timestamps from the first non-empty series for tooltip lookup
  const tooltipTimes = series.find((s) => s.times.length > 0)?.times ?? [];

  // Compute segments using the same logic as the graph rendering (including
  // break points from startedAt/stoppedAt), so the tooltip respects gaps.
  const GAP_MS = 30_000;
  const breakPoints: number[] = [];
  if (stoppedAt !== null) breakPoints.push(stoppedAt);
  if (startedAt !== null) breakPoints.push(startedAt);
  const tooltipSegments = computeSegments(tooltipTimes, GAP_MS, breakPoints.length > 0 ? breakPoints : undefined);

  // Gap regions for the visual hatching pattern (derived from segments)
  const gapRegions: { start: number; end: number }[] = [];
  for (let i = 1; i < tooltipSegments.length; i++) {
    gapRegions.push({ start: tooltipSegments[i - 1].endTime, end: tooltipSegments[i].startTime });
  }

  function handleMove(e: React.MouseEvent<HTMLDivElement>) {
    if (!hasData || !plotRef.current) return;
    const rect = plotRef.current.getBoundingClientRect();
    const offset = showYAxis ? 44 : 0;
    const width = Math.max(1, rect.width - offset);
    const x = e.clientX - rect.left - offset;
    setPlotOffset(offset);
    setPlotWidth(width);
    setHoverX(Math.max(0, Math.min(width, x)));
  }

  function handleLeave() {
    setHoverX(null);
  }

  // Convert hoverX (px) to time, then find the segment and nearest sample
  let hoverTime: number | null = null;
  let hoverIdx: number | null = null;
  let noData = true;
  if (hoverX !== null && plotWidth > 0 && tooltipTimes.length > 0) {
    const fraction = hoverX / plotWidth;
    hoverTime = timeStart + fraction * (timeEnd - timeStart);
    // Find which segment the hover time falls into
    for (const seg of tooltipSegments) {
      if (hoverTime >= seg.startTime && hoverTime <= seg.endTime) {
        // Find nearest sample within this segment
        let bestDist = Infinity;
        for (const idx of seg.indices) {
          const dist = Math.abs(tooltipTimes[idx] - hoverTime);
          if (dist < bestDist) {
            bestDist = dist;
            hoverIdx = idx;
          }
        }
        noData = false;
        break;
      }
    }
  }

  const cursorPercent = hoverX !== null && plotWidth > 0
    ? (hoverX / plotWidth) * 100
    : null;
  const cursorLeft = hoverX !== null ? plotOffset + hoverX : null;

  // Determine tooltip flip side if near right edge
  const tooltipFlip = cursorPercent !== null && cursorPercent > 70;

  // Stopped marker position
  const stoppedPercent = stoppedAt !== null && stoppedAt >= timeStart && stoppedAt <= timeEnd
    ? ((stoppedAt - timeStart) / (timeEnd - timeStart)) * 100
    : null;

  // Started marker position
  const startedPercent = startedAt !== null && startedAt >= timeStart && startedAt <= timeEnd
    ? ((startedAt - timeStart) / (timeEnd - timeStart)) * 100
    : null;

  return (
    <div className={`area-chart ${showYAxis ? "has-y-axis" : ""} ${className}`.trim()}>
      <div
        className="area-chart-plot"
        style={{ height }}
        ref={plotRef}
        onMouseMove={handleMove}
        onMouseLeave={handleLeave}
      >
        {showYAxis && (
          <div className="area-chart-y-axis" aria-hidden="true">
            {yTicks!.map((tick) => (
              <span key={tick} className="area-chart-y-tick" style={{ bottom: `${(tick / safeMax) * 100}%` }}>
                {yFormat!(tick)}
              </span>
            ))}
          </div>
        )}
        <svg className="area-chart-svg" viewBox={`0 0 ${VB_W} ${VB_H}`} preserveAspectRatio="none" role="img">
          <defs>
            {series.map((s, index) => (
              <linearGradient key={index} id={`${uid}-fill-${index}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={s.color} stopOpacity={s.fillOpacity ?? 0.28} />
                <stop offset="100%" stopColor={s.color} stopOpacity={0} />
              </linearGradient>
            ))}
            <pattern id={`${uid}-gap`} patternUnits="userSpaceOnUse" width="6" height="6" patternTransform="rotate(45)">
              <line x1="0" y1="0" x2="0" y2="6" stroke="var(--text-tertiary)" strokeWidth="1" opacity="0.15" />
            </pattern>
          </defs>
          {showYAxis && yTicks!.map((tick) => {
            const y = (1 - tick / safeMax) * VB_H;
            return <line key={tick} className="area-chart-grid" x1={0} x2={VB_W} y1={y} y2={y} vectorEffect="non-scaling-stroke" />;
          })}
          {showXAxis && xTicks!.map((tick) => {
            const x = timeToX(tick, timeStart, timeEnd);
            return <line key={tick} className="area-chart-grid" x1={x} x2={x} y1={0} y2={VB_H} vectorEffect="non-scaling-stroke" />;
          })}
          {hasData && gapRegions.map((gap, i) => {
            const x1 = timeToX(gap.start, timeStart, timeEnd);
            const x2 = timeToX(gap.end, timeStart, timeEnd);
            return <rect key={`gap-${i}`} x={x1} y={0} width={Math.max(0, x2 - x1)} height={VB_H} fill={`url(#${uid}-gap)`} />;
          })}
          {hasData && series.map((s, index) => {
            // Pass startedAt as a break point so the line splits at the
            // server start boundary. Samples before startedAt belong to a
            // previous session and should not connect to the current session.
            // Also pass stoppedAt when available (server is stopped).
            const breakPoints: number[] = [];
            if (stoppedAt !== null && stoppedAt >= timeStart && stoppedAt <= timeEnd) breakPoints.push(stoppedAt);
            if (startedAt !== null && startedAt >= timeStart && startedAt <= timeEnd) breakPoints.push(startedAt);
            // If only startedAt is present (server running after restart),
            // use it as a single break point — split at that timestamp
            const breaks = breakPoints.length >= 1 ? breakPoints : undefined;
            const { line, area } = buildPaths(s.values, s.times, timeStart, timeEnd, max, 30000, breaks);
            return (
              <g key={index} className="area-chart-series">
                <path d={area} fill={`url(#${uid}-fill-${index})`} stroke="none" />
                <path d={line} fill="none" stroke={s.color} strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" vectorEffect="non-scaling-stroke" />
              </g>
            );
          })}
          {hasData && stoppedAt !== null && stoppedAt >= timeStart && stoppedAt <= timeEnd && (
            <line className="area-chart-stopped-svg-line" x1={timeToX(stoppedAt, timeStart, timeEnd)} x2={timeToX(stoppedAt, timeStart, timeEnd)} y1={0} y2={VB_H} vectorEffect="non-scaling-stroke" />
          )}
          {hasData && startedAt !== null && startedAt >= timeStart && startedAt <= timeEnd && (
            <line className="area-chart-started-svg-line" x1={timeToX(startedAt, timeStart, timeEnd)} x2={timeToX(startedAt, timeStart, timeEnd)} y1={0} y2={VB_H} vectorEffect="non-scaling-stroke" />
          )}
          {/* "Now" marker line at the right edge */}
          <line className="area-chart-now-line" x1={VB_W} x2={VB_W} y1={0} y2={VB_H} vectorEffect="non-scaling-stroke" />
        </svg>
        {emptyLabel && !hasData && (
          <div className="area-chart-empty">{emptyLabel}</div>
        )}
        {hasData && stoppedPercent !== null && (
          <div className="area-chart-stopped" style={{ left: `${stoppedPercent}%` }}>
            <div className="area-chart-stopped-label">
              Server stopped
            </div>
          </div>
        )}
        {hasData && startedPercent !== null && (
          <div className="area-chart-started" style={{ left: `${startedPercent}%` }}>
            <div className="area-chart-started-label">
              Server started
            </div>
          </div>
        )}
        {hasData && cursorPercent !== null && cursorLeft !== null && (
          <>
            <div className="area-chart-cursor" style={{ left: cursorLeft }} />
            {hoverIdx !== null && hoverTime !== null && (
              <div
                className={`area-chart-tooltip ${tooltipFlip ? "flip" : ""}`}
                style={{ left: cursorLeft }}
              >
                <span className="area-chart-tooltip-time">
                  {new Date(hoverTime).toLocaleTimeString([], { hour: "numeric", minute: "2-digit", second: "2-digit" })}
                </span>
                {noData ? (
                  <span className="area-chart-tooltip-nodata">No data</span>
                ) : (
                  series.filter((s) => s.values.length > 0).map((s, i) => {
                    const val = s.values[Math.min(hoverIdx!, s.values.length - 1)] ?? 0;
                    const fmt = s.format ?? ((v) => v.toFixed(0));
                    return (
                      <span key={i} className="area-chart-tooltip-row">
                        <span className="area-chart-tooltip-dot" style={{ background: s.color }} />
                        {s.label && <span className="area-chart-tooltip-label">{s.label}</span>}
                        <strong>{fmt(val)}</strong>
                      </span>
                    );
                  })
                )}
              </div>
            )}
          </>
        )}
      </div>
      {showXAxis && (
        <div className="area-chart-x-axis" aria-hidden="true">
          <div className="area-chart-x-axis-inner">
            {xTicks!.map((tick) => {
              const percent = ((tick - timeStart) / (timeEnd - timeStart)) * 100;
              // Skip ticks too close to the right edge (would overlap "Now" line)
              if (percent > 95) return null;
              return (
                <span
                  key={tick}
                  className="area-chart-x-tick"
                  style={{ position: "absolute", left: `${percent}%`, transform: "translateX(-50%)" }}
                >
                  {xFormat!(tick)}
                </span>
              );
            })}
            <span
              className="area-chart-x-tick area-chart-now-label"
              style={{ position: "absolute", right: 0, transform: "translateX(50%)" }}
            >
              Now
            </span>
          </div>
        </div>
      )}
    </div>
  );
}
