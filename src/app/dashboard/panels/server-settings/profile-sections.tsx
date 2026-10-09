"use client";

import { validMemoryRange } from "../../lib/utils";
import type { MinecraftMetadata, ServerType } from "../../lib/types";
import { Card, Disclosure, FieldGrid, Input } from "../../components/ui";
import { ExtraArgsPresetRow, JavaPresetRow, MemoryPresetRow } from "../../components/preset-rows";
import { VersionSelect } from "../../components/version-select";
import { LoaderSelect } from "../../components/loader-select";
import { CrashRestartCard, PerformanceCard } from "./performance-cards";

export type ProfileDraft = {
  name: string;
  type: ServerType;
  minecraftVersion: string;
  loaderVersion: string;
  javaPath: string;
  minMemoryMb: number;
  maxMemoryMb: number;
  launchJar: string;
  extraArgs: string;
  jvmPreset: string;
  restartPolicy: "off" | "on-crash";
  restartMaxAttempts: number;
  restartWindowMinutes: number;
};

type SetProfile = (update: (current: ProfileDraft) => ProfileDraft) => void;

export function ProfileGeneralCard({ profile, setProfile }: { profile: ProfileDraft; setProfile: SetProfile }) {
  return (
    <Card title="General" description="The name shown in the sidebar and dashboard.">
      <Input
        label="Name"
        value={profile.name}
        onChange={(event) => setProfile((current) => ({ ...current, name: event.target.value }))}
        error={profile.name.trim() ? "" : "A name is required."}
      />
    </Card>
  );
}

export function ProfileVersionCard({
  profile,
  setProfile,
  minecraftVersion,
  needsLoader,
  metadata,
  metadataError,
}: {
  profile: ProfileDraft;
  setProfile: SetProfile;
  minecraftVersion: string;
  needsLoader: boolean;
  metadata: MinecraftMetadata | null;
  metadataError: string;
}) {
  return (
    <Card title="Version" description="Changing the version updates the server jar the next time it starts.">
      <label>
        Minecraft
        <VersionSelect
          value={minecraftVersion}
          serverType={profile.type}
          metadata={metadata}
          metadataError={metadataError}
          onChange={(value) => setProfile((current) => ({ ...current, minecraftVersion: value, loaderVersion: "" }))}
        />
      </label>
      {needsLoader && (
        <Disclosure title="Loader version" description={`Advanced: pick a specific ${profile.type} loader.`}>
          <label>
            Loader
            <LoaderSelect
              type={profile.type}
              minecraftVersion={minecraftVersion}
              metadata={metadata}
              metadataError={metadataError}
              value={profile.loaderVersion}
              onChange={(value) => setProfile((current) => ({ ...current, loaderVersion: value }))}
            />
          </label>
        </Disclosure>
      )}
    </Card>
  );
}

export function RuntimeSections({ profile, setProfile }: { profile: ProfileDraft; setProfile: SetProfile }) {
  const memoryValid = validMemoryRange(profile.minMemoryMb, profile.maxMemoryMb);
  const memoryError = memoryValid ? "" : "Minimum is 512 MB, and max cannot be lower than min.";
  return (
    <>
      <Card title="Java" description="Auto-managed installs the Java version this profile needs during setup or first start.">
        <Input label="Java runtime" value={profile.javaPath} onChange={(event) => setProfile((current) => ({ ...current, javaPath: event.target.value }))} />
        <JavaPresetRow javaPath={profile.javaPath} onApply={(javaPath) => setProfile((current) => ({ ...current, javaPath }))} />
      </Card>
      <Card title="Memory" description="Heap size in megabytes.">
        <FieldGrid columns={2}>
          <Input
            label="Min memory"
            type="number"
            value={profile.minMemoryMb}
            onChange={(event) => setProfile((current) => ({ ...current, minMemoryMb: Number(event.target.value) }))}
          />
          <Input
            label="Max memory"
            type="number"
            value={profile.maxMemoryMb}
            onChange={(event) => setProfile((current) => ({ ...current, maxMemoryMb: Number(event.target.value) }))}
            error={memoryError}
          />
        </FieldGrid>
        <MemoryPresetRow
          minMemoryMb={profile.minMemoryMb}
          maxMemoryMb={profile.maxMemoryMb}
          onApply={(minMemoryMb, maxMemoryMb) => setProfile((current) => ({ ...current, minMemoryMb, maxMemoryMb }))}
        />
      </Card>
      <Card title="Launch" description="What Cliff runs and the arguments it passes.">
        <Input label="Launch target" value={profile.launchJar} onChange={(event) => setProfile((current) => ({ ...current, launchJar: event.target.value }))} />
        <Input label="Extra args" value={profile.extraArgs} onChange={(event) => setProfile((current) => ({ ...current, extraArgs: event.target.value }))} />
        <ExtraArgsPresetRow extraArgs={profile.extraArgs} onApply={(extraArgs) => setProfile((current) => ({ ...current, extraArgs }))} />
      </Card>
      <PerformanceCard profile={profile} setProfile={setProfile} />
      <CrashRestartCard profile={profile} setProfile={setProfile} />
    </>
  );
}
