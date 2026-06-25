"use client";

const memoryPresets = [
  { label: "Light", min: 1024, max: 2048 },
  { label: "Standard", min: 2048, max: 4096 },
  { label: "Modded", min: 4096, max: 6144 },
  { label: "Heavy", min: 6144, max: 8192 },
];

const javaPresets = [
  { label: "Auto-managed", value: "auto" },
  { label: "PATH java", value: "java" },
  { label: "Windows javaw", value: "javaw" },
  { label: "Homebrew", value: "/opt/homebrew/bin/java" },
  { label: "Linux", value: "/usr/bin/java" },
];

const extraArgPresets = [
  { label: "No GUI", value: "nogui" },
  { label: "UTF-8", value: "-Dfile.encoding=UTF-8" },
  { label: "IPv4", value: "-Djava.net.preferIPv4Stack=true" },
  { label: "None", value: "" },
];

export function MemoryPresetRow({ minMemoryMb, maxMemoryMb, onApply }: { minMemoryMb: number; maxMemoryMb: number; onApply: (min: number, max: number) => void }) {
  return (
    <div className="preset-row">
      <span>Memory presets</span>
      <div>
        {memoryPresets.map((preset) => (
          <button
            key={preset.label}
            type="button"
            className={minMemoryMb === preset.min && maxMemoryMb === preset.max ? "active" : ""}
            onClick={() => onApply(preset.min, preset.max)}
          >
            {preset.label} {preset.max / 1024}G
          </button>
        ))}
      </div>
    </div>
  );
}

export function JavaPresetRow({ javaPath, onApply }: { javaPath: string; onApply: (javaPath: string) => void }) {
  return (
    <div className="preset-row">
      <span>Java presets</span>
      <div>
        {javaPresets.map((preset) => (
          <button
            key={preset.value}
            type="button"
            className={javaPath === preset.value ? "active" : ""}
            onClick={() => onApply(preset.value)}
          >
            {preset.label}
          </button>
        ))}
      </div>
    </div>
  );
}

export function ExtraArgsPresetRow({ extraArgs, onApply }: { extraArgs: string; onApply: (extraArgs: string) => void }) {
  return (
    <div className="preset-row">
      <span>Extra args presets</span>
      <div>
        {extraArgPresets.map((preset) => (
          <button
            key={preset.label}
            type="button"
            className={extraArgs === preset.value ? "active" : ""}
            onClick={() => onApply(preset.value)}
          >
            {preset.label}
          </button>
        ))}
      </div>
    </div>
  );
}
