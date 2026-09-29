"use client";

import React, { useState } from "react";
import { Check, Copy } from "lucide-react";
import { copyTextToClipboard } from "../../lib/clipboard";
import { IconButton } from "./icon-button";

export interface CopyButtonProps {
  text: string;
  /** Accessible name, e.g. "Copy data directory". */
  label: string;
  onError?: () => void;
}

/** Icon button that copies text and briefly shows a check mark. */
export function CopyButton({ text, label, onError }: CopyButtonProps) {
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await copyTextToClipboard(text);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      onError?.();
    }
  }

  return (
    <IconButton size="sm" aria-label={copied ? "Copied" : label} onClick={copy}>
      {copied ? <Check size={14} /> : <Copy size={14} />}
    </IconButton>
  );
}
