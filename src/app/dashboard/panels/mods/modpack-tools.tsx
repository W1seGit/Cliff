"use client";

import { useRef, useState } from "react";
import { Download, Upload } from "lucide-react";
import { importModpack, modpackExportUrl } from "../../lib/runtime-client";
import { Banner, Button, Card } from "../../components/ui";

/** Import a .mrpack into the server, or export the server's mods as one. */
export function ModpackTools({
  serverId, isRunning, disabled, onImported,
}: {
  serverId: string;
  isRunning: boolean;
  disabled: boolean;
  onImported: () => Promise<void> | void;
}) {
  const [importing, setImporting] = useState(false);
  const [dragActive, setDragActive] = useState(false);
  const [outcome, setOutcome] = useState<{ kind: "ok" | "error"; text: string; files?: string[] } | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const blocked = disabled || importing || isRunning;

  async function importFile(file: File | undefined) {
    if (!file || blocked) return;
    if (!file.name.toLowerCase().endsWith(".mrpack")) {
      setOutcome({ kind: "error", text: "That is not a .mrpack file." });
      return;
    }
    setImporting(true);
    setOutcome(null);
    try {
      const form = new FormData();
      form.set("file", file);
      const data = await importModpack(serverId, form);
      setOutcome({ kind: "ok", text: `Imported ${data.name || file.name}.`, files: data.files });
      await onImported();
    } catch (error) {
      setOutcome({ kind: "error", text: error instanceof Error ? error.message : "Could not import the modpack" });
    } finally {
      setImporting(false);
    }
  }

  return (
    <Card
      className="modpack-tools"
      title="Modpacks"
      description="Move a whole set of mods between servers as a single .mrpack file."
    >
      <div className="modpack-tools-grid">
        <div className="modpack-tools-section">
          <h3>Import a .mrpack</h3>
          <p className="muted">
            Cliff checks the pack&apos;s Minecraft version and loader against this server and refuses packs made for anything else.
            It takes a safety snapshot first. The server must be stopped.
          </p>
          <div
            className={`modpack-drop ${dragActive ? "drag-active" : ""} ${blocked ? "is-disabled" : ""}`}
            role="button"
            tabIndex={blocked ? -1 : 0}
            aria-disabled={blocked}
            aria-label="Choose a .mrpack file to import"
            onDragOver={(event) => { event.preventDefault(); if (!blocked) setDragActive(true); }}
            onDragLeave={() => setDragActive(false)}
            onDrop={(event) => {
              event.preventDefault();
              setDragActive(false);
              void importFile(event.dataTransfer.files[0]);
            }}
            onClick={() => { if (!blocked) inputRef.current?.click(); }}
            onKeyDown={(event) => {
              if ((event.key === "Enter" || event.key === " ") && !blocked) {
                event.preventDefault();
                inputRef.current?.click();
              }
            }}
          >
            <input
              ref={inputRef}
              type="file"
              accept=".mrpack"
              hidden
              onChange={(event) => {
                void importFile(event.target.files?.[0]);
                event.target.value = "";
              }}
            />
            <Upload size={20} aria-hidden="true" />
            <strong>{importing ? "Importing, this can take a minute..." : "Drop a .mrpack here or click to choose"}</strong>
          </div>
          {isRunning && <p className="muted">Stop the server to import a modpack.</p>}
          <div role="status" aria-live="polite">
            {importing && <p className="muted">Uploading and installing...</p>}
            {outcome?.kind === "ok" && (
              <Banner variant="info" title={outcome.text}>
                {outcome.files?.length ? `${outcome.files.length} file${outcome.files.length === 1 ? "" : "s"} added.` : undefined}
              </Banner>
            )}
          </div>
          {outcome?.kind === "error" && <Banner variant="danger" title="Import failed">{outcome.text}</Banner>}
        </div>

        <div className="modpack-tools-section">
          <h3>Export as .mrpack</h3>
          <p className="muted">
            Mods found on Modrinth are linked instead of copied. Other jars and the config folder are bundled inside the file.
          </p>
          <Button iconLeft={<Download size={14} aria-hidden="true" />} href={modpackExportUrl(serverId)} download>
            Download .mrpack
          </Button>
        </div>
      </div>
    </Card>
  );
}
