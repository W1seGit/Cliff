"use client";

import React from "react";

export interface FieldGridProps extends React.HTMLAttributes<HTMLDivElement> {
  columns?: 1 | 2 | 3;
  gap?: "sm" | "md";
}

export function FieldGrid({ columns = 1, gap = "md", className = "", children, ...props }: FieldGridProps) {
  const classes = `field-grid cols-${columns} gap-${gap} ${className}`.trim();
  return (
    <div className={classes} {...props}>
      {children}
    </div>
  );
}

export interface SectionProps {
  title?: React.ReactNode;
  description?: React.ReactNode;
  collapsible?: boolean;
  defaultOpen?: boolean;
  className?: string;
  children?: React.ReactNode;
  actions?: React.ReactNode;
}

export function Section({ title, description, collapsible = false, defaultOpen = true, className = "", children, actions }: SectionProps) {
  const classes = `form-section ${className}`.trim();
  if (collapsible) {
    return (
      <details className={`advanced-section compact ${className}`.trim()} open={defaultOpen || undefined}>
        <summary>{title}</summary>
        {description && <p className="muted">{description}</p>}
        <div className={classes}>
          {children}
        </div>
      </details>
    );
  }
  return (
    <div className={classes}>
      {(title || description || actions) && (
        <div className="form-section-header">
          {title && <h3>{title}</h3>}
          {description && <p className="muted">{description}</p>}
          {actions}
        </div>
      )}
      {children}
    </div>
  );
}
