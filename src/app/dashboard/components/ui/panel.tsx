"use client";

import React from "react";
import { PageHeader } from "./page-header";

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
        <PageHeader title={title} description={description} icon={icon} actions={headerActions} />
        {panelElement}
      </>
    );
  }

  return panelElement;
}
