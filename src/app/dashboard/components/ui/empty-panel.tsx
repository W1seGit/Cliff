"use client";

import React from "react";
import { Panel } from "./panel";
import { Button } from "./button";

export interface EmptyPanelProps {
  title: string;
  description?: string;
  action: string;
  onAction: () => void;
}

export function EmptyPanel({ title, description = "No server selected.", action, onAction }: EmptyPanelProps) {
  return (
    <Panel className="empty-panel">
      <h2>{title}</h2>
      <p className="muted">{description}</p>
      <Button variant="primary" onClick={onAction}>
        {action}
      </Button>
    </Panel>
  );
}
