"use client";

import React from "react";

export interface HintProps extends React.HTMLAttributes<HTMLDivElement> {
  variant?: "form" | "source";
  warn?: boolean;
}

export function Hint({ variant = "form", warn = false, className = "", children, ...props }: HintProps) {
  const componentClass = variant === "source" ? "source-hint" : "form-hint";
  const warnClass = warn ? "warn" : "";
  const classes = `${componentClass} ${warnClass} ${className}`.trim();

  if (variant === "source") {
    return (
      <div className={classes} {...props}>
        {children}
      </div>
    );
  }

  return (
    <p className={classes} {...(props as React.HTMLAttributes<HTMLParagraphElement>)}>
      {children}
    </p>
  );
}
