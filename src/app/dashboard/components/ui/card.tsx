"use client";

import React from "react";

export interface CardProps extends Omit<React.HTMLAttributes<HTMLElement>, "title"> {
  as?: "section" | "div" | "article";
  title?: React.ReactNode;
  description?: React.ReactNode;
  /** Controls rendered at the right of the header (buttons, pills). */
  actions?: React.ReactNode;
  footer?: React.ReactNode;
  /** Remove body padding, e.g. for tables or the console. */
  flush?: boolean;
}

/** Hairline-bordered surface. The building block for grouped content. */
export function Card({
  as: Component = "section",
  title,
  description,
  actions,
  footer,
  flush = false,
  className = "",
  children,
  ...props
}: CardProps) {
  const titleId = React.useId();
  const hasHeader = Boolean(title || description || actions);
  return (
    <Component
      className={`card ${className}`.trim()}
      aria-labelledby={title ? titleId : undefined}
      {...props}
    >
      {hasHeader && (
        <header className="card-header">
          <div className="card-heading">
            {title && <h2 id={titleId} className="card-title">{title}</h2>}
            {description && <p className="card-description">{description}</p>}
          </div>
          {actions && <div className="card-actions">{actions}</div>}
        </header>
      )}
      <div className={flush ? "card-body flush" : "card-body"}>{children}</div>
      {footer && <footer className="card-footer">{footer}</footer>}
    </Component>
  );
}
