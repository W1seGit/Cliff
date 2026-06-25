"use client";

import React from "react";

export interface CheckboxProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "type" | "onChange"> {
  label: React.ReactNode;
  compact?: boolean;
  onChange?: (checked: boolean) => void;
}

export function Checkbox({ label, compact, className = "", onChange, ...props }: CheckboxProps) {
  const labelClass = compact ? "check compact-check" : "check";

  const handleChange = (event: React.ChangeEvent<HTMLInputElement>) => {
    if (onChange) {
      onChange(event.target.checked);
    }
  };

  return (
    <label className={labelClass}>
      <input type="checkbox" onChange={handleChange} className={className || undefined} {...props} />
      {label}
    </label>
  );
}
