"use client";

import React from "react";
import { Tabs, TabPanel } from "./tabs";
import { StatusDot } from "./status-dot";

export interface SettingsNavItem {
  id: string;
  label: string;
  icon?: React.ReactNode;
  /** Show a dot when this section has unsaved edits. */
  dirty?: boolean;
}

export interface SettingsLayoutProps {
  ariaLabel: string;
  items: SettingsNavItem[];
  activeId: string;
  onChange: (id: string) => void;
  /** Unique per page so tab and panel ids do not collide. */
  idPrefix: string;
  children: React.ReactNode;
}

/**
 * Settings shell: underline tabs (the same style the wizards use) above the
 * active section. Tabs can carry an icon and an unsaved-changes dot.
 */
export function SettingsLayout({ ariaLabel, items, activeId, onChange, idPrefix, children }: SettingsLayoutProps) {
  return (
    <div className="settings-shell">
      <Tabs
        className="settings-tabs"
        ariaLabel={ariaLabel}
        idPrefix={idPrefix}
        activeId={activeId}
        onChange={onChange}
        items={items.map((item) => ({
          id: item.id,
          label: (
            <>
              {item.icon}
              <span className="settings-nav-label">{item.label}</span>
              {item.dirty && <StatusDot tone="accent" label="Unsaved changes" />}
            </>
          ),
        }))}
      />
      <div className="settings-content">{children}</div>
    </div>
  );
}

export interface SettingsSectionPanelProps {
  idPrefix: string;
  id: string;
  activeId: string;
  children: React.ReactNode;
}

/** Content for one rail entry. Only the active one renders. */
export function SettingsSectionPanel({ idPrefix, id, activeId, children }: SettingsSectionPanelProps) {
  return (
    <TabPanel idPrefix={idPrefix} id={id} activeId={activeId} className="settings-section-panel">
      {children}
    </TabPanel>
  );
}
