"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { AlertTriangle, Check, Circle, Loader2 } from "lucide-react";
import { fetchCreateProgress } from "../lib/runtime-client";
import type { OperationSnapshot } from "../lib/types";

/** A random id the daemon uses to report progress for one create or import. */
export function newProgressId(): string {
  const bytes = new Uint8Array(12);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
}

/** Follows a running create or import by polling the daemon's progress for it. */
export function useOperationProgress() {
  const [snapshot, setSnapshot] = useState<OperationSnapshot | null>(null);
  const timer = useRef<number | null>(null);

  const stop = useCallback(() => {
    if (timer.current !== null) window.clearInterval(timer.current);
    timer.current = null;
  }, []);

  const start = useCallback((id: string) => {
    stop();
    setSnapshot(null);
    const tick = async () => {
      try {
        setSnapshot(await fetchCreateProgress(id));
      } catch {
        // The operation has not registered yet, or the daemon is busy; try again.
      }
    };
    timer.current = window.setInterval(tick, 700);
    void tick();
  }, [stop]);

  useEffect(() => stop, [stop]);

  return { snapshot, start, stop };
}

function useElapsedSeconds() {
  const [seconds, setSeconds] = useState(0);
  useEffect(() => {
    const began = Date.now();
    const timer = window.setInterval(() => setSeconds(Math.floor((Date.now() - began) / 1000)), 1000);
    return () => window.clearInterval(timer);
  }, []);
  return seconds;
}

function formatElapsed(seconds: number) {
  if (seconds < 60) return `${seconds}s`;
  return `${Math.floor(seconds / 60)}m ${String(seconds % 60).padStart(2, "0")}s`;
}

/** What Cliff is doing, one line per step, with the running step described. */
export function OperationProgress({ title, snapshot, waitingLabel }: { title: string; snapshot: OperationSnapshot | null; waitingLabel: string }) {
  const elapsed = useElapsedSeconds();
  const steps = snapshot?.steps.length
    ? snapshot.steps
    : [{ id: "waiting", label: waitingLabel, state: "active" as const }];

  return (
    <div className="operation-progress" role="status" aria-live="polite">
      <div className="operation-progress-head">
        <strong>{title}</strong>
        <span className="muted">{formatElapsed(elapsed)}</span>
      </div>
      <ol className="operation-steps">
        {steps.map((step) => (
          <li key={step.id} className={step.state}>
            <span className="operation-step-icon" aria-hidden="true">
              {step.state === "failed" ? <AlertTriangle size={15} /> : step.state === "done" ? <Check size={15} /> : step.state === "active" ? <Loader2 size={15} className="spin" /> : <Circle size={13} />}
            </span>
            <span className="operation-step-text">
              <strong>{step.label}</strong>
              {step.state === "active" && snapshot?.detail && <small>{snapshot.detail}</small>}
            </span>
          </li>
        ))}
      </ol>
    </div>
  );
}
