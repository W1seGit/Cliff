"use client";

import React from "react";
import { ArrowUp, ArrowDown, ArrowUpDown } from "lucide-react";

export interface TableProps extends React.TableHTMLAttributes<HTMLTableElement> {
  wrapperClassName?: string;
}

export function Table({ wrapperClassName = "", className = "", children, ...props }: TableProps) {
  return (
    <div className={`table-wrap ${wrapperClassName}`.trim()}>
      <table className={className || undefined} {...props}>
        {children}
      </table>
    </div>
  );
}

export interface SortableThProps {
  label: React.ReactNode;
  sortKey: string;
  activeSort: string | null;
  sortDir: "asc" | "desc";
  onSort: (key: string) => void;
  className?: string;
}

export function SortableTh({ label, sortKey, activeSort, sortDir, onSort, className = "" }: SortableThProps) {
  const isActive = activeSort === sortKey;
  return (
    <th className={`sortable-th ${className}`.trim()}>
      <button type="button" className="sortable-th-button" onClick={() => onSort(sortKey)}>
        <span>{label}</span>
        {isActive ? (
          sortDir === "asc" ? <ArrowUp size={13} /> : <ArrowDown size={13} />
        ) : (
          <ArrowUpDown size={13} className="sortable-th-inactive" />
        )}
      </button>
    </th>
  );
}
