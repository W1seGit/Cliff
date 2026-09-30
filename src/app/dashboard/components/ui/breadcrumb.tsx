"use client";

import React from "react";
import { ChevronRight } from "lucide-react";

export interface BreadcrumbItem {
  label: string;
  /** Omit on the current (last) item. */
  onClick?: () => void;
}

/** Path trail. Every item but the last is a button that navigates there. */
export function Breadcrumb({ items, disabled }: { items: BreadcrumbItem[]; disabled?: boolean }) {
  return (
    <nav className="breadcrumb" aria-label="Folder path">
      <ol>
        {items.map((item, index) => {
          const last = index === items.length - 1;
          return (
            <li key={`${index}:${item.label}`}>
              {last || !item.onClick ? (
                <span className="breadcrumb-current" aria-current={last ? "page" : undefined}>{item.label}</span>
              ) : (
                <button type="button" className="breadcrumb-link" disabled={disabled} onClick={item.onClick}>
                  {item.label}
                </button>
              )}
              {!last && <ChevronRight size={14} className="breadcrumb-sep" aria-hidden="true" />}
            </li>
          );
        })}
      </ol>
    </nav>
  );
}
