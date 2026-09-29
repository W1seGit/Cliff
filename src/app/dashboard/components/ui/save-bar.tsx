"use client";

import React from "react";
import { Button } from "./button";
import { StatusDot } from "./status-dot";

export interface SaveBarProps {
  dirty: boolean;
  canSave: boolean;
  saving?: boolean;
  saveLabel?: string;
  /** Explains why Save is disabled, shown in place of the default message. */
  disabledReason?: string;
  onSave: () => void;
  onDiscard?: () => void;
}

/** Floating unsaved-changes bar. Renders nothing while the form is clean. */
export function SaveBar({ dirty, canSave, saving = false, saveLabel = "Save changes", disabledReason, onSave, onDiscard }: SaveBarProps) {
  if (!dirty) return null;
  const blocked = !canSave && !saving;
  return (
    <div className="save-bar" role="region" aria-label="Unsaved changes">
      <div className="save-bar-inner">
        <span className="save-bar-status" role="status" aria-live="polite">
          <StatusDot tone={blocked ? "warning" : "accent"} />
          {blocked ? disabledReason || "Fix the highlighted fields to save" : "You have unsaved changes"}
        </span>
        <div className="save-bar-actions">
          {onDiscard && (
            <Button size="sm" onClick={onDiscard} disabled={saving}>
              Discard
            </Button>
          )}
          <Button
            size="sm"
            variant="primary"
            onClick={onSave}
            disabled={!canSave}
            loading={saving}
            loadingText="Saving..."
          >
            {saveLabel}
          </Button>
        </div>
      </div>
    </div>
  );
}
