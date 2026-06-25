"use client";

import React from "react";

export interface EmptyStateProps {
  title?: React.ReactNode;
  description?: React.ReactNode;
  icon?: React.ReactNode;
  action?: React.ReactNode;
  className?: string;
}

export function EmptyState({ title, description, icon, action, className = "" }: EmptyStateProps) {
  const classes = `empty-state ${className}`.trim();
  return (
    <div className={classes}>
      {icon && <div className="empty-state-icon" aria-hidden="true">{icon}</div>}
      {title && <h3>{title}</h3>}
      {description && <p className="muted">{description}</p>}
      {action && <div className="empty-state-action">{action}</div>}
    </div>
  );
}
