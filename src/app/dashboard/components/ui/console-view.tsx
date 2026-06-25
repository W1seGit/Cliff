"use client";

import { type ReactNode, useEffect, useRef } from "react";

export interface ConsoleLine {
  key: string;
  time: string;
  text: string;
  level: "error" | "warning" | "command" | "info";
}

function logLevelFor(line: string): ConsoleLine["level"] {
  if (line.trim().startsWith(">")) return "command";
  if (/\b(error|exception|failed|fatal|crash|caused by)\b/i.test(line)) return "error";
  if (/\b(warn|warning|deprecated)\b/i.test(line)) return "warning";
  return "info";
}

export function parseConsoleLines(lines: string[]): ConsoleLine[] {
  let lastTime = "--:--:--";
  let lastLevel: ConsoleLine["level"] = "info";
  return lines.map((line, index): ConsoleLine => {
    // Match Minecraft-style timestamps: [12:30:45] or 12:30:45
    let match = line.match(/^\[?(\d{2}:\d{2}:\d{2})\]?\s*(.*)$/);
    // Match daemon log timestamps (live buffer): 2024-01-15 12:30:45 message
    if (!match) match = line.match(/^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\s+(.*)$/);
    // Match slog text handler format (full log file): time=2024-01-15T12:30:45.123Z level=INFO msg="..."
    // Extracts the msg content (quoted or unquoted) and appends any trailing fields.
    if (!match) {
      const slog = line.match(/^time=(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2}:\d{2})\.\d+(?:Z|[+-]\d{2}:\d{2})\s+level=(\w+)\s+msg=(?:"([^"]*)"|(\S+))\s*(.*)$/);
      if (slog) {
        lastTime = `${slog[1]} ${slog[2]}`;
        const slogLevel = slog[3].toUpperCase();
        const ownLevel: ConsoleLine["level"] = slogLevel === "ERROR" ? "error" : slogLevel === "WARN" ? "warning" : "info";
        const level = ownLevel === "info" && /^\s+(at|\.\.\.)\b/.test(line) ? lastLevel : ownLevel;
        lastLevel = level;
        const msgText = slog[4] ?? slog[5] ?? "";
        const rest = slog[6]?.trim();
        const text = rest ? `${msgText} ${rest}` : msgText;
        return { key: `${line}-${index}`, time: lastTime, text, level };
      }
    }
    if (match) lastTime = match[1];
    const ownLevel = logLevelFor(line);
    const level = ownLevel === "info" && /^\s+(at|\.\.\.)\b/.test(line) ? lastLevel : ownLevel;
    lastLevel = level;
    return {
      key: `${line}-${index}`,
      time: lastTime,
      text: match ? match[2] : line,
      level,
    };
  });
}

const FOLLOW_THRESHOLD_PX = 80;

function isNearBottom(el: HTMLElement): boolean {
  return el.scrollHeight - el.scrollTop - el.clientHeight < FOLLOW_THRESHOLD_PX;
}

export interface ConsoleViewProps {
  lines: string[];
  /** Pre-parsed lines. When provided, `lines` is ignored and no re-parsing happens. */
  parsedLines?: ConsoleLine[];
  emptyMessage?: string;
  className?: string;
  header?: ReactNode;
}

export function ConsoleView({
  lines,
  parsedLines,
  emptyMessage = "No logs yet.",
  className = "",
  header,
}: ConsoleViewProps) {
  const consoleRef = useRef<HTMLDivElement | null>(null);
  const atBottomRef = useRef(true);
  const renderedLines = parsedLines ?? parseConsoleLines(lines);

  useEffect(() => {
    if (atBottomRef.current && consoleRef.current) {
      consoleRef.current.scrollTop = consoleRef.current.scrollHeight;
    }
  }, [renderedLines.length]);

  return (
    <div className={`console-view ${className}`.trim()}>
      {header && <div className="console-view-header">{header}</div>}
      <div className="console" ref={consoleRef} onScroll={() => {
        if (consoleRef.current) atBottomRef.current = isNearBottom(consoleRef.current);
      }}>
        {renderedLines.length ? renderedLines.map((line) => (
          <div key={line.key} className={`console-line ${line.level}`}>
            <span className="console-time">{line.time}</span>
            <pre>{line.text}</pre>
          </div>
        )) : (
          <div className="console-line muted-line"><span className="console-time">--:--:--</span><pre>{emptyMessage}</pre></div>
        )}
      </div>
    </div>
  );
}