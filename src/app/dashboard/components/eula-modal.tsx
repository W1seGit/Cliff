"use client";

import { useEffect, useState } from "react";
import { Modal } from "./ui/modal";
import { ToggleRow } from "./ui/setting-row";
import { fetchServerProperties, saveServerProperties } from "../lib/runtime-client";

export function EulaModal({
  serverId,
  isOpen,
  onClose,
  onMessage,
  onSaved,
}: {
  serverId: string;
  isOpen: boolean;
  onClose: () => void;
  onMessage: (message: string) => void;
  onSaved?: () => void;
}) {
  const [accepted, setAccepted] = useState(false);
  const [busy, setBusy] = useState(false);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    if (!isOpen || loaded) return;
    let cancelled = false;
    fetchServerProperties(serverId)
      .then((data) => {
        if (cancelled) return;
        setAccepted(data.eulaAccepted);
        setLoaded(true);
      })
      .catch(() => {
        if (cancelled) return;
        setLoaded(true);
      });
    return () => { cancelled = true; };
  }, [isOpen, serverId, loaded]);

  function handleClose() {
    setLoaded(false);
    onClose();
  }

  async function handleDone() {
    setBusy(true);
    try {
      const data = await fetchServerProperties(serverId);
      await saveServerProperties(serverId, { editable: data.editable, raw: data.raw, eulaAccepted: accepted });
      onMessage(accepted ? "EULA accepted" : "EULA updated");
      setLoaded(false);
      onClose();
      onSaved?.();
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Failed to save EULA");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      isOpen={isOpen}
      onClose={handleClose}
      title="Accept Minecraft EULA"
      description="You must accept the Minecraft EULA before the server can start."
      confirmLabel="Done"
      confirmVariant="primary"
      confirmDisabled={busy || !loaded}
      confirmLoading={busy}
      onConfirm={handleDone}
      onCancel={handleClose}
      busy={busy}
      form
    >
      <ToggleRow label="Accept Minecraft EULA" description="Required before the server can start." checked={accepted} onChange={setAccepted} disabled={busy} />
    </Modal>
  );
}
