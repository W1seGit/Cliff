"use client";

import { useEffect, useState } from "react";
import { fetchJvmPresets } from "../../lib/runtime-client";
import type { JvmPreset } from "../../lib/types";
import { Banner, Card, Disclosure, FieldGrid, Input, Select, SettingRow, SkeletonRows, ToggleRow } from "../../components/ui";
import type { ProfileDraft } from "./profile-sections";
import { restartAttemptsRange, restartWindowRange, wholeRangeError } from "./validation";

type SetProfile = (update: (current: ProfileDraft) => ProfileDraft) => void;

export function PerformanceCard({ profile, setProfile }: { profile: ProfileDraft; setProfile: SetProfile }) {
  const [presets, setPresets] = useState<JvmPreset[] | null>(null);
  const [error, setError] = useState("");
  const maxMemoryMb = profile.maxMemoryMb;

  useEffect(() => {
    let cancelled = false;
    const timer = window.setTimeout(() => {
      fetchJvmPresets(Number.isFinite(maxMemoryMb) && maxMemoryMb > 0 ? maxMemoryMb : 512)
        .then((data) => { if (!cancelled) { setPresets(data.presets ?? []); setError(""); } })
        .catch((err) => { if (!cancelled) setError(err instanceof Error ? err.message : "Could not load tuning presets"); });
    }, 250);
    return () => { cancelled = true; window.clearTimeout(timer); };
  }, [maxMemoryMb]);

  const selected = presets?.find((preset) => preset.id === profile.jvmPreset);
  const unknown = presets !== null && profile.jvmPreset !== "" && !selected;
  const selectId = "jvm-preset-select";

  return (
    <Card title="Performance" description="Optional Java settings that can make the server run smoother, especially with many players or mods.">
      {presets === null && !error ? (
        <SkeletonRows rows={2} label="Loading tuning presets" />
      ) : (
        <>
          {error && <Banner variant="warning">Could not load tuning presets: {error}</Banner>}
          <SettingRow label="Tuning preset" description="Applied on top of your memory settings the next time the server starts." htmlFor={selectId}>
            <Select
              id={selectId}
              value={profile.jvmPreset}
              onChange={(event) => setProfile((current) => ({ ...current, jvmPreset: event.target.value }))}
            >
              <option value="">Default (no extra tuning)</option>
              {(presets ?? []).filter((preset) => preset.id !== "").map((preset) => (
                <option key={preset.id} value={preset.id}>{preset.name}</option>
              ))}
              {(unknown || (presets === null && profile.jvmPreset !== "")) && <option value={profile.jvmPreset}>{profile.jvmPreset}{unknown ? " (not available)" : ""}</option>}
            </Select>
          </SettingRow>
          {selected && <p className="field-note">{selected.description}</p>}
          {unknown && <Banner variant="warning">This preset is not available any more. Pick another one or choose Default.</Banner>}
          {selected && selected.flags.length > 0 && (
            <div>
              <p className="field-note" id="jvm-flags-label">Flags this preset adds</p>
              <pre className="jvm-flags" aria-labelledby="jvm-flags-label" tabIndex={0}>{selected.flags.join(" ")}</pre>
            </div>
          )}
          <Disclosure title="What is this?">
            <p className="field-note">
              Minecraft runs on Java, and Java has many options for how it cleans up memory. The default works fine for small servers.
              Aikar&apos;s flags are a well-known set that keeps that cleanup short and steady, which helps avoid lag spikes on busier or modded servers.
              You can switch back to Default at any time. If the server fails to start after changing this, set it back to Default.
            </p>
          </Disclosure>
        </>
      )}
    </Card>
  );
}

export function CrashRestartCard({ profile, setProfile }: { profile: ProfileDraft; setProfile: SetProfile }) {
  const enabled = profile.restartPolicy === "on-crash";
  const attemptsError = wholeRangeError(profile.restartMaxAttempts, restartAttemptsRange.min, restartAttemptsRange.max);
  const windowError = wholeRangeError(profile.restartWindowMinutes, restartWindowRange.min, restartWindowRange.max);
  const attemptsText = Number.isFinite(profile.restartMaxAttempts) ? String(profile.restartMaxAttempts) : "the maximum number of";
  const windowText = Number.isFinite(profile.restartWindowMinutes) ? String(profile.restartWindowMinutes) : "the chosen number of";
  return (
    <Card title="If the server crashes" description="Bring the server back up on its own when it stops unexpectedly. Stopping it yourself never triggers a restart.">
      <ToggleRow
        label="Restart automatically after a crash"
        description="Cliff waits a few seconds, then starts the server again."
        checked={enabled}
        onChange={(checked) => setProfile((current) => ({ ...current, restartPolicy: checked ? "on-crash" : "off" }))}
      />
      {enabled && (
        <>
          <FieldGrid columns={2}>
            <Input
              label="Maximum restarts"
              type="number"
              inputMode="numeric"
              min={restartAttemptsRange.min}
              max={restartAttemptsRange.max}
              step={1}
              value={Number.isFinite(profile.restartMaxAttempts) ? profile.restartMaxAttempts : ""}
              onChange={(event) => setProfile((current) => ({ ...current, restartMaxAttempts: event.target.value === "" ? NaN : Number(event.target.value) }))}
              error={attemptsError}
            />
            <Input
              label="Within (minutes)"
              type="number"
              inputMode="numeric"
              min={restartWindowRange.min}
              max={restartWindowRange.max}
              step={1}
              value={Number.isFinite(profile.restartWindowMinutes) ? profile.restartWindowMinutes : ""}
              onChange={(event) => setProfile((current) => ({ ...current, restartWindowMinutes: event.target.value === "" ? NaN : Number(event.target.value) }))}
              error={windowError}
            />
          </FieldGrid>
          <p className="field-note">
            The wait doubles with each retry. If the server crashes {attemptsText} times within {windowText} minutes,
            Cliff gives up, leaves the server stopped and notifies your webhooks.
          </p>
        </>
      )}
    </Card>
  );
}
