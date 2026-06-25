"use client";

import { useEffect, useRef, useState } from "react";
import { FolderOpen, Upload } from "lucide-react";
import { serverTypeNeedsLoader, validMemoryRange, validPort } from "../lib/utils";
import { JavaPresetRow, ExtraArgsPresetRow, MemoryPresetRow } from "../components/preset-rows";
import { ServerTypePresets } from "../components/server-type-presets";
import { VersionSelect } from "../components/version-select";
import { LoaderSelect } from "../components/loader-select";
import { detectImportSource, importStagedServer } from "../lib/runtime-client";
import type { ImportDetection, MinecraftMetadata, ServerType, UnsavedChangesRegistration } from "../lib/types";
import { Input } from "../components/ui/input";
import { Panel } from "../components/ui/panel";
import { Hint } from "../components/ui/hint";
import { WizardTabs, WizardActions } from "../components/ui/wizard";
import { FieldGrid } from "../components/ui/field-grid";

type ImportSource = {
  kind: "zip" | "folder";
  files: File[];
  paths: string[];
  label: string;
};

function relativePathFor(file: File) {
  return (file as File & { webkitRelativePath?: string }).webkitRelativePath || file.name;
}

// --- Drag-and-drop helpers for traversing dropped directories ---

type FsEntry = FileSystemEntry & { createReader?: () => FileSystemDirectoryReader; file?: (cb: (f: File) => void, err: (e: unknown) => void) => void };

function readEntries(reader: FileSystemDirectoryReader): Promise<FileSystemEntry[]> {
  return new Promise((resolve, reject) => {
    reader.readEntries(resolve, reject);
  });
}

async function readAllEntries(reader: FileSystemDirectoryReader): Promise<FileSystemEntry[]> {
  const all: FileSystemEntry[] = [];
  let batch: FileSystemEntry[];
  do {
    batch = await readEntries(reader);
    all.push(...batch);
  } while (batch.length > 0);
  return all;
}

async function traverseEntry(entry: FsEntry, path: string): Promise<{ file: File; path: string }[]> {
  if (entry.isFile && entry.file) {
    const file = await new Promise<File>((resolve, reject) => entry.file!(resolve, reject));
    return [{ file, path: path + file.name }];
  }
  if (entry.isDirectory && entry.createReader) {
    const reader = entry.createReader();
    const children = await readAllEntries(reader);
    const results: { file: File; path: string }[] = [];
    for (const child of children) {
      const childResults = await traverseEntry(child as FsEntry, path + entry.name + "/");
      results.push(...childResults);
    }
    return results;
  }
  return [];
}

export function ImportPanel({
  metadata,
  metadataError,
  onImported,
  onMessage,
  onUnsavedChange,
}: {
  metadata: MinecraftMetadata | null;
  metadataError: string;
  onImported: (serverId?: string) => void;
  onMessage: (message: string) => void;
  onUnsavedChange: (change: UnsavedChangesRegistration | null) => void;
}) {
  const zipInputRef = useRef<HTMLInputElement | null>(null);
  const folderInputRef = useRef<HTMLInputElement | null>(null);
  const [step, setStep] = useState(0);
  const [source, setSource] = useState<ImportSource | null>(null);
  const [detection, setDetection] = useState<ImportDetection | null>(null);
  const [name, setName] = useState("");
  const [type, setType] = useState<ServerType>("fabric");
  const [minecraftVersion, setMinecraftVersion] = useState("");
  const [loaderVersion, setLoaderVersion] = useState("");
  const [minMemoryMb, setMinMemoryMb] = useState(2048);
  const [maxMemoryMb, setMaxMemoryMb] = useState(4096);
  const [port, setPort] = useState("");
  const [launchJar, setLaunchJar] = useState("");
  const [javaPath, setJavaPath] = useState("java");
  const [extraArgs, setExtraArgs] = useState("");
  const [busy, setBusy] = useState(false);
  const [dragActive, setDragActive] = useState(false);

  const importSteps = ["Source", "Detected", "Resources", "Review"];
  const effectiveMinecraftVersion = minecraftVersion || detection?.minecraftVersion || metadata?.latest.release || "";
  const effectivePort = port.trim() ? Number(port) : detection?.port || 25565;
  const needsLoader = serverTypeNeedsLoader(type);
  const memoryValid = validMemoryRange(minMemoryMb, maxMemoryMb);
  const portValid = validPort(effectivePort);
  const canImport = Boolean(detection?.token && name.trim() && effectiveMinecraftVersion && (!needsLoader || loaderVersion) && memoryValid && portValid && !busy);
  const importStepValid = [
    Boolean(detection),
    Boolean(detection && name.trim() && effectiveMinecraftVersion && (!needsLoader || loaderVersion)),
    memoryValid && portValid,
    canImport,
  ];
  const canContinue = step === 0
    ? Boolean(source && !busy)
    : step === 1
      ? importStepValid[1]
      : step === 2
        ? importStepValid[2]
        : importStepValid[3];
  const canVisitStep = (index: number) => {
    if (busy || index === 0) return !busy;
    if (index === 1) return Boolean(detection);
    return importStepValid.slice(0, index).every(Boolean);
  };
  const hasUnsavedChanges = Boolean(source || detection || step > 0);

  useEffect(() => {
    onUnsavedChange(hasUnsavedChanges ? {
      id: "import-server",
      label: "Import server",
      dirty: true,
      message: "This import has not been finished. Navigating away will reset the import process.",
      discardLabel: "Discard import",
    } : null);
    return () => onUnsavedChange(null);
  }, [hasUnsavedChanges, onUnsavedChange]);

  function chooseZip(file: File | null) {
    if (!file) return;
    const label = file.name.replace(/\.zip$/i, "") || "Imported server";
    setSource({ kind: "zip", files: [file], paths: [file.name], label });
    setDetection(null);
    setStep(0);
    if (!name.trim()) setName(label);
  }

  function chooseFolder(fileList: FileList | null) {
    const files = Array.from(fileList ?? []);
    if (files.length === 0) return;
    const paths = files.map(relativePathFor);
    const label = paths[0]?.split(/[\\/]/)[0] || files[0]?.name || "Imported server";
    setSource({ kind: "folder", files, paths, label });
    setDetection(null);
    setStep(0);
    if (!name.trim()) setName(label);
  }

  function chooseDroppedFiles(files: File[], paths: string[]) {
    if (files.length === 0) return;
    const isZip = files.length === 1 && files[0].name.toLowerCase().endsWith(".zip");
    if (isZip) {
      chooseZip(files[0]);
    } else {
      const label = paths[0]?.split(/[\\/]/)[0] || files[0]?.name || "Imported server";
      setSource({ kind: "folder", files, paths, label });
      setDetection(null);
      setStep(0);
      if (!name.trim()) setName(label);
    }
  }

  async function handleDrop(event: React.DragEvent) {
    event.preventDefault();
    event.stopPropagation();
    setDragActive(false);
    if (busy) return;
    const items = event.dataTransfer.items;
    if (items && items.length > 0 && typeof items[0].webkitGetAsEntry === "function") {
      const collected: { file: File; path: string }[] = [];
      for (let i = 0; i < items.length; i++) {
        const entry = items[i].webkitGetAsEntry() as FsEntry | null;
        if (!entry) continue;
        const results = await traverseEntry(entry, "");
        collected.push(...results);
      }
      if (collected.length > 0) {
        chooseDroppedFiles(collected.map((c) => c.file), collected.map((c) => c.path));
        return;
      }
    }
    const droppedFiles = Array.from(event.dataTransfer.files ?? []);
    if (droppedFiles.length > 0) {
      chooseDroppedFiles(droppedFiles, droppedFiles.map((f) => f.name));
    }
  }

  function applyDetection(nextDetection: ImportDetection) {
    setDetection(nextDetection);
    setName((current) => current.trim() || nextDetection.name);
    setType(nextDetection.type);
    setMinecraftVersion(nextDetection.minecraftVersion);
    setLoaderVersion(nextDetection.loaderVersion);
    setPort(String(nextDetection.port || ""));
    setLaunchJar(nextDetection.launchJar);
  }

  async function detectSource() {
    if (!source || busy) return;
    setBusy(true);
    try {
      const form = new FormData();
      form.set("mode", source.kind === "zip" ? "detect-zip" : "detect-folder");
      form.set("name", name.trim() || source.label);
      if (source.kind === "zip") {
        form.set("file", source.files[0]);
      } else {
        form.set("paths", JSON.stringify(source.paths));
        source.files.forEach((file, index) => form.append("files", file, source.paths[index] || file.name));
      }
      const data = await detectImportSource(form);
      applyDetection(data.detection);
      setStep(1);
      onMessage("Server detected");
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Detection failed");
    } finally {
      setBusy(false);
    }
  }

  async function importServer() {
    if (!canImport || !detection?.token) return;
    setBusy(true);
    try {
      const data = await importStagedServer({
        mode: "import-staged",
        token: detection.token,
        name,
        type,
        minecraftVersion: effectiveMinecraftVersion,
        loaderVersion: needsLoader ? loaderVersion : "",
        javaPath,
        extraArgs,
        minMemoryMb,
        maxMemoryMb,
        port: effectivePort,
        launchJar,
      });
      await onImported(data.server?.id);
      onUnsavedChange(null);
      onMessage("Server imported");
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Import failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Panel className="form-grid utility-wizard-panel wizard-panel">
      <h2>Import existing server</h2>
      <div className="wizard-header">
        <WizardTabs
          steps={importSteps}
          currentStep={step}
          canVisitStep={canVisitStep}
          stepValid={importStepValid}
          onStepChange={setStep}
          ariaLabel="Import server steps"
        />
        <WizardActions
          currentStep={step}
          totalSteps={importSteps.length}
          busy={busy}
          canContinue={step === 0 ? Boolean(source && !busy) : canContinue}
          canSubmit={canImport}
          submitLabel="Import server"
          submittingLabel="Importing..."
          onBack={() => setStep((current) => Math.max(0, current - 1))}
          onContinue={step === 0 ? detectSource : () => setStep((current) => Math.min(importSteps.length - 1, current + 1))}
          onSubmit={importServer}
        />
      </div>

      {step === 0 && (
        <div className="form-section">
          <h3>Choose source</h3>
          <p className="muted import-source-description">Choose a ZIP or folder. The dashboard copies it into managed storage first, then detects the Minecraft version, loader, mods, port, and launch target.</p>
          <div
            className={`mod-upload-drop import-upload-zone ${source ? "selected" : ""} ${dragActive ? "drag-active" : ""}`}
            onDragOver={(e) => { e.preventDefault(); e.stopPropagation(); if (!busy) setDragActive(true); }}
            onDragLeave={(e) => { e.preventDefault(); e.stopPropagation(); setDragActive(false); }}
            onDrop={handleDrop}
            onClick={(event) => {
              const target = event.target as HTMLElement;
              if (target.closest("button,input")) return;
              if (!source && !busy) zipInputRef.current?.click();
            }}
          >
            <Input ref={zipInputRef} type="file" accept=".zip,application/zip" disabled={busy} onChange={(event) => chooseZip(event.target.files?.[0] ?? null)} />
            <Input ref={folderInputRef} type="file" multiple disabled={busy} {...{ webkitdirectory: "", directory: "" }} onChange={(event) => chooseFolder(event.target.files)} />
            {source ? (
              <>
                {source.kind === "zip" ? <Upload size={28} /> : <FolderOpen size={28} />}
                <strong>{source.label}</strong>
                <span className="muted">{source.kind === "zip" ? "ZIP archive selected" : `${source.files.length} files selected`}</span>
                <button type="button" className="import-upload-clear" disabled={busy} onClick={(e) => { e.preventDefault(); e.stopPropagation(); setSource(null); setDetection(null); }}>Choose different source</button>
              </>
            ) : (
              <>
                <Upload size={28} />
                <strong>Drag and drop a ZIP or folder</strong>
                <span className="muted">or click to browse — .zip files or a server folder</span>
                <button type="button" className="import-upload-folder-link" disabled={busy} onClick={(e) => { e.preventDefault(); e.stopPropagation(); folderInputRef.current?.click(); }}>Browse folder instead</button>
              </>
            )}
          </div>
        </div>
      )}

      {step === 1 && detection && (
        <div className="form-section">
          <h3>Detected profile</h3>
          <Hint variant="source" warn>Please review these detected values and change anything that looks wrong before continuing.</Hint>
          <div className="import-detection">
            <span><strong>{detection.type}</strong></span>
            <span>MC {detection.minecraftVersion || "choose"}</span>
            <span>{detection.loaderVersion || "no loader"}</span>
            <span>{detection.mods} mods{detection.disabledMods ? `, ${detection.disabledMods} disabled` : ""}</span>
          </div>
          <Input label="Display name" value={name} onChange={(event) => setName(event.target.value)} />
          <ServerTypePresets value={type} onChange={(nextType) => { setType(nextType); setLoaderVersion(serverTypeNeedsLoader(nextType) ? loaderVersion : ""); }} />
          <label>Minecraft<VersionSelect value={effectiveMinecraftVersion} serverType={type} metadata={metadata} metadataError={metadataError} onChange={(value) => { setMinecraftVersion(value); setLoaderVersion(""); }} /></label>
          {needsLoader && <label>Loader<LoaderSelect type={type} minecraftVersion={effectiveMinecraftVersion} metadata={metadata} metadataError={metadataError} value={loaderVersion} onChange={setLoaderVersion} /></label>}
          <Input label="Launch target" value={launchJar} onChange={(event) => setLaunchJar(event.target.value)} />
        </div>
      )}

      {step === 2 && (
        <div className="form-section">
          <h3>Resources</h3>
          <FieldGrid columns={2}>
            <Input label="Min memory" type="number" value={minMemoryMb} onChange={(event) => setMinMemoryMb(Number(event.target.value))} />
            <Input label="Max memory" type="number" value={maxMemoryMb} onChange={(event) => setMaxMemoryMb(Number(event.target.value))} />
          </FieldGrid>
          <MemoryPresetRow minMemoryMb={minMemoryMb} maxMemoryMb={maxMemoryMb} onApply={(min, max) => { setMinMemoryMb(min); setMaxMemoryMb(max); }} />
          <Input label="Port" type="number" value={effectivePort} onChange={(event) => setPort(event.target.value)} />
          <details className="advanced-section">
            <summary>Advanced launch settings</summary>
            <Input label="Java command" value={javaPath} onChange={(event) => setJavaPath(event.target.value)} />
            <JavaPresetRow javaPath={javaPath} onApply={setJavaPath} />
            <Input label="Extra args" value={extraArgs} onChange={(event) => setExtraArgs(event.target.value)} />
            <ExtraArgsPresetRow extraArgs={extraArgs} onApply={setExtraArgs} />
          </details>
        </div>
      )}

      {step === 3 && detection && (
        <div className="form-section">
          <h3>Review</h3>
          <div className="wizard-review-grid">
            <div><span>Type</span><strong>{type}</strong></div>
            <div><span>Minecraft</span><strong>{effectiveMinecraftVersion}</strong></div>
            <div><span>Loader</span><strong>{loaderVersion || "None"}</strong></div>
            <div><span>Port</span><strong>{effectivePort}</strong></div>
            <div><span>Mods</span><strong>{detection.mods} enabled</strong></div>
            <div><span>Disabled mods</span><strong>{detection.disabledMods}</strong></div>
            <div><span>Memory</span><strong>{minMemoryMb}M / {maxMemoryMb}M</strong></div>
            <div><span>Launch target</span><strong>{launchJar || "Detected"}</strong></div>
          </div>
          <Hint warn={!canImport}>{canImport ? "Ready to import." : "Review the detected profile and resources before importing."}</Hint>
        </div>
      )}
    </Panel>
  );
}
