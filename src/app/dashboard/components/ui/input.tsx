"use client";

import React, { useId } from "react";

export interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {
  label?: React.ReactNode;
  filter?: boolean;
  suffix?: React.ReactNode;
  /** Validation message shown under the field. Also sets aria-invalid. */
  error?: string;
}

export const Input = React.forwardRef<HTMLInputElement, InputProps>(
  ({ label, filter, suffix, error, className = "", ...props }, ref) => {
    const errorId = useId();
    const inputClass = filter ? `filter-input ${className}`.trim() : className;
    const inputElement = (
      <input
        ref={ref}
        className={inputClass || undefined}
        {...props}
        aria-invalid={error ? true : props["aria-invalid"]}
        aria-describedby={error ? errorId : props["aria-describedby"]}
      />
    );
    const errorElement = error ? (
      <span id={errorId} className="field-error" role="alert">
        {error}
      </span>
    ) : null;

    const control = suffix ? (
      <div className="input-with-suffix">
        {inputElement}
        {suffix}
      </div>
    ) : (
      inputElement
    );

    if (label) {
      return (
        <label>
          {label}
          {control}
          {errorElement}
        </label>
      );
    }

    return errorElement ? (
      <>
        {control}
        {errorElement}
      </>
    ) : (
      control
    );
  }
);

Input.displayName = "Input";
