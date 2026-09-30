"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ArrowDownToLine, Download, Terminal } from "lucide-react";
import { fetchServerProperties, logsUrl, sendRuntimeCommand } from "../lib/runtime-client";
import type { RuntimeStatus, ServerRecord } from "../lib/types";
import { Button } from "../components/ui/button";
import { Page } from "../components/ui/page-layout";
import { StatusDot } from "../components/ui/status-dot";
import { CopyButton } from "../components/ui/copy-button";
import { Banner } from "../components/ui/banner";
import { Input } from "../components/ui/input";

type ConsoleLine = {
  key: string;
  time: string;
  text: string;
  level: "error" | "warning" | "command" | "info";
};

function logLevelFor(line: string): ConsoleLine["level"] {
  if (line.trim().startsWith(">")) return "command";
  if (/\b(error|exception|failed|fatal|crash|caused by)\b/i.test(line)) return "error";
  if (/\b(warn|warning|deprecated)\b/i.test(line)) return "warning";
  return "info";
}

function consoleLines(lines: string[]) {
  let lastTime = "--:--:--";
  let lastLevel: ConsoleLine["level"] = "info";
  return lines.map((line, index): ConsoleLine => {
    const match = line.match(/^\[?(\d{2}:\d{2}:\d{2})\]?\s*(.*)$/);
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

export function ConsolePanel({
  selected,
  isRunning,
  anotherServerRunning,
  runningServer,
  logs,
  onCommand,
  onMessage,
  onRefresh,
  onAcceptEula,
}: {
  selected: ServerRecord;
  isRunning: boolean;
  anotherServerRunning: boolean;
  runningServer?: ServerRecord;
  runtime: RuntimeStatus;
  logs: string[];
  onCommand?: ((command: string) => boolean) | null;
  onMessage: (message: string) => void;
  onRefresh: () => void;
  onAcceptEula: () => void;
}) {
  const [command, setCommand] = useState("");
  const [busyAction, setBusyAction] = useState("");
  const [showJumpButton, setShowJumpButton] = useState(false);
  const consoleRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const atBottomRef = useRef(true);
  const trimmedCommand = command.trim();
  const logLines = useMemo(() => (Array.isArray(logs) ? logs : []), [logs]);
  const renderedLogs = consoleLines(logLines);

  const scrollToBottom = useCallback(() => {
    if (consoleRef.current) {
      consoleRef.current.scrollTop = consoleRef.current.scrollHeight;
      atBottomRef.current = true;
      setShowJumpButton(false);
    }
  }, []);

  useEffect(() => {
    if (atBottomRef.current && consoleRef.current) {
      consoleRef.current.scrollTop = consoleRef.current.scrollHeight;
    }
  }, [logLines.length]);

  // Detect the server's "you need to agree to the EULA" message and offer the
  // accept dialog. The message stays in the log after the EULA is accepted, so
  // ask the server whether it is really unaccepted before prompting; otherwise
  // every visit to this tab would re-open the dialog until the next start.
  const eulaPromptedRef = useRef(false);
  const mountedRef = useRef(true);
  useEffect(() => {
    mountedRef.current = true;
    return () => { mountedRef.current = false; };
  }, []);
  useEffect(() => {
    if (eulaPromptedRef.current || logLines.length === 0) return;
    const recent = logLines.slice(-30);
    const detected = recent.some((line) => /eula/i.test(line) && /agree|accept|need/i.test(line));
    if (!detected) return;
    eulaPromptedRef.current = true;
    fetchServerProperties(selected.id)
      .then((properties) => {
        if (mountedRef.current && !properties.eulaAccepted) onAcceptEula();
      })
      .catch(() => {
        // If the check fails, fall back to prompting so a real EULA problem is not hidden.
        if (mountedRef.current) onAcceptEula();
      });
  }, [logLines, onAcceptEula, selected.id]);

  // Reset the EULA prompt tracker when the server starts running again so
  // the modal can reappear if the EULA issue recurs on a later start.
  useEffect(() => {
    if (isRunning) eulaPromptedRef.current = false;
  }, [isRunning]);

  async function action(path: string, body = {}) {
    if (busyAction) return false;
    setBusyAction(path);
    try {
      if (path === "command" && "command" in body && typeof body.command === "string") {
        if (onCommand) {
          if (!onCommand(body.command)) throw new Error("Console connection is not ready");
        } else {
          await sendRuntimeCommand(selected.id, body.command);
          await onRefresh();
        }
      } else {
        throw new Error("Unsupported console action");
      }
      return true;
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Action failed");
      return false;
    } finally {
      setBusyAction("");
    }
  }

  async function submitCommand() {
    if (!trimmedCommand) return;
    scrollToBottom();
    const ok = await action("command", { command: trimmedCommand });
    if (ok) setCommand("");
    window.setTimeout(() => inputRef.current?.focus(), 0);
  }

  return (
    <Page
      className="console-page"
      title="Console"
      description="Send commands and read live output from the server."
      icon={<Terminal size={20} />}
      toolbar={
        <div className="console-toolbar">
          <span className="console-state">
            <StatusDot tone={isRunning ? "running" : "neutral"} />
            {isRunning ? "Live output" : logLines.length ? "Last output" : "Not running"}
          </span>
          <span className="console-toolbar-actions">
            <CopyButton text={logLines.join("\n")} label="Copy log" onError={() => onMessage("Copy failed")} />
            <Button size="sm" iconLeft={<Download size={14} />} href={logsUrl(selected.id, "?download=1")} download>
              Download
            </Button>
          </span>
        </div>
      }
    >
      {anotherServerRunning && (
        <Banner variant="warning">Console commands are disabled because {runningServer?.name ?? "another server"} is running.</Banner>
      )}

      <div className="console-card">
      <div className="console-wrapper">
        <div className="console" ref={consoleRef} onScroll={() => {
          if (consoleRef.current) {
            const near = isNearBottom(consoleRef.current);
            atBottomRef.current = near;
            setShowJumpButton(!near);
          }
        }} onClick={() => {
          const sel = window.getSelection();
          if (!sel || sel.toString().length === 0) inputRef.current?.focus();
        }}>
          {renderedLogs.length ? renderedLogs.map((line) => (
            <div key={line.key} className={`console-line ${line.level}`}>
              <span className="console-time">{line.time}</span>
              <pre>{line.text}</pre>
            </div>
          )) : (
            <div className="console-line muted-line"><span className="console-time">--:--:--</span><pre>No live logs yet.</pre></div>
          )}
        </div>
        {showJumpButton && (
          <button className="console-jump-bottom" onClick={scrollToBottom} aria-label="Jump to latest">
            <ArrowDownToLine size={14} />
            Jump to latest
          </button>
        )}
      </div>

      <form className="console-prompt" onSubmit={(event) => { event.preventDefault(); submitCommand(); }}>
        <div className="console-input-line">
          <span className="console-prompt-char">&gt;</span>
          <Input
            ref={inputRef}
            disabled={!isRunning || Boolean(busyAction) || anotherServerRunning}
            placeholder={isRunning ? "Type a command, e.g. say Hello" : "Start the server to send commands"}
            value={command}
            onChange={(event) => setCommand(event.target.value)}
          />
          <Button variant="primary" size="sm" disabled={!isRunning || !trimmedCommand || Boolean(busyAction) || anotherServerRunning} loading={busyAction === "command"} loadingText="Sending...">Send</Button>
        </div>
      </form>
      </div>
    </Page>
  );
}