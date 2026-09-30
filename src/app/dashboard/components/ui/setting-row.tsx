"use client";

import React from "react";
import { Toggle } from "./toggle";

export interface SettingRowProps {
  label: React.ReactNode;
  description?: React.ReactNode;
  /** The control on the right: a Toggle, Select, button, etc. */
  children: React.ReactNode;
  className?: string;
  /** id of the control, so the label is a real <label>. */
  htmlFor?: string;
}

/** A labelled setting: text on the left, control on the right. Stacks on narrow screens. */
export function SettingRow({ label, description, children, className = "", htmlFor }: SettingRowProps) {
  const id = React.useId();
  return (
    <div className={`setting-row ${className}`.trim()}>
      <div className="setting-row-copy">
        {htmlFor ? (
          <label className="setting-row-label" htmlFor={htmlFor} id={`${id}-label`}>{label}</label>
        ) : (
          <span className="setting-row-label" id={`${id}-label`}>{label}</span>
        )}
        {description && <span className="setting-row-description" id={`${id}-desc`}>{description}</span>}
      </div>
      <div className="setting-row-control">{children}</div>
    </div>
  );
}

export interface ToggleRowProps {
  label: React.ReactNode;
  description?: React.ReactNode;
  checked: boolean;
  onChange: (checked: boolean) => void;
  disabled?: boolean;
  className?: string;
}

/** SettingRow with a switch that is named and described by the visible text. */
export function ToggleRow({ label, description, checked, onChange, disabled, className = "" }: ToggleRowProps) {
  const id = React.useId();
  return (
    <div className={`setting-row ${className}`.trim()}>
      <div className="setting-row-copy">
        <span className="setting-row-label" id={`${id}-label`}>{label}</span>
        {description && <span className="setting-row-description" id={`${id}-desc`}>{description}</span>}
      </div>
      <div className="setting-row-control">
        <Toggle
          checked={checked}
          onChange={onChange}
          disabled={disabled}
          aria-labelledby={`${id}-label`}
          aria-describedby={description ? `${id}-desc` : undefined}
        />
      </div>
    </div>
  );
}

export interface KeyValueItem {
  key?: string;
  label: React.ReactNode;
  value: React.ReactNode;
  /** Secondary text under the value, e.g. "supported versions". */
  note?: React.ReactNode;
  /** Render the value in the monospace face (paths, URLs, versions). */
  mono?: boolean;
}

export interface KeyValueListProps {
  items: KeyValueItem[];
  className?: string;
}

/** Read-only label/value rows. Replaces the settings-version-row / property-row variants. */
export function KeyValueList({ items, className = "" }: KeyValueListProps) {
  if (items.length === 0) return null;
  return (
    <dl className={`kv-list ${className}`.trim()}>
      {items.map((item, index) => (
        <div className="kv-row" key={item.key ?? index}>
          <dt>{item.label}</dt>
          <dd className={item.mono ? "mono" : undefined}>
            {item.value}
            {item.note && <small>{item.note}</small>}
          </dd>
        </div>
      ))}
    </dl>
  );
}
