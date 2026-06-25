"use client";

import { useEffect, useRef, useState } from "react";
import { Settings, RotateCcw } from "lucide-react";
import { serverTypeNeedsLoader, validMemoryRange } from "../lib/utils";
import { fetchServerProperties, runFileAction, saveServerProperties, serverFileUrl, updateServerProfile, uploadServerFile } from "../lib/runtime-client";
import { ExtraArgsPresetRow, JavaPresetRow, MemoryPresetRow } from "../components/preset-rows";
import { VersionSelect } from "../components/version-select";
import { LoaderSelect } from "../components/loader-select";
import type { MinecraftMetadata, ServerProperties, ServerPropertiesEditable, ServerRecord, UnsavedChangesRegistration } from "../lib/types";
import { Button } from "../components/ui/button";
import { Panel } from "../components/ui/panel";
import { Input } from "../components/ui/input";
import { Select } from "../components/ui/select";
import { Toggle } from "../components/ui/toggle";
import { Hint } from "../components/ui/hint";
import { Tabs } from "../components/ui/tabs";
import { Toolbar } from "../components/ui/toolbar";
import { FieldGrid } from "../components/ui/field-grid";
import { ImageCropModal } from "../components/ui/image-crop-modal";
import { notifyServerIconUpdated } from "../components/server-avatar";

const editablePropertyMap = {
  motd: "motd",
  "level-name": "levelName",
  "level-seed": "levelSeed",
  gamemode: "gamemode",
  difficulty: "difficulty",
  "max-players": "maxPlayers",
  "server-port": "serverPort",
  "view-distance": "viewDistance",
  "simulation-distance": "simulationDistance",
  "online-mode": "onlineMode",
  "white-list": "whiteList",
  pvp: "pvp",
  "enable-command-block": "enableCommandBlock",
  "allow-flight": "allowFlight",
} as const satisfies Record<string, keyof ServerPropertiesEditable>;

function rawValueForEditableField(key: keyof ServerPropertiesEditable, value: ServerPropertiesEditable[keyof ServerPropertiesEditable]) {
  if (typeof value === "boolean") return String(value);
  return String(value ?? "");
}

function editableValueFromRaw(key: keyof ServerPropertiesEditable, value: string) {
  if (key === "maxPlayers" || key === "serverPort" || key === "viewDistance" || key === "simulationDistance") return Number(value);
  if (key === "onlineMode" || key === "whiteList" || key === "pvp" || key === "enableCommandBlock" || key === "allowFlight") return value.trim().toLowerCase() === "true";
  return value;
}

function sortedRecordJson(record: Record<string, unknown>) {
  return JSON.stringify(Object.fromEntries(Object.entries(record).toSorted(([a], [b]) => a.localeCompare(b))));
}

export function ServerSettingsPanel({
  server,
  metadata,
  metadataError,
  isRunning,
  onSaved,
  onMessage,
  onUnsavedChange,
}: {
  server: ServerRecord;
  metadata: MinecraftMetadata | null;
  metadataError: string;
  isRunning: boolean;
  onSaved: () => void;
  onMessage: (message: string) => void;
  onUnsavedChange: (change: UnsavedChangesRegistration | null) => void;
}) {
  const [properties, setProperties] = useState<ServerProperties | null>(null);
  const [draft, setDraft] = useState<ServerProperties["editable"] | null>(null);
  const [rawDraft, setRawDraft] = useState<ServerProperties["raw"]>({});
  const [eulaAccepted, setEulaAccepted] = useState(false);
  const [profileBusy, setProfileBusy] = useState(false);
  const [settingsBusy, setSettingsBusy] = useState(false);
  const [iconPreviewUrl, setIconPreviewUrl] = useState("");
  const [iconFallback, setIconFallback] = useState(false);
  const [iconVersion, setIconVersion] = useState(0);
  const [pendingIconFile, setPendingIconFile] = useState<File | null>(null);
  const [iconResetPending, setIconResetPending] = useState(false);
  const [cropFile, setCropFile] = useState<File | null>(null);
  const [activeSettingsTab, setActiveSettingsTab] = useState<"game" | "profile">(() => {
    if (typeof window === "undefined") return "game";
    const hash = window.location.hash.replace("#", "");
    return hash === "profile" ? "profile" : "game";
  });
  const [profile, setProfile] = useState({
    name: server.name,
    type: server.type,
    minecraftVersion: server.minecraftVersion,
    loaderVersion: server.loaderVersion,
    javaPath: server.javaPath,
    minMemoryMb: server.minMemoryMb,
    maxMemoryMb: server.maxMemoryMb,
    launchJar: server.launchJar,
    extraArgs: server.extraArgs,
  });

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setProfile({
        name: server.name, type: server.type, minecraftVersion: server.minecraftVersion, loaderVersion: server.loaderVersion,
        javaPath: server.javaPath, minMemoryMb: server.minMemoryMb, maxMemoryMb: server.maxMemoryMb, launchJar: server.launchJar, extraArgs: server.extraArgs,
      });
      setIconPreviewUrl("");
      setIconFallback(false);
      setIconVersion(Date.now());
      setPendingIconFile(null);
      setIconResetPending(false);
    }, 0);
    fetchServerProperties(server.id)
      .then((data) => { setProperties(data); setDraft(data.editable); setRawDraft(data.raw); setEulaAccepted(data.eulaAccepted); })
      .catch((error) => onMessage(error.message));
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [server.id]);

  useEffect(() => () => {
    if (iconPreviewUrl) URL.revokeObjectURL(iconPreviewUrl);
  }, [iconPreviewUrl]);

  // Sync tab from URL hash on back/forward navigation
  useEffect(() => {
    const onHashChange = () => {
      const hash = window.location.hash.replace("#", "");
      const next = hash === "profile" ? "profile" : "game";
      setActiveSettingsTab((prev) => prev !== next ? next : prev);
    };
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  const profileMinecraftVersion = profile.minecraftVersion || metadata?.latest.release || "";
  const profileNeedsLoader = serverTypeNeedsLoader(profile.type);
  const profileMemoryValid = validMemoryRange(profile.minMemoryMb, profile.maxMemoryMb);
  const canSaveProfile = Boolean(metadata && profile.name.trim() && profileMinecraftVersion && (!profileNeedsLoader || profile.loaderVersion) && profileMemoryValid && !profileBusy);
  const canSaveSettings = Boolean(
    draft && draft.levelName.trim() && draft.maxPlayers >= 1 && draft.maxPlayers <= 1000 &&
    draft.serverPort >= 1 && draft.serverPort <= 65535 && draft.viewDistance >= 2 && draft.viewDistance <= 32 &&
    draft.simulationDistance >= 2 && draft.simulationDistance <= 32 && !settingsBusy,
  );
  const profileDirty = profile.name !== server.name ||
    profile.type !== server.type ||
    profile.minecraftVersion !== server.minecraftVersion ||
    profile.loaderVersion !== server.loaderVersion ||
    profile.javaPath !== server.javaPath ||
    profile.minMemoryMb !== server.minMemoryMb ||
    profile.maxMemoryMb !== server.maxMemoryMb ||
    profile.launchJar !== server.launchJar ||
    profile.extraArgs !== server.extraArgs;
  const iconDirty = Boolean(pendingIconFile) || iconResetPending;
  const settingsDirty = Boolean(properties && draft && (
    eulaAccepted !== properties.eulaAccepted ||
    sortedRecordJson(draft) !== sortedRecordJson(properties.editable) ||
    sortedRecordJson(rawDraft) !== sortedRecordJson(properties.raw)
  )) || iconDirty;
  const hasUnsavedChanges = profileDirty || settingsDirty;

  async function saveProfile() {
    if (!canSaveProfile) return false;
    setProfileBusy(true);
    try {
      await updateServerProfile(server.id, { ...profile, loaderVersion: profileNeedsLoader ? profile.loaderVersion : "" });
      await onSaved();
      onMessage("Profile saved");
      return true;
    } catch (error) { onMessage(error instanceof Error ? error.message : "Profile save failed"); return false; }
    finally { setProfileBusy(false); }
  }

  async function saveSettings() {
    if (!draft || !canSaveSettings) return false;
    setSettingsBusy(true);
    try {
      if (iconResetPending) {
        await runFileAction(server.id, { action: "delete", path: "server-icon.png" });
        setIconResetPending(false);
        setPendingIconFile(null);
        setIconPreviewUrl("");
        setIconFallback(true);
        setIconVersion((v) => v + 1);
        notifyServerIconUpdated(server.id);
      } else if (pendingIconFile) {
        const form = new FormData();
        form.set("action", "upload");
        form.set("path", "");
        form.set("file", pendingIconFile, "server-icon.png");
        await uploadServerFile(server.id, form);
        setPendingIconFile(null);
        setIconVersion((v) => v + 1);
        notifyServerIconUpdated(server.id);
      }
      const data = await saveServerProperties(server.id, { editable: draft, raw: rawDraft, eulaAccepted });
      setProperties(data);
      setDraft(data.editable);
      setRawDraft(data.raw);
      setEulaAccepted(data.eulaAccepted);
      await onSaved();
      onMessage("Settings saved");
      return true;
    } catch (error) { onMessage(error instanceof Error ? error.message : "Settings save failed"); return false; }
    finally { setSettingsBusy(false); }
  }

  async function saveDirtySections() {
    if (settingsDirty) {
      const saved = await saveSettings();
      if (!saved) throw new Error("Settings save failed");
    }
    if (profileDirty) {
      const saved = await saveProfile();
      if (!saved) throw new Error("Profile save failed");
    }
  }

  const saveDirtySectionsRef = useRef(saveDirtySections);
  useEffect(() => { saveDirtySectionsRef.current = saveDirtySections; });

  useEffect(() => {
    onUnsavedChange(hasUnsavedChanges ? {
      id: `server-settings:${server.id}`,
      label: "Server settings",
      dirty: true,
      canSave: (!settingsDirty || canSaveSettings) && (!profileDirty || canSaveProfile),
      onSave: () => saveDirtySectionsRef.current(),
    } : null);
    return () => onUnsavedChange(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasUnsavedChanges, settingsDirty, profileDirty, canSaveSettings, canSaveProfile, server.id]);

  function setField<K extends keyof ServerProperties["editable"]>(key: K, value: ServerProperties["editable"][K]) {
    setDraft((current) => (current ? { ...current, [key]: value } : current));
    const rawKey = Object.entries(editablePropertyMap).find(([, editableKey]) => editableKey === key)?.[0];
    if (rawKey) {
      setRawDraft((current) => ({ ...current, [rawKey]: rawValueForEditableField(key, value) }));
    }
  }

  function setRawProperty(key: string, value: string) {
    setRawDraft((current) => ({ ...current, [key]: value }));
    const editableKey = editablePropertyMap[key as keyof typeof editablePropertyMap];
    if (editableKey) {
      setDraft((current) => (current ? { ...current, [editableKey]: editableValueFromRaw(editableKey, value) } : current));
    }
  }

  function uploadServerIcon(file: File | null) {
    if (!file) return;
    if (file.type && file.type !== "image/png") {
      onMessage("Server icon must be a PNG file");
      return;
    }
    const img = new Image();
    const url = URL.createObjectURL(file);
    img.onload = () => {
      URL.revokeObjectURL(url);
      if (img.naturalWidth === img.naturalHeight) {
        applyPendingIcon(file);
      } else {
        setCropFile(file);
      }
    };
    img.onerror = () => {
      URL.revokeObjectURL(url);
      onMessage("Could not read the image file");
    };
    img.src = url;
  }

  function applyPendingIcon(file: File) {
    setIconResetPending(false);
    setIconFallback(false);
    setPendingIconFile(file);
    setIconPreviewUrl((current) => {
      if (current) URL.revokeObjectURL(current);
      return URL.createObjectURL(file);
    });
  }

  function resetIconToDefault() {
    setPendingIconFile(null);
    setIconResetPending(true);
    setIconPreviewUrl((current) => {
      if (current) URL.revokeObjectURL(current);
      return "";
    });
    setIconFallback(true);
  }

  if (!draft) return (
    <section className="server-settings-page">
      <div className="settings-page-header">
        <div className="settings-page-title">
          <h1><span className="workspace-page-icon"><Settings /></span>Settings</h1>
          <p>Configure game behavior and the server profile.</p>
        </div>
      </div>
      <Panel className="form-grid compact-form settings-panel"><p className="muted">Loading...</p></Panel>
    </section>
  );

  return (
    <section className="server-settings-page">
      <div className="settings-page-header">
        <div className="settings-page-title">
          <h1><span className="workspace-page-icon"><Settings /></span>Settings</h1>
          <p>Configure game behavior and the server profile.</p>
        </div>
        <Toolbar>
          {activeSettingsTab === "profile" ? (
            <Button variant="primary" disabled={!canSaveProfile} onClick={saveProfile} loading={profileBusy} loadingText="Saving...">Save</Button>
          ) : (
            <Button variant="primary" disabled={!canSaveSettings} onClick={saveSettings} loading={settingsBusy} loadingText="Saving...">Save</Button>
          )}
        </Toolbar>
      </div>

      <Tabs
        ariaLabel="Server settings sections"
        items={[
          { id: "game", label: "Game" },
          { id: "profile", label: "Profile" },
        ]}
        activeId={activeSettingsTab}
        onChange={(id) => {
          setActiveSettingsTab(id as typeof activeSettingsTab);
          if (typeof window !== "undefined") {
            window.history.replaceState(null, "", id === "game" ? window.location.pathname : `${window.location.pathname}#${id}`);
          }
        }}
      />

      {activeSettingsTab === "profile" && (
        <Panel className="form-grid compact-form settings-panel">
          {isRunning && <Hint warn>Profile changes apply on the next start.</Hint>}
          <div className="settings-section">
            <h2 className="settings-section-header">General</h2>
            <Input label="Name" value={profile.name} onChange={(event) => setProfile((current) => ({ ...current, name: event.target.value }))} />
          </div>
          <div className="settings-section">
            <h2 className="settings-section-header">Version</h2>
            <label>Minecraft<VersionSelect value={profileMinecraftVersion} serverType={profile.type} metadata={metadata} metadataError={metadataError} onChange={(value) => setProfile((current) => ({ ...current, minecraftVersion: value, loaderVersion: "" }))} /></label>
          {profileNeedsLoader && (
              <details className="advanced-section compact profile-advanced">
                <summary>
                  <span>Advanced Loader Options</span>
                </summary>
                <label>Loader<LoaderSelect type={profile.type} minecraftVersion={profileMinecraftVersion} metadata={metadata} metadataError={metadataError} value={profile.loaderVersion} onChange={(value) => setProfile((current) => ({ ...current, loaderVersion: value }))} /></label>
              </details>
            )}
          </div>
          <details className="advanced-section compact profile-advanced">
            <summary><span>Runtime</span></summary>
            <div className="settings-section">
              <Input label="Java runtime" value={profile.javaPath} onChange={(event) => setProfile((current) => ({ ...current, javaPath: event.target.value }))} />
              <JavaPresetRow javaPath={profile.javaPath} onApply={(javaPath) => setProfile((current) => ({ ...current, javaPath }))} />
              <Hint>Auto-managed installs and uses the Java version required by this Minecraft profile on first start.</Hint>
              <FieldGrid columns={2}>
                <Input label="Min memory" type="number" value={profile.minMemoryMb} onChange={(event) => setProfile((current) => ({ ...current, minMemoryMb: Number(event.target.value) }))} />
                <Input label="Max memory" type="number" value={profile.maxMemoryMb} onChange={(event) => setProfile((current) => ({ ...current, maxMemoryMb: Number(event.target.value) }))} />
              </FieldGrid>
              <MemoryPresetRow minMemoryMb={profile.minMemoryMb} maxMemoryMb={profile.maxMemoryMb} onApply={(minMemoryMb, maxMemoryMb) => setProfile((current) => ({ ...current, minMemoryMb, maxMemoryMb }))} />
              <Input label="Launch target" value={profile.launchJar} onChange={(event) => setProfile((current) => ({ ...current, launchJar: event.target.value }))} />
              <Input label="Extra args" value={profile.extraArgs} onChange={(event) => setProfile((current) => ({ ...current, extraArgs: event.target.value }))} />
              <ExtraArgsPresetRow extraArgs={profile.extraArgs} onApply={(extraArgs) => setProfile((current) => ({ ...current, extraArgs }))} />
            </div>
          </details>
        </Panel>
      )}

      {activeSettingsTab === "game" && (
        <Panel className="form-grid compact-form settings-panel">
          {isRunning && <Hint warn>Most game settings require a restart.</Hint>}
          <div className="settings-section">
            <h2 className="settings-section-header">Server list</h2>
            <div className="settings-toggle-row eula-toggle-row">
              <div className="settings-toggle-copy">
                <strong>Accept Minecraft EULA</strong>
                <span>Required before the server can start.</span>
              </div>
              <Toggle checked={eulaAccepted} onChange={setEulaAccepted} aria-label="Accept Minecraft EULA" />
            </div>
            <div className="server-list-editor">
              <div className="mc-server-preview" aria-label="Minecraft server list preview">
                <div className="mc-server-icon">
                  {/* eslint-disable-next-line @next/next/no-img-element */}
                  <img
                    src={iconFallback ? "/assets/default-server-icon.png" : iconPreviewUrl || serverFileUrl(server.id, "server-icon.png", `raw=1&v=${iconVersion}`)}
                    alt=""
                    onLoad={(event) => { event.currentTarget.style.display = "block"; }}
                    onError={() => setIconFallback(true)}
                  />
                </div>
                <div className="mc-server-copy">
                  <strong>{profile.name || server.name}</strong>
                  <span>{draft.motd || "A Minecraft Server"}</span>
                </div>
                <div className="mc-server-stats">
                  <span>0/{draft.maxPlayers}</span>
                  <span className="mc-signal" aria-hidden="true"><i /><i /><i /><i /></span>
                </div>
              </div>
              <div className="server-list-fields">
                <Input label="Server list description" value={draft.motd} onChange={(event) => setField("motd", event.target.value)} />
                <div className="server-icon-upload-row">
                  <label className="server-icon-upload">
                    <span>Server thumbnail</span>
                    <Input type="file" accept="image/png" onChange={(event) => uploadServerIcon(event.target.files?.[0] ?? null)} />
                  </label>
                  <Button type="button" onClick={resetIconToDefault} disabled={iconResetPending || (!pendingIconFile && iconFallback)}>
                    <RotateCcw size={14} /> Reset to default
                  </Button>
                </div>
              </div>
            </div>
          </div>
          <ImageCropModal
            file={cropFile}
            onClose={() => setCropFile(null)}
            onCrop={(cropped) => { setCropFile(null); applyPendingIcon(cropped); }}
            title="Crop server thumbnail"
            description="Drag to position the square crop, then click Apply."
          />
          <div className="settings-section">
            <h2 className="settings-section-header">World</h2>
            <FieldGrid columns={2}>
              <Input label="World folder" value={draft.levelName} onChange={(event) => setField("levelName", event.target.value)} />
              <Input label="Seed" value={draft.levelSeed} onChange={(event) => setField("levelSeed", event.target.value)} placeholder="random" />
            </FieldGrid>
          </div>
          <div className="settings-section">
            <h2 className="settings-section-header">Gameplay</h2>
            <FieldGrid columns={2}>
              <Select label="Gamemode" value={draft.gamemode} onChange={(event) => setField("gamemode", event.target.value)}><option>survival</option><option>creative</option><option>adventure</option><option>spectator</option></Select>
              <Select label="Difficulty" value={draft.difficulty} onChange={(event) => setField("difficulty", event.target.value)}><option>peaceful</option><option>easy</option><option>normal</option><option>hard</option></Select>
            </FieldGrid>
            <FieldGrid columns={2}>
              <Input label="Max players" type="number" min={1} max={1000} value={draft.maxPlayers} onChange={(event) => setField("maxPlayers", Number(event.target.value))} />
              <Input label="Port" type="number" min={1} max={65535} value={draft.serverPort} onChange={(event) => setField("serverPort", Number(event.target.value))} />
            </FieldGrid>
          </div>
          <div className="settings-section">
            <h2 className="settings-section-header">Rules</h2>
            <FieldGrid columns={2}>
              <Input label="View distance" type="number" min={2} max={32} value={draft.viewDistance} onChange={(event) => setField("viewDistance", Number(event.target.value))} />
              <Input label="Simulation distance" type="number" min={2} max={32} value={draft.simulationDistance} onChange={(event) => setField("simulationDistance", Number(event.target.value))} />
            </FieldGrid>
            <div className="settings-toggle-grid">
              <div className="settings-toggle-row">
                <div className="settings-toggle-copy">
                  <strong>Online mode</strong>
                  <span>Verify players against Minecraft servers.</span>
                </div>
                <Toggle checked={draft.onlineMode} onChange={(checked) => setField("onlineMode", checked)} aria-label="Online mode" />
              </div>
              <div className="settings-toggle-row">
                <div className="settings-toggle-copy">
                  <strong>Whitelist</strong>
                  <span>Only allow listed players to join.</span>
                </div>
                <Toggle checked={draft.whiteList} onChange={(checked) => setField("whiteList", checked)} aria-label="Whitelist" />
              </div>
              <div className="settings-toggle-row">
                <div className="settings-toggle-copy">
                  <strong>PVP</strong>
                  <span>Allow players to damage each other.</span>
                </div>
                <Toggle checked={draft.pvp} onChange={(checked) => setField("pvp", checked)} aria-label="PVP" />
              </div>
              <div className="settings-toggle-row">
                <div className="settings-toggle-copy">
                  <strong>Command blocks</strong>
                  <span>Enable command block functionality.</span>
                </div>
                <Toggle checked={draft.enableCommandBlock} onChange={(checked) => setField("enableCommandBlock", checked)} aria-label="Command blocks" />
              </div>
              <div className="settings-toggle-row">
                <div className="settings-toggle-copy">
                  <strong>Allow flight</strong>
                  <span>Let players fly in survival mode.</span>
                </div>
                <Toggle checked={draft.allowFlight} onChange={(checked) => setField("allowFlight", checked)} aria-label="Allow flight" />
              </div>
            </div>
          </div>
          <details className="advanced-section compact profile-advanced">
            <summary><span>Raw properties</span></summary>
            <Hint>All {Object.keys(properties?.raw ?? {}).length} server.properties values currently loaded for this server.</Hint>
            <div className="settings-section">
                <div className="raw-property-grid">
                  {Object.entries(rawDraft).map(([key, value]) => (
                    <Input key={key} label={key} value={value} onChange={(event) => setRawProperty(key, event.target.value)} />
                  ))}
                </div>
            </div>
          </details>
        </Panel>
      )}
    </section>
  );
}


