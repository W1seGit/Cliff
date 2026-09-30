"use client";

import React from "react";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";

export interface MenuProps {
  /** The element that opens the menu (a Button or IconButton). Must accept a ref. */
  trigger: React.ReactElement;
  align?: "start" | "center" | "end";
  side?: "top" | "right" | "bottom" | "left";
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  className?: string;
  children: React.ReactNode;
}

/**
 * Dropdown menu with arrow-key navigation, typeahead, Escape to close and
 * focus return. Replaces the hand-rolled `more-menu` popovers.
 */
export function Menu({ trigger, align = "end", side = "bottom", open, onOpenChange, className = "", children }: MenuProps) {
  return (
    <DropdownMenu.Root open={open} onOpenChange={onOpenChange}>
      <DropdownMenu.Trigger asChild>{trigger}</DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          className={`menu-content ${className}`.trim()}
          align={align}
          side={side}
          sideOffset={6}
          collisionPadding={8}
        >
          {children}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

export interface MenuItemProps {
  icon?: React.ReactNode;
  danger?: boolean;
  disabled?: boolean;
  onSelect?: () => void;
  children: React.ReactNode;
}

export function MenuItem({ icon, danger, disabled, onSelect, children }: MenuItemProps) {
  return (
    <DropdownMenu.Item
      className={danger ? "menu-item danger" : "menu-item"}
      disabled={disabled}
      onSelect={() => onSelect?.()}
    >
      {icon}
      {children}
    </DropdownMenu.Item>
  );
}

export function MenuLabel({ children }: { children: React.ReactNode }) {
  return <DropdownMenu.Label className="menu-label">{children}</DropdownMenu.Label>;
}

export function MenuSeparator() {
  return <DropdownMenu.Separator className="menu-separator" />;
}
