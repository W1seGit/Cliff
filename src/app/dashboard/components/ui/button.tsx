"use client";

import React from "react";

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: "primary" | "danger" | "danger-ghost" | "link" | "default";
  size?: "sm" | "md" | "lg";
  /** Stretch to the full width of the container. */
  block?: boolean;
  /** Icon rendered before the label. */
  iconLeft?: React.ReactNode;
  loading?: boolean;
  loadingText?: string;
  href?: string;
  /** Skip the standard button styling, for buttons that style themselves (rows, backdrops). */
  plain?: boolean;
}

export function Button({
  variant,
  size = "md",
  block,
  iconLeft,
  loading,
  loadingText,
  href,
  plain,
  className = "",
  disabled,
  children,
  ...props
}: ButtonProps & React.AnchorHTMLAttributes<HTMLAnchorElement>) {
  const getClassName = () => {
    const classes: string[] = [];
    if (!plain && variant !== "link") classes.push("btn");
    if (variant === "primary") classes.push("primary");
    else if (variant === "danger") classes.push("danger-button");
    else if (variant === "danger-ghost") classes.push("danger-ghost");
    else if (variant === "link") classes.push("button-link");
    if (size !== "md") classes.push(`btn-${size}`);
    if (block) classes.push("btn-block");
    if (className) classes.push(className);
    return classes.join(" ");
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
        {iconLeft}
        {children}
      </a>
    );
  }

  return (
    <button
      disabled={isBtnDisabled}
      className={getClassName() || undefined}
      {...(props as React.ButtonHTMLAttributes<HTMLButtonElement>)}
    >
      {loading ? (
        loadingText || "Working..."
      ) : (
        <>
          {iconLeft}
          {children}
        </>
      )}
    </button>
  );
}
