"use client";

import React, { useEffect, useRef } from "react";
import { ChevronDown } from "lucide-react";

export interface DisclosureProps {
  title: React.ReactNode;
  description?: React.ReactNode;
  defaultOpen?: boolean;
  /** Opens the group (and keeps it from hiding) while true, for example while a field needs fixing. */
  forceOpen?: boolean;
  className?: string;
  children: React.ReactNode;
}

/** Collapsible group built on native <details>, so it is keyboard and screen-reader accessible. */
export function Disclosure({ title, description, defaultOpen = false, forceOpen = false, className = "", children }: DisclosureProps) {
  const ref = useRef<HTMLDetailsElement | null>(null);
  useEffect(() => {
    if (forceOpen && ref.current) ref.current.open = true;
  }, [forceOpen]);
  return (
    <details ref={ref} className={`disclosure ${className}`.trim()} open={defaultOpen || undefined}>
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
