"use client";

import React from "react";

export interface PanelProps extends Omit<React.HTMLAttributes<HTMLElement>, "title"> {
  as?: "section" | "div";
  title?: React.ReactNode;
  description?: React.ReactNode;
  icon?: React.ReactNode;
  headerActions?: React.ReactNode;
}

export function Panel({
  as: Component = "section",
  className = "",
  title,
  description,
  icon,
  headerActions,
  children,
  ...props
}: PanelProps) {
  const panelElement = (
    <Component className={`panel ${className}`.trim()} {...props}>
      {children}
    </Component>
  );

  if (title) {
    return (
      <>
        <div className="workspace-page-header">
          <div className="workspace-page-heading">
            <h2>{icon && <span className="workspace-page-icon">{icon}</span>}{title}</h2>
            {description && <p>{description}</p>}
          </div>
          {headerActions}
        </div>
        {panelElement}
      </>
    );
  }

  return panelElement;
}
