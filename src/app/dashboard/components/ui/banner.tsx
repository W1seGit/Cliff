"use client";

import React from "react";
import { AlertTriangle, CircleAlert, Info } from "lucide-react";

export interface BannerProps {
  variant?: "info" | "warning" | "danger";
  title?: React.ReactNode;
  /** Right-aligned action, e.g. a button. */
  action?: React.ReactNode;
  className?: string;
  children?: React.ReactNode;
}

const icons = {
  info: Info,
  warning: AlertTriangle,
  danger: CircleAlert,
} as const;

/** Inline notice. Slimmer than the old amber Hint blocks. */
export function Banner({ variant = "info", title, action, className = "", children }: BannerProps) {
  const Icon = icons[variant];
  return (
    <div className={`banner banner-${variant} ${className}`.trim()} role={variant === "info" ? "note" : "alert"}>
      <Icon className="banner-icon" size={16} aria-hidden="true" />
      <div className="banner-copy">
        {title && <strong>{title}</strong>}
        {children && <span>{children}</span>}
      </div>
      {action && <div className="banner-action">{action}</div>}
    </div>
  );
}
