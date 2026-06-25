"use client";

import type { ServerType } from "../lib/types";

type ServerTypePreset = { type: ServerType; label: string; logo: string; detail: string; recommended?: boolean };
type DisabledPreset = { label: string; logo: string; detail: string; badge: string };

const pluginPresets: ServerTypePreset[] = [
  { type: "paper", label: "Paper", logo: "/assets/logos/papermc.svg", detail: "High-performance plugin server", recommended: true },
  { type: "purpur", label: "Purpur", logo: "/assets/logos/purpur.svg", detail: "Paper fork with extra customization" },
  { type: "folia", label: "Folia", logo: "/assets/logos/folia.png", detail: "Regionized multithreaded Paper fork" },
];

const moddedPresets: ServerTypePreset[] = [
  { type: "fabric", label: "Fabric", logo: "/assets/logos/fabric.png", detail: "Lightweight mod loader", recommended: true },
  { type: "forge", label: "Forge", logo: "/assets/logos/forge.svg", detail: "Classic mod loader" },
  { type: "neoforge", label: "NeoForge", logo: "/assets/logos/neoforge.png", detail: "Modern Forge fork" },
];

const vanillaPresets: ServerTypePreset[] = [
  { type: "vanilla", label: "Vanilla", logo: "/assets/logos/vanilla.png", detail: "Mojang server jar" },
];

const comingLaterPresets: DisabledPreset[] = [
  { label: "Spigot", logo: "/assets/logos/spigot.png", detail: "Requires BuildTools compilation", badge: "Coming later" },
];

function PresetButton({ preset, active, onClick }: { preset: ServerTypePreset; active: boolean; onClick: () => void }) {
  return (
    <button type="button" className={active ? "active" : ""} onClick={onClick}>
      <span className={`type-logo ${preset.type}`}>
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img src={preset.logo} alt="" loading="lazy" />
      </span>
      <span>
        <strong>{preset.label}{preset.recommended && <span className="preset-badge recommended">Recommended</span>}</strong>
        <small>{preset.detail}</small>
      </span>
    </button>
  );
}

function DisabledPresetButton({ preset }: { preset: DisabledPreset }) {
  return (
    <button type="button" disabled className="preset-disabled">
      <span className="type-logo">
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img src={preset.logo} alt="" loading="lazy" />
      </span>
      <span>
        <strong>{preset.label}</strong>
        <small>{preset.detail}</small>
      </span>
      <span className="preset-badge">{preset.badge}</span>
    </button>
  );
}

export function ServerTypePresets({ value, onChange }: { value: ServerType; onChange: (type: ServerType) => void }) {
  return (
    <div className="server-type-presets">
      <div className="preset-section">
        <h4 className="preset-section-label">Vanilla</h4>
        {vanillaPresets.map((preset) => (
          <PresetButton key={preset.type} preset={preset} active={value === preset.type} onClick={() => onChange(preset.type)} />
        ))}
      </div>
      <div className="preset-section">
        <h4 className="preset-section-label">Plugin servers</h4>
        {pluginPresets.map((preset) => (
          <PresetButton key={preset.type} preset={preset} active={value === preset.type} onClick={() => onChange(preset.type)} />
        ))}
      </div>
      <div className="preset-section">
        <h4 className="preset-section-label">Modded servers</h4>
        {moddedPresets.map((preset) => (
          <PresetButton key={preset.type} preset={preset} active={value === preset.type} onClick={() => onChange(preset.type)} />
        ))}
      </div>
      <div className="preset-section">
        <h4 className="preset-section-label">Coming later</h4>
        {comingLaterPresets.map((preset) => (
          <DisabledPresetButton key={preset.label} preset={preset} />
        ))}
      </div>
    </div>
  );
}
