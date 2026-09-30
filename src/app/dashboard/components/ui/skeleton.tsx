"use client";

import React from "react";

export interface SkeletonProps {
  variant?: "line" | "block" | "dot" | "toggle" | "button" | "mod-icon" | "stat";
  width?: "wide" | "medium" | "short";
  className?: string;
  style?: React.CSSProperties;
}

/** Loading placeholder. Decorative, so hidden from assistive tech. */
export function Skeleton({ variant = "line", width, className = "", style }: SkeletonProps) {
  const classes = ["skeleton", `skeleton-${variant}`, width ?? "", className].filter(Boolean).join(" ");
  return <span className={classes} style={style} aria-hidden="true" />;
}

export interface SkeletonRowsProps {
  rows?: number;
  label?: string;
}

/** A stack of text-like skeleton lines with a polite loading announcement. */
export function SkeletonRows({ rows = 3, label = "Loading" }: SkeletonRowsProps) {
  const widths: Array<SkeletonProps["width"]> = ["wide", "medium", "short"];
  return (
    <div className="skeleton-rows" role="status" aria-live="polite" aria-label={label}>
      {Array.from({ length: rows }, (_, index) => (
        <Skeleton key={index} width={widths[index % widths.length]} />
      ))}
    </div>
  );
}
