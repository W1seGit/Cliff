"use client";

import React from "react";
import { Button } from "./button";

export interface ModalProps {
  isOpen: boolean;
  onClose: () => void;
  title: string;
  description?: string | React.ReactNode;
  children?: React.ReactNode;
  confirmLabel?: string;
  confirmVariant?: "primary" | "danger" | "default";
  confirmDisabled?: boolean;
  confirmLoading?: boolean;
  cancelLabel?: string;
  onConfirm?: () => void;
  onCancel?: () => void;
  busy?: boolean;
  form?: boolean; // If true, wraps children in <div className="modal-form">
  rolePresentationClick?: boolean;
}

export function Modal({
  isOpen,
  onClose,
  title,
  description,
  children,
  confirmLabel,
  confirmVariant = "primary",
  confirmDisabled,
  confirmLoading,
  cancelLabel = "Cancel",
  onConfirm,
  onCancel,
  busy,
  form = true,
  rolePresentationClick = true,
}: ModalProps) {
  if (!isOpen) return null;

  const handleBackdropMouseDown = (event: React.MouseEvent) => {
    if (rolePresentationClick && event.target === event.currentTarget && !busy) {
      if (onCancel) onCancel();
      else onClose();
    }
  };

  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={handleBackdropMouseDown}>
      <section className="modal" role="dialog" aria-modal="true" aria-labelledby="modal-title">
        <h2 id="modal-title">{title}</h2>
        {description && (
          typeof description === "string" ? (
            <p>{description}</p>
          ) : (
            <div className="modal-message">{description}</div>
          )
        )}
        {children && (
          form ? <div className="modal-form">{children}</div> : children
        )}
        <div className="modal-actions">
          <Button disabled={busy} onClick={onCancel || onClose}>
            {cancelLabel}
          </Button>
          {onConfirm && (
            <Button
              variant={confirmVariant}
              disabled={confirmDisabled || busy}
              onClick={onConfirm}
              loading={confirmLoading}
              loadingText={busy ? "Working..." : undefined}
            >
              {confirmLabel}
            </Button>
          )}
        </div>
      </section>
    </div>
  );
}
