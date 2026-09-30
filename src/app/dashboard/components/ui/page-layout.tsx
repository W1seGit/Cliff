"use client";

import React from "react";
import { PageHeader } from "./page-header";

export interface PageProps {
  title?: React.ReactNode;
  description?: React.ReactNode;
  icon?: React.ReactNode;
  /** The page's single primary action, aligned right of the title. */
  actions?: React.ReactNode;
  /** A <Tabs> row (section switcher) shown under the header. */
  tabs?: React.ReactNode;
  /** Search, filters and secondary actions, in one row above the content. */
  toolbar?: React.ReactNode;
  className?: string;
  children: React.ReactNode;
}

/**
 * The anatomy every page shares:
 *
 *   header (title, description, one primary action)
 *   tabs (optional)
 *   toolbar (optional)
 *   content
 *
 * Width and padding come from the surrounding `.page-frame`, so a page never
 * sets its own.
 */
export function Page({ title, description, icon, actions, tabs, toolbar, className = "", children }: PageProps) {
  return (
    <section className={`page ${className}`.trim()}>
      {title && <PageHeader title={title} description={description} icon={icon} actions={actions} />}
      {tabs && <div className="page-tabs">{tabs}</div>}
      {toolbar && <div className="page-toolbar">{toolbar}</div>}
      <div className="page-body">{children}</div>
    </section>
  );
}
