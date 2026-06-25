"use client";

import React from "react";

export interface ToolbarProps extends React.HTMLAttributes<HTMLDivElement> {
  spread?: boolean;
}

export function Toolbar({ spread = false, className = "", children, ...props }: ToolbarProps) {
  const classes = spread ? `toolbar spread ${className}` : `toolbar ${className}`;
  return (
    <div className={classes.trim()} {...props}>
      {children}
    </div>
  );
}
