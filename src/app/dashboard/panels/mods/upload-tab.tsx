"use client";

import { useRef, useState } from "react";
import { CircleCheck, MinusCircle, Upload, X } from "lucide-react";
import { formatBytes } from "../../lib/utils";
import { uploadServerMod } from "../../lib/runtime-client";
import type { UploadResult, WorldInfo } from "../../lib/types";
import { Banner, Button, IconButton, Pill, Select } from "../../components/ui";

const kindLabels: Record<string, string> = {
  mod: "Mod",
  plugin: "Plugin",
  datapack: "Datapack",
  bundle: "Bundle",
  world: "World",
  resourcepack: "Resource pack",
  jar: "Jar",
  unknown: "Unknown",
};

function isSupported(file: File) {
  const name = file.name.toLowerCase();
  return name.endsWith(".jar") || name.endsWith(".zip");
}

/**
 * Upload many .jar and .zip files at once. What each file is (mod, plugin,
 * datapack, or a zip of mods) is worked out from its contents on the server,
 * so the user never has to say.
 */
export function UploadTab({
  serverId,
  pluginProfile,
  worlds,
  selectedWorld,
  onSelectWorld,
  disabled,
  onUploaded,
  onMessage,
}: {
  serverId: string;
  pluginProfile: boolean;
  worlds: WorldInfo[];
  selectedWorld: string;
  onSelectWorld: (world: string) => void;
  disabled: boolean;
  /** Called after a successful upload so lists can refresh. */
  onUploaded: () => Promise<void> | void;
  onMessage: (message: string, kind?: "success" | "error" | "warning" | "info") => void;
}) {
  const [files, setFiles] = useState<File[]>([]);
  const [results, setResults] = useState<UploadResult[] | null>(null);
  const [uploading, setUploading] = useState(false);
  const [dragActive, setDragActive] = useState(false);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const busy = disabled || uploading;
  const hasZip = files.some((file) => file.name.toLowerCase().endsWith(".zip"));

  function addFiles(incoming: FileList | File[] | null) {
    if (!incoming) return;
    const list = Array.from(incoming);
    const accepted = list.filter(isSupported);
    const ignored = list.length - accepted.length;
    if (ignored > 0) onMessage(`Ignored ${ignored} file${ignored === 1 ? "" : "s"}: only .jar and .zip are supported`, "warning");
    setResults(null);
    setFiles((current) => {
      const seen = new Set(current.map((file) => `${file.name}:${file.size}`));
      return [...current, ...accepted.filter((file) => !seen.has(`${file.name}:${file.size}`))];
    });
  }

  async function upload() {
    if (files.length === 0 || busy) return;
    setUploading(true);
    try {
      const form = new FormData();
      // The server reads fields in order, so the action and world come first.
      form.set("action", "upload-auto");
      if (selectedWorld) form.set("worldName", selectedWorld);
      for (const file of files) form.append("file", file);
      const data = await uploadServerMod(serverId, form);
      const list = data.results ?? [];
      const added = list.filter((result) => result.status === "added").length;
      const skipped = list.length - added;
      setResults(list);
      setFiles([]);
      if (added > 0 && skipped === 0) onMessage(`Added ${added} file${added === 1 ? "" : "s"}`, "success");
      else if (added > 0) onMessage(`Added ${added}, skipped ${skipped}. See the details below.`, "warning");
      else onMessage("Nothing was added. See the details below.", "error");
      await onUploaded();
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Upload failed", "error");
    } finally {
      setUploading(false);
    }
  }

  return (
    <div className="discover-upload">
      <div className="discover-upload-head">
        <div>
          <h2>Upload</h2>
          <p className="muted">
            Drop {pluginProfile ? "plugins" : "mods"}, datapacks, or zips of them. Cliff checks what each file is and puts it in the right place.
          </p>
        </div>
        <Button variant="primary" iconLeft={<Upload size={16} />} disabled={files.length === 0 || busy} loading={uploading} loadingText="Uploading..." onClick={upload}>
          {files.length > 1 ? `Upload ${files.length} files` : "Upload"}
        </Button>
      </div>

      <div
        className={`mod-upload-drop ${dragActive ? "drag-active" : ""}`}
        role="button"
        tabIndex={0}
        aria-label="Choose files to upload"
        onDragOver={(event) => {
          event.preventDefault();
          if (!busy) setDragActive(true);
        }}
        onDragLeave={(event) => {
          event.preventDefault();
          setDragActive(false);
        }}
        onDrop={(event) => {
          event.preventDefault();
          setDragActive(false);
          if (!busy) addFiles(event.dataTransfer.files);
        }}
        onClick={() => {
          if (!busy) inputRef.current?.click();
        }}
        onKeyDown={(event) => {
          if ((event.key === "Enter" || event.key === " ") && !busy) {
            event.preventDefault();
            inputRef.current?.click();
          }
        }}
      >
        <input
          ref={inputRef}
          type="file"
          multiple
          accept=".jar,.zip"
          disabled={busy}
          onChange={(event) => {
            addFiles(event.target.files);
            event.target.value = "";
          }}
        />
        <Upload size={22} aria-hidden="true" />
        <strong>Drag and drop .jar or .zip files</strong>
        <span className="muted">or click to choose several at once</span>
      </div>

      {files.length > 0 && (
        <ul className="upload-list" aria-label="Files to upload">
          {files.map((file) => (
            <li key={`${file.name}:${file.size}`} className="upload-row">
              <span className="upload-row-name">{file.name}</span>
              <span className="muted">{formatBytes(file.size)}</span>
              <IconButton size="sm" aria-label={`Remove ${file.name}`} disabled={busy} onClick={() => setFiles((current) => current.filter((item) => item !== file))}>
                <X size={14} />
              </IconButton>
            </li>
          ))}
        </ul>
      )}

      {hasZip && worlds.length > 1 && (
        <label className="discover-filter-field">
          <span>If a zip is a datapack, add it to</span>
          <Select value={selectedWorld} onChange={(event) => onSelectWorld(event.target.value)} disabled={busy}>
            {worlds.map((world) => (
              <option key={world.name} value={world.name}>{world.name}</option>
            ))}
          </Select>
        </label>
      )}

      {results && (
        <div className="upload-results" role="status" aria-label="Upload results">
          {results.every((result) => result.status !== "added") && (
            <Banner variant="warning">None of these files could be added.</Banner>
          )}
          <ul className="upload-list">
            {results.map((result, index) => (
              <li key={`${result.name}:${index}`} className={`upload-row upload-result ${result.status}`}>
                {result.status === "added" ? <CircleCheck size={16} className="upload-icon ok" aria-label="Added" /> : <MinusCircle size={16} className="upload-icon skip" aria-label="Skipped" />}
                <span className="upload-row-copy">
                  <span className="upload-row-name">{result.name}</span>
                  {(result.message || result.source) && (
                    <small className="muted">{[result.source ? `from ${result.source}` : "", result.message].filter(Boolean).join(" · ")}</small>
                  )}
                </span>
                <Pill variant={result.status === "added" ? "success" : "default"}>{kindLabels[result.kind] ?? result.kind}</Pill>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
