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
  /** Heading level. Defaults to h2 because the server header (or the page band) already renders the page h1. */
  as?: "h1" | "h2";
  className?: string;
}

/** Title block at the top of a page or panel. */
export function PageHeader({ title, description, icon, actions, back, as: Heading = "h2", className = "" }: PageHeaderProps) {
  return (
    <header className={`page-header ${className}`.trim()}>
      {back && <div className="page-header-back">{back}</div>}
      <div className="page-header-row">
        <div className="page-header-copy">
          <Heading className="page-header-title">
            {icon && <span className="page-header-icon" aria-hidden="true">{icon}</span>}
            {title}
          </Heading>
          {description && <p className="page-header-description">{description}</p>}
        </div>
        {actions && <div className="page-header-actions">{actions}</div>}
      </div>
    </header>
  );
}
