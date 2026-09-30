"use client";

import { useCallback, useRef, useState } from "react";
import { AlertTriangle, Check, Circle, Loader2 } from "lucide-react";
import { applyUpdate, fetchUpdateProgress, reloadAfterDaemonRestart } from "../lib/runtime-client";
import type { UpdateProgress } from "../lib/types";

type Stage = Exclude<UpdateProgress["stage"], "" | "failed">;

/** The steps of an update in order, worded for the person watching. */
const steps: { id: Stage; label: string }[] = [
  { id: "downloading", label: "Download the update" },
  { id: "verifying", label: "Check the download" },
  { id: "checking", label: "Test-start the new version" },
  { id: "backup", label: "Back up Cliff's settings" },
  { id: "stopping", label: "Stop your Minecraft server and save the world" },
  { id: "installing", label: "Install the new version" },
  { id: "restarting", label: "Restart and check the new version" },
];

const idleProgress: UpdateProgress = { active: false, stage: "", message: "" };

/**
 * Runs an update and follows its progress. The daemon reports each step while
 * it works; once it restarts, the page waits for it and reloads.
 */
export function useUpdateInstaller(onMessage: (message: string) => void) {
  const [installing, setInstalling] = useState(false);
  const [progress, setProgress] = useState<UpdateProgress>(idleProgress);
  const [error, setError] = useState("");
  const lastStage = useRef<Stage>("downloading");
  const [failedStage, setFailedStage] = useState<Stage | null>(null);

  const install = useCallback(async () => {
    setInstalling(true);
    setError("");
    setFailedStage(null);
    lastStage.current = "downloading";
    setProgress({ active: true, stage: "downloading", message: "Starting the update..." });

    let alive = true;
    const poll = window.setInterval(async () => {
      try {
        const next = await fetchUpdateProgress();
        // A "failed" step from an earlier attempt is not this update's; a real
        // failure arrives as the request's own error.
        if (alive && next.stage && next.stage !== "failed") {
          lastStage.current = next.stage;
          setProgress(next);
        }
      } catch {
        // The daemon is restarting; keep showing the last step.
      }
    }, 600);

    try {
      const result = await applyUpdate();
      if (result.success) {
        lastStage.current = "restarting";
        setProgress({
          active: true,
          stage: "restarting",
          message: "Restarting Cliff. It checks the new version and goes back to the old one by itself if it does not start.",
        });
        if (result.restarting) {
          await reloadAfterDaemonRestart();
        } else {
          window.location.reload();
        }
      } else {
        const message = result.message || "Update failed";
        setFailedStage(lastStage.current);
        setError(message);
        onMessage(message);
      }
    } catch (failure) {
      const message = failure instanceof Error ? failure.message : "Update failed";
      setFailedStage(lastStage.current);
      setError(message);
      onMessage(message);
    } finally {
      alive = false;
      window.clearInterval(poll);
      setInstalling(false);
    }
  }, [onMessage]);

  return { installing, progress, error, failedStage, install };
}

/** The step list shown while an update runs, or after it stopped. */
export function UpdateProgressList({
  progress,
  error,
  failedStage,
}: {
  progress: UpdateProgress;
  error: string;
  failedStage: Stage | null;
}) {
  const currentStage = (failedStage ?? (progress.stage === "failed" ? "downloading" : progress.stage)) as Stage | "";
  const currentIndex = steps.findIndex((step) => step.id === currentStage);

  return (
    <div className="update-progress" role="status" aria-live="polite">
      <ol className="update-steps">
        {steps.map((step, index) => {
          const failed = Boolean(error) && index === currentIndex;
          const done = !failed && currentIndex >= 0 && index < currentIndex;
          const active = !error && index === currentIndex;
          return (
            <li key={step.id} className={failed ? "failed" : done ? "done" : active ? "active" : "pending"}>
              <span className="update-step-icon" aria-hidden="true">
                {failed ? <AlertTriangle size={15} /> : done ? <Check size={15} /> : active ? <Loader2 size={15} className="spin" /> : <Circle size={13} />}
              </span>
              <span className="update-step-text">
                <strong>{step.label}</strong>
                {active && progress.message && <small>{progress.message}</small>}
              </span>
            </li>
          );
        })}
      </ol>
      {error && (
        <p className="update-progress-error">
          The update stopped: {error}
        </p>
      )}
    </div>
  );
}
