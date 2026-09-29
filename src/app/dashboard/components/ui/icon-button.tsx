"use client";

import React from "react";

export interface IconButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  /** Required: an icon-only control has no visible text for assistive tech. */
  "aria-label": string;
  variant?: "default" | "danger" | "primary";
  size?: "sm" | "md";
}

export const IconButton = React.forwardRef<HTMLButtonElement, IconButtonProps>(
  ({ variant = "default", size = "md", className = "", type = "button", children, ...props }, ref) => {
    const classes = [
      "icon-button",
      "icon-only",
      variant === "danger" ? "danger-button" : variant === "primary" ? "primary" : "",
      size === "sm" ? "icon-only-sm" : "",
      className,
    ]
      .filter(Boolean)
      .join(" ");
    return (
      <button ref={ref} type={type} className={classes} {...props}>
        {children}
      </button>
    );
  }
);

IconButton.displayName = "IconButton";
