"use client";

import React, { useRef } from "react";

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
  /** When set, tabs get ids and aria-controls that match <TabPanel idPrefix=... />. */
  idPrefix?: string;
}

/**
 * Tab strip with roving tabindex: only the active tab is in the tab order,
 * and Arrow/Home/End move between tabs (automatic activation).
 */
export function Tabs({ items, activeId, onChange, ariaLabel, className = "", idPrefix }: TabsProps) {
  const classes = `tabs ${className}`.trim();
  const listRef = useRef<HTMLDivElement>(null);

  function handleKeyDown(event: React.KeyboardEvent<HTMLDivElement>) {
    const enabled = items.filter((item) => !item.disabled);
    if (enabled.length === 0) return;
    const currentIndex = enabled.findIndex((item) => item.id === activeId);
    let next = -1;
    if (event.key === "ArrowRight") next = (currentIndex + 1) % enabled.length;
    else if (event.key === "ArrowLeft") next = (currentIndex - 1 + enabled.length) % enabled.length;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = enabled.length - 1;
    if (next < 0) return;
    event.preventDefault();
    const target = enabled[next];
    onChange(target.id);
    listRef.current?.querySelector<HTMLButtonElement>(`[data-tab-id="${CSS.escape(target.id)}"]`)?.focus();
  }

  return (
    <div className={classes} role="tablist" aria-label={ariaLabel} ref={listRef} onKeyDown={handleKeyDown}>
      {items.map((item) => {
        const selected = item.id === activeId;
        const itemClass = [selected ? "active" : "", item.extraClassName ?? ""].filter(Boolean).join(" ").trim();
        return (
          <button
            key={item.id}
            type="button"
            role="tab"
            id={idPrefix ? `${idPrefix}-tab-${item.id}` : undefined}
            aria-controls={idPrefix ? `${idPrefix}-panel-${item.id}` : undefined}
            aria-selected={selected}
            tabIndex={selected ? 0 : -1}
            data-tab-id={item.id}
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

export interface TabPanelProps {
  idPrefix: string;
  id: string;
  activeId: string;
  className?: string;
  children: React.ReactNode;
}

/** Panel paired with a Tabs entry. Renders only while its tab is active. */
export function TabPanel({ idPrefix, id, activeId, className, children }: TabPanelProps) {
  if (id !== activeId) return null;
  return (
    <div role="tabpanel" id={`${idPrefix}-panel-${id}`} aria-labelledby={`${idPrefix}-tab-${id}`} className={className} tabIndex={0}>
      {children}
    </div>
  );
}
