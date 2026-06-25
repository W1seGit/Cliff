"use client";

import React from "react";

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: "primary" | "danger" | "link" | "default";
  loading?: boolean;
  loadingText?: string;
  href?: string;
}

export function Button({
  variant,
  loading,
  loadingText,
  href,
  className = "",
  disabled,
  children,
  ...props
}: ButtonProps & React.AnchorHTMLAttributes<HTMLAnchorElement>) {
  const getClassName = () => {
    let base = className;
    if (variant === "primary") {
      base = `primary ${base}`;
    } else if (variant === "danger") {
      base = `danger-button ${base}`;
    } else if (variant === "link") {
      base = `button-link ${base}`;
    }
    return base.trim();
  };

  const isBtnDisabled = disabled || loading;

  if (href) {
    if (isBtnDisabled) {
      return (
        <span className={`button-link disabled ${className}`.trim()}>
          {children}
        </span>
      );
    }
    return (
      <a
        href={href}
        className={getClassName()}
        {...(props as React.AnchorHTMLAttributes<HTMLAnchorElement>)}
      >
        {children}
      </a>
    );
  }

  return (
    <button
      disabled={isBtnDisabled}
      className={getClassName()}
      {...(props as React.ButtonHTMLAttributes<HTMLButtonElement>)}
    >
      {loading ? loadingText || "Working..." : children}
    </button>
  );
}
