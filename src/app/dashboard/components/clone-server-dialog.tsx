"use client";

import { useState } from "react";
import type { ServerRecord } from "../lib/types";
import { validPort } from "../lib/utils";
import { Modal } from "./ui/modal";
import { FieldGrid } from "./ui/field-grid";
import { Input } from "./ui/input";

function CloneBody({
  server,
  onSubmit,
  onClose,
}: {
  server: ServerRecord;
  onSubmit: (name: string, port: number) => void | Promise<void>;
  onClose: () => void;
}) {
  const [name, setName] = useState(`${server.name} Copy`);
  const [port, setPort] = useState(String(server.port + 1));
  const [busy, setBusy] = useState(false);

  const trimmed = name.trim();
  const portValue = Number(port);
  const nameError = trimmed ? "" : "A name is required.";
  const portError = validPort(portValue) ? "" : "Enter a port from 1 to 65535.";
  const canSubmit = !nameError && !portError && !busy;

  async function submit() {
    if (!canSubmit) return;
    setBusy(true);
    try {
      await onSubmit(trimmed, portValue);
      onClose();
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      isOpen
      onClose={onClose}
      title="Clone server"
      description={`Create a copy of ${server.name}. The clone gets its own files and port.`}
      confirmLabel="Clone"
      confirmDisabled={!canSubmit}
      onConfirm={submit}
      busy={busy}
    >
      <form
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <FieldGrid columns={1}>
          <Input label="Name" value={name} autoFocus onChange={(event) => setName(event.target.value)} error={nameError} />
          <Input label="Port" type="number" min={1} max={65535} value={port} onChange={(event) => setPort(event.target.value)} error={portError} />
        </FieldGrid>
      </form>
    </Modal>
  );
}

/** Two-field replacement for the old name and port window.prompt pair. */
export function CloneServerDialog({
  server,
  onSubmit,
  onClose,
}: {
  server: ServerRecord | null;
  onSubmit: (server: ServerRecord, name: string, port: number) => void | Promise<void>;
  onClose: () => void;
}) {
  if (!server) return null;
  return <CloneBody key={server.id} server={server} onSubmit={(name, port) => onSubmit(server, name, port)} onClose={onClose} />;
}
