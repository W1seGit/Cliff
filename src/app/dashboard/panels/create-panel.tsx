"use client";

import { useEffect, useState } from "react";
import { serverTypeNeedsLoader, validMemoryRange, validPort } from "../lib/utils";
import { createServerProfile } from "../lib/runtime-client";
import { ExtraArgsPresetRow, JavaPresetRow, MemoryPresetRow } from "../components/preset-rows";
import { ServerTypePresets } from "../components/server-type-presets";
import { VersionSelect } from "../components/version-select";
import { LoaderSelect } from "../components/loader-select";
import type { MinecraftMetadata, ServerType, UnsavedChangesRegistration } from "../lib/types";
import { Input } from "../components/ui/input";
import { Panel } from "../components/ui/panel";
import { Hint } from "../components/ui/hint";
import { WizardTabs, WizardActions } from "../components/ui/wizard";
import { FieldGrid } from "../components/ui/field-grid";

const defaultCreateNames: Record<ServerType, string> = {
  vanilla: "New Vanilla Server",
  paper: "New Paper Server",
  purpur: "New Purpur Server",
  folia: "New Folia Server",
  fabric: "New Fabric Server",
  forge: "New Forge Server",
  neoforge: "New NeoForge Server",
};

export function CreatePanel({
  metadata,
  metadataError,
  onCreated,
  onMessage,
  onUnsavedChange,
}: {
  metadata: MinecraftMetadata | null;
  metadataError: string;
  onCreated: (serverId?: string) => void;
  onMessage: (message: string) => void;
  onUnsavedChange: (change: UnsavedChangesRegistration | null) => void;
}) {
  const [name, setName] = useState(defaultCreateNames.fabric);
  const [type, setType] = useState<ServerType>("fabric");
  const [minecraftVersion, setMinecraftVersion] = useState("");
  const [loaderVersion, setLoaderVersion] = useState("");
  const [minMemoryMb, setMinMemoryMb] = useState(2048);
  const [maxMemoryMb, setMaxMemoryMb] = useState(4096);
  const [port, setPort] = useState(25565);
  const [javaPath, setJavaPath] = useState("auto");
  const [extraArgs, setExtraArgs] = useState("");
  const [busy, setBusy] = useState(false);
  const [step, setStep] = useState(0);

  const effectiveMinecraftVersion = minecraftVersion || metadata?.latest.release || "";
  const needsLoader = serverTypeNeedsLoader(type);
  const memoryValid = validMemoryRange(minMemoryMb, maxMemoryMb);
  const portValid = validPort(port);
  const canSubmit = Boolean(metadata && name.trim() && effectiveMinecraftVersion && (!needsLoader || loaderVersion) && memoryValid && portValid && !busy);
  const createSteps = ["Type", "Version", "Resources", "Review"];
  const createStepValid = [
    Boolean(name.trim()),
    Boolean(metadata && effectiveMinecraftVersion && (!needsLoader || loaderVersion)),
    memoryValid && portValid,
    canSubmit,
  ];
  const canContinue = step === 0
    ? createStepValid[0]
    : step === 1
      ? createStepValid[1]
      : step === 2
        ? createStepValid[2]
        : createStepValid[3];
  const canVisitStep = (index: number) => !busy && (index === 0 || createStepValid.slice(0, index).every(Boolean));
  const hasUnsavedChanges = step > 0 ||
    name !== defaultCreateNames.fabric ||
    type !== "fabric" ||
    minecraftVersion !== "" ||
    loaderVersion !== "" ||
    minMemoryMb !== 2048 ||
    maxMemoryMb !== 4096 ||
    port !== 25565 ||
    javaPath !== "auto" ||
    extraArgs !== "";

  useEffect(() => {
    onUnsavedChange(hasUnsavedChanges ? {
      id: "create-server",
      label: "Create server",
      dirty: true,
      message: "This server profile has not been created yet. Navigating away will reset the create process.",
      discardLabel: "Discard profile",
    } : null);
    return () => onUnsavedChange(null);
  }, [hasUnsavedChanges, onUnsavedChange]);

  async function submit() {
    if (!canSubmit) return;
    setBusy(true);
    try {
      const data = await createServerProfile({ mode: "create", name, type, minecraftVersion: effectiveMinecraftVersion, loaderVersion: needsLoader ? loaderVersion : "", minMemoryMb, maxMemoryMb, port, javaPath, extraArgs });
      await onCreated(data.server?.id);
      onUnsavedChange(null);
      onMessage(data.note ?? "Server created");
    } catch (error) { onMessage(error instanceof Error ? error.message : "Create failed"); }
    finally { setBusy(false); }
  }

  return (
    <Panel className="form-grid utility-wizard-panel wizard-panel">
      <h2>Create server profile</h2>
      <div className="wizard-header">
        <WizardTabs
          steps={createSteps}
          currentStep={step}
          canVisitStep={canVisitStep}
          stepValid={createStepValid}
          onStepChange={setStep}
          ariaLabel="Create server steps"
        />
        <WizardActions
          currentStep={step}
          totalSteps={createSteps.length}
          busy={busy}
          canContinue={canContinue}
          canSubmit={canSubmit}
          submitLabel="Create profile"
          submittingLabel="Creating..."
          onBack={() => setStep((current) => Math.max(0, current - 1))}
          onContinue={() => setStep((current) => Math.min(createSteps.length - 1, current + 1))}
          onSubmit={submit}
        />
      </div>

      {step === 0 && (
        <div className="form-section">
          <h3>Server type</h3>
          <Input label="Name" value={name} onChange={(event) => setName(event.target.value)} />
          <ServerTypePresets value={type} onChange={(nextType) => {
            setName((current) => (Object.values(defaultCreateNames).includes(current.trim()) ? defaultCreateNames[nextType] : current));
            setType(nextType);
            setLoaderVersion("");
          }} />
          {type === "folia" && (
            <Hint warn>
              Folia is advanced and experimental. It can improve performance for large servers with
              spread-out players, but not all Paper plugins are compatible. Most servers should use Paper instead.
            </Hint>
          )}
        </div>
      )}

      {step === 1 && (
        <div className="form-section">
          <h3>Version</h3>
          <label>Minecraft<VersionSelect value={effectiveMinecraftVersion} serverType={type} metadata={metadata} metadataError={metadataError} onChange={(value) => { setMinecraftVersion(value); setLoaderVersion(""); }} /></label>
          {needsLoader && <label>Loader<LoaderSelect type={type} minecraftVersion={effectiveMinecraftVersion} metadata={metadata} metadataError={metadataError} value={loaderVersion} onChange={setLoaderVersion} /></label>}
          {type !== "vanilla" && (
            <Hint>Only Minecraft versions supported by {type} are shown. Not all Minecraft versions have builds for every server type.</Hint>
          )}
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
          <Input label="Port" type="number" value={port} onChange={(event) => setPort(Number(event.target.value))} />
          <details className="advanced-section">
            <summary>Advanced launch settings</summary>
            <Input label="Java runtime" value={javaPath} onChange={(event) => setJavaPath(event.target.value)} />
            <JavaPresetRow javaPath={javaPath} onApply={setJavaPath} />
            <Hint>Auto-managed installs and uses the Java version required by this Minecraft profile on first start.</Hint>
            <Input label="Extra args" value={extraArgs} onChange={(event) => setExtraArgs(event.target.value)} />
            <ExtraArgsPresetRow extraArgs={extraArgs} onApply={setExtraArgs} />
          </details>
        </div>
      )}

      {step === 3 && (
        <div className="form-section">
          <h3>Review</h3>
          <div className="wizard-review-grid">
            <div><span>Type</span><strong>{type}</strong></div>
            <div><span>Minecraft</span><strong>{effectiveMinecraftVersion}</strong></div>
            <div><span>Loader</span><strong>{loaderVersion || "None"}</strong></div>
            <div><span>Port</span><strong>{port}</strong></div>
            <div><span>Memory</span><strong>{minMemoryMb}M / {maxMemoryMb}M</strong></div>
          </div>
          <Hint warn={!canSubmit}>{canSubmit ? "Profile valid." : "Complete the previous steps to create."}</Hint>
        </div>
      )}
    </Panel>
  );
}
