"use client";

import React from "react";
import { ChevronDown } from "lucide-react";

export interface DisclosureProps {
  title: React.ReactNode;
  description?: React.ReactNode;
  defaultOpen?: boolean;
  className?: string;
  children: React.ReactNode;
}

/** Collapsible group built on native <details>, so it is keyboard and screen-reader accessible. */
export function Disclosure({ title, description, defaultOpen = false, className = "", children }: DisclosureProps) {
  return (
    <details className={`disclosure ${className}`.trim()} open={defaultOpen || undefined}>
      <summary>
        <span className="disclosure-copy">
          <span className="disclosure-title">{title}</span>
          {description && <span className="disclosure-description">{description}</span>}
        </span>
        <ChevronDown className="disclosure-chevron" size={16} aria-hidden="true" />
      </summary>
      <div className="disclosure-body">{children}</div>
    </details>
  );
}
