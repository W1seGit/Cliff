"use client";

import React from "react";

export interface StatusDotProps {
  tone?: "neutral" | "success" | "running" | "warning" | "danger" | "accent";
  pulse?: boolean;
  /** Accessible text. Omit when a visible label sits next to the dot. */
  label?: string;
  className?: string;
}

export function StatusDot({ tone = "neutral", pulse = false, label, className = "" }: StatusDotProps) {
  const classes = ["status-dot", `tone-${tone}`, pulse ? "pulse" : "", className].filter(Boolean).join(" ");
  return label ? <span className={classes} role="img" aria-label={label} /> : <span className={classes} aria-hidden="true" />;
}
