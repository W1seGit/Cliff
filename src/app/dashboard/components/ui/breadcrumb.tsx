"use client";

import React, { useState } from "react";
import { ChevronRight } from "lucide-react";

export interface BreadcrumbItem {
  label: string;
  /** Omit on the current (last) item. */
  onClick?: () => void;
  /** Makes the item a drop target for dragged files. */
  onDrop?: () => void;
}

/** Path trail. Every item but the last is a button that navigates there. */
export function Breadcrumb({ items, disabled }: { items: BreadcrumbItem[]; disabled?: boolean }) {
  const [dropIndex, setDropIndex] = useState(-1);
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
                <button
                  type="button"
                  className={`breadcrumb-link${dropIndex === index ? " drop-active" : ""}`}
                  disabled={disabled}
                  onClick={item.onClick}
                  onDragOver={item.onDrop ? (event) => { event.preventDefault(); event.dataTransfer.dropEffect = "move"; setDropIndex(index); } : undefined}
                  onDragLeave={item.onDrop ? () => setDropIndex(-1) : undefined}
                  onDrop={item.onDrop ? (event) => { event.preventDefault(); setDropIndex(-1); item.onDrop?.(); } : undefined}
                >
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
