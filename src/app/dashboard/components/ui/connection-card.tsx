"use client";

import React from "react";

export interface ConnectionCardProps {
  label: React.ReactNode;
  value?: React.ReactNode;
  loading?: boolean;
  onClick?: () => void;
  className?: string;
  "aria-label"?: string;
}

export function ConnectionCard({
  label,
  value,
  loading = false,
  onClick,
  className = "",
  "aria-label": ariaLabel,
}: ConnectionCardProps) {
  const cardClass = `connection-card public-access-connection-card ${className}`.trim();

  if (loading) {
    return (
      <div className={cardClass} aria-hidden="true">
        <span className="skeleton skeleton-line short" />
        <strong className="skeleton skeleton-line wide" />
      </div>
    );
  }

  if (onClick) {
    return (
      <button
        className={cardClass}
        type="button"
        onClick={onClick}
        aria-label={ariaLabel}
      >
        <span>{label}</span>
        <strong>{value}</strong>
      </button>
    );
  }

  return (
    <div className={cardClass}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}
