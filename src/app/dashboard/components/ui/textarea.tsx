"use client";

import React from "react";

export interface TextareaProps extends React.TextareaHTMLAttributes<HTMLTextAreaElement> {
  label?: React.ReactNode;
}

export function Textarea({ label, className = "", ...props }: TextareaProps) {
  const textareaElement = <textarea className={className || undefined} {...props} />;

  if (label) {
    return (
      <label>
        {label}
        {textareaElement}
      </label>
    );
  }

  return textareaElement;
}
