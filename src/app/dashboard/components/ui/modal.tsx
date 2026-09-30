"use client";

import React from "react";
import * as Dialog from "@radix-ui/react-dialog";
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
  /** When false, clicking the backdrop and pressing Escape do not dismiss the dialog. */
  rolePresentationClick?: boolean;
}

/**
 * Accessible dialog: focus is trapped while open and returned to the trigger on
 * close, Escape dismisses, and the title/description are wired with aria ids.
 */
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
  const dismiss = () => {
    if (onCancel) onCancel();
    else onClose();
  };
  const canDismiss = rolePresentationClick && !busy;

  return (
    <Dialog.Root
      open={isOpen}
      onOpenChange={(open) => {
        if (!open && canDismiss) dismiss();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="modal-backdrop">
          <Dialog.Content
            className="modal"
            {...(description ? {} : { "aria-describedby": undefined })}
            onEscapeKeyDown={(event) => {
              if (!canDismiss) event.preventDefault();
            }}
            onPointerDownOutside={(event) => {
              if (!canDismiss) event.preventDefault();
            }}
            onInteractOutside={(event) => {
              // Toasts render outside the dialog; clicking one must not dismiss it.
              const target = event.target as HTMLElement | null;
              if (target?.closest("[role='status'], [data-toast]")) event.preventDefault();
            }}
          >
            <Dialog.Title asChild>
              <h2>{title}</h2>
            </Dialog.Title>
            {description && (
              <Dialog.Description asChild>
                {typeof description === "string" ? <p>{description}</p> : <div className="modal-message">{description}</div>}
              </Dialog.Description>
            )}
            {children && (form ? <div className="modal-form">{children}</div> : children)}
            <div className="modal-actions">
              <Button disabled={busy} onClick={dismiss}>
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
          </Dialog.Content>
        </Dialog.Overlay>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
