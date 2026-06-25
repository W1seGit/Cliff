"use client";

import React from "react";

export interface SelectProps extends React.SelectHTMLAttributes<HTMLSelectElement> {
  label?: React.ReactNode;
}

export function Select({ label, className = "", children, ...props }: SelectProps) {
  const selectElement = (
    <select className={className || undefined} {...props}>
      {children}
    </select>
  );

  if (label) {
    return (
      <label>
        {label}
        {selectElement}
      </label>
    );
  }

  return selectElement;
}
