"use client";

import React from "react";

export interface TabItem {
  id: string;
  label: React.ReactNode;
  disabled?: boolean;
  extraClassName?: string;
}

export interface TabsProps {
  items: TabItem[];
  activeId: string;
  onChange: (id: string) => void;
  ariaLabel?: string;
  className?: string;
}

export function Tabs({ items, activeId, onChange, ariaLabel, className = "" }: TabsProps) {
  const classes = `tabs ${className}`.trim();
  return (
    <div className={classes} role="tablist" aria-label={ariaLabel}>
      {items.map((item) => {
        const itemClass = [
          item.id === activeId ? "active" : "",
          item.extraClassName ?? "",
        ].filter(Boolean).join(" ").trim();
        return (
          <button
            key={item.id}
            type="button"
            role="tab"
            aria-selected={item.id === activeId}
            disabled={item.disabled}
            className={itemClass || undefined}
            onClick={() => onChange(item.id)}
          >
            {item.label}
          </button>
        );
      })}
    </div>
  );
}
