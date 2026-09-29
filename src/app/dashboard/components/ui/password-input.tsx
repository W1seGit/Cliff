"use client";

import React, { useState } from "react";
import { Eye, EyeOff } from "lucide-react";
import { Input, type InputProps } from "./input";
import { IconButton } from "./icon-button";

export type PasswordInputProps = Omit<InputProps, "type" | "suffix">;

/** Password field with a show/hide toggle. */
export const PasswordInput = React.forwardRef<HTMLInputElement, PasswordInputProps>(
  (props, ref) => {
    const [visible, setVisible] = useState(false);
    return (
      <Input
        ref={ref}
        {...props}
        type={visible ? "text" : "password"}
        suffix={
          <IconButton
            className="input-suffix-button"
            size="sm"
            aria-label={visible ? "Hide password" : "Show password"}
            aria-pressed={visible}
            onClick={() => setVisible((value) => !value)}
          >
            {visible ? <EyeOff size={15} /> : <Eye size={15} />}
          </IconButton>
        }
      />
    );
  }
);

PasswordInput.displayName = "PasswordInput";
