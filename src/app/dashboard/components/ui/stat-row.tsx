"use client";

import React from "react";

export interface StatItem {
  label: React.ReactNode;
  value: React.ReactNode;
}

export interface StatRowProps {
  items: StatItem[];
  className?: string;
  variant?: "inline" | "stacked";
}

export function StatRow({ items, className = "", variant = "inline" }: StatRowProps) {
  if (items.length === 0) return null;
  const baseClass = variant === "stacked" ? "stat-list" : "stat-row";
  const classes = `${baseClass} ${className}`.trim();
  return (
    <div className={classes}>
      {items.map((item, index) => (
        <span key={index} className="stat-item">
          <span className="stat-label">{item.label}</span>
          <strong className="stat-value">{item.value}</strong>
        </span>
      ))}
    </div>
  );
}
