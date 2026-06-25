"use client";

import React, { useState } from "react";
import { ChevronDown } from "lucide-react";
import { Button } from "./button";

export interface SelectionToggle {
  label: string;
  variant?: "primary" | "danger" | "default";
  onClick: () => void;
}

export interface SelectionAction {
  label: string;
  variant?: "primary" | "danger" | "default";
  disabled?: boolean;
  loading?: boolean;
  loadingText?: string;
  onClick: () => void;
  toggle?: SelectionToggle[];
}

export interface SelectionBarProps {
  selectedCount: number;
  actions: SelectionAction[];
  className?: string;
}

export function SelectionBar({ selectedCount, actions, className = "" }: SelectionBarProps) {
  if (selectedCount === 0 && actions.length === 0) return null;
  const classes = `selection-bar ${className}`.trim();
  return (
    <div className={classes}>
      <span className="selection-bar-count">{selectedCount} selected</span>
      <div className="selection-bar-actions">
        {actions.map((action, i) => (
          <SplitButton key={i} action={action} />
        ))}
      </div>
    </div>
  );
}

function SplitButton({ action }: { action: SelectionAction }) {
  const [toggleIdx, setToggleIdx] = useState(0);
  const toggles = action.toggle;

  if (!toggles || toggles.length === 0) {
    return (
      <Button
        variant={action.variant}
        disabled={action.disabled}
        loading={action.loading}
        loadingText={action.loadingText}
        onClick={action.onClick}
      >
        {action.label}
      </Button>
    );
  }

  const current = toggles[toggleIdx];
  const nextIdx = (toggleIdx + 1) % toggles.length;
  const isDanger = current.variant === "danger";

  return (
    <div className={`split-button ${isDanger ? "danger" : ""}`.trim()}>
      <Button
        disabled={action.disabled}
        loading={action.loading}
        loadingText={action.loadingText}
        onClick={current.onClick}
      >
        {current.label}
      </Button>
      <button
        type="button"
        className="split-button-arrow"
        disabled={action.disabled}
        onClick={() => setToggleIdx(nextIdx)}
        aria-label="Switch action"
      >
        <ChevronDown size={14} />
      </button>
    </div>
  );
}
