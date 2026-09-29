"use client";

import React from "react";

export interface PageHeaderProps {
  title: React.ReactNode;
  description?: React.ReactNode;
  icon?: React.ReactNode;
  /** Buttons or pills aligned to the right of the title. */
  actions?: React.ReactNode;
  /** Small link above the title, e.g. a Back button. */
  back?: React.ReactNode;
  className?: string;
}

/** Page title block. One h1 per screen. */
export function PageHeader({ title, description, icon, actions, back, className = "" }: PageHeaderProps) {
  return (
    <header className={`page-header ${className}`.trim()}>
      {back && <div className="page-header-back">{back}</div>}
      <div className="page-header-row">
        <div className="page-header-copy">
          <h1 className="page-header-title">
            {icon && <span className="page-header-icon" aria-hidden="true">{icon}</span>}
            {title}
          </h1>
          {description && <p className="page-header-description">{description}</p>}
        </div>
        {actions && <div className="page-header-actions">{actions}</div>}
      </div>
    </header>
  );
}
