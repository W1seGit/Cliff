"use client";

import type { ServerType } from "../lib/types";

type ServerTypePreset = { type: ServerType; label: string; logo: string; detail: string; recommended?: boolean };
type DisabledPreset = { label: string; logo: string; detail: string };

/** Logo for each platform, shared by the server type picker and the marketplace filters. */
export const platformLogos: Record<string, string> = {
  vanilla: "/assets/logos/vanilla.png",
  paper: "/assets/logos/papermc.svg",
  purpur: "/assets/logos/purpur.svg",
  folia: "/assets/logos/folia.png",
  spigot: "/assets/logos/spigot.png",
  fabric: "/assets/logos/fabric.png",
  forge: "/assets/logos/forge.svg",
  neoforge: "/assets/logos/neoforge.png",
};

const pluginPresets: ServerTypePreset[] = [
  { type: "paper", label: "Paper", logo: platformLogos.paper, detail: "High-performance plugin server", recommended: true },
  { type: "purpur", label: "Purpur", logo: platformLogos.purpur, detail: "Paper fork with extra customization" },
  { type: "folia", label: "Folia", logo: platformLogos.folia, detail: "Regionized multithreaded Paper fork" },
];

const moddedPresets: ServerTypePreset[] = [
  { type: "fabric", label: "Fabric", logo: platformLogos.fabric, detail: "Lightweight mod loader", recommended: true },
  { type: "forge", label: "Forge", logo: platformLogos.forge, detail: "Classic mod loader" },
  { type: "neoforge", label: "NeoForge", logo: platformLogos.neoforge, detail: "Modern Forge fork" },
];

const vanillaPresets: ServerTypePreset[] = [
  { type: "vanilla", label: "Vanilla", logo: platformLogos.vanilla, detail: "Mojang server jar" },
];

const comingLaterPresets: DisabledPreset[] = [
  { label: "Spigot", logo: platformLogos.spigot, detail: "Requires BuildTools compilation" },
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
