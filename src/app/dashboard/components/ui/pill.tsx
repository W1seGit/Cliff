"use client";

import React from "react";

export interface PillProps extends React.HTMLAttributes<HTMLSpanElement> {
  variant?: "default" | "success" | "warning" | "danger" | "accent";
}

const variantClass: Record<NonNullable<PillProps["variant"]>, string> = {
  default: "",
  success: "success",
  warning: "warning",
  danger: "danger",
  accent: "accent",
};

export function Pill({ variant = "default", className = "", children, ...props }: PillProps) {
  const classes = `pill ${variantClass[variant]} ${className}`.trim();
  return (
    <span className={classes} {...props}>
      {children}
    </span>
  );
}
