"use client";

import React from "react";

export interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {
  label?: React.ReactNode;
  filter?: boolean;
  suffix?: React.ReactNode;
}

export const Input = React.forwardRef<HTMLInputElement, InputProps>(
  ({ label, filter, suffix, className = "", ...props }, ref) => {
    const inputClass = filter ? `filter-input ${className}`.trim() : className;
    const inputElement = <input ref={ref} className={inputClass || undefined} {...props} />;

    if (suffix) {
      const wrapped = (
        <div className="input-with-suffix">
          {inputElement}
          {suffix}
        </div>
      );
      if (label) {
        return (
          <label>
            {label}
            {wrapped}
          </label>
        );
      }
      return wrapped;
    }

    if (label) {
      return (
        <label>
          {label}
          {inputElement}
        </label>
      );
    }

    return inputElement;
  }
);

Input.displayName = "Input";
