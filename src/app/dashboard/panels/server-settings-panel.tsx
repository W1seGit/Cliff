"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Cpu, Gamepad2, Server, SlidersHorizontal, Wrench } from "lucide-react";
import { serverTypeNeedsLoader, validMemoryRange } from "../lib/utils";
import { fetchServerProperties, runFileAction, saveServerProperties, serverFileUrl, updateServerProfile, uploadServerFile } from "../lib/runtime-client";
import { useHashSection } from "../lib/use-hash-section";
import { editableFromRaw, parsePropertiesText, sameProperties, setPropertyInText, validatePropertiesText } from "../lib/properties-text";
import type { MinecraftMetadata, ServerProperties, ServerPropertiesEditable, ServerRecord, UnsavedChangesRegistration } from "../lib/types";
import { Banner, Card, PageHeader, SettingsLayout, SettingsSectionPanel, SkeletonRows } from "../components/ui";
import { ImageCropModal } from "../components/ui/image-crop-modal";
import { notifyServerIconUpdated } from "../components/server-avatar";
import { EulaCard, GameplayCard, RulesCard, WorldCard } from "./server-settings/game-sections";
import { ServerListCard } from "./server-settings/server-list-card";
import { ProfileGeneralCard, ProfileVersionCard, RuntimeSections } from "./server-settings/profile-sections";
import { PropertiesEditorCard } from "./server-settings/properties-editor";

const settingsSections = ["game", "profile", "runtime", "advanced"] as const;
type SettingsSection = (typeof settingsSections)[number];

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
  // The text of server.properties is the single source of truth. The Game tab
  // fields are derived from it and edit one line at a time, so comments and
  // key order in the file survive.
  const [propsText, setPropsText] = useState("");
  const [eulaAccepted, setEulaAccepted] = useState(false);
  const [profileBusy, setProfileBusy] = useState(false);
  const [settingsBusy, setSettingsBusy] = useState(false);
  const [iconPreviewUrl, setIconPreviewUrl] = useState("");
  const [iconFallback, setIconFallback] = useState(false);
  const [iconVersion, setIconVersion] = useState(0);
  const [pendingIconFile, setPendingIconFile] = useState<File | null>(null);
  const [iconResetPending, setIconResetPending] = useState(false);
  const [cropFile, setCropFile] = useState<File | null>(null);
  const [activeSection, selectSection] = useHashSection<SettingsSection>(settingsSections, "game");
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
      .then((data) => { setProperties(data); setPropsText(data.text ?? ""); setEulaAccepted(data.eulaAccepted); })
      .catch((error) => onMessage(error.message));
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [server.id]);

  useEffect(() => () => {
    if (iconPreviewUrl) URL.revokeObjectURL(iconPreviewUrl);
  }, [iconPreviewUrl]);

  const rawDraft = useMemo(() => parsePropertiesText(propsText), [propsText]);
  const draft = useMemo(() => (properties ? editableFromRaw(rawDraft) : null), [properties, rawDraft]);
  const savedEditable = useMemo(() => (properties ? editableFromRaw(parsePropertiesText(properties.text ?? "")) : null), [properties]);
  const propsIssues = useMemo(() => validatePropertiesText(propsText), [propsText]);
  const profileMinecraftVersion = profile.minecraftVersion || metadata?.latest.release || "";
  const profileNeedsLoader = serverTypeNeedsLoader(profile.type);
  const profileMemoryValid = validMemoryRange(profile.minMemoryMb, profile.maxMemoryMb);
  const canSaveProfile = Boolean(metadata && profile.name.trim() && profileMinecraftVersion && (!profileNeedsLoader || profile.loaderVersion) && profileMemoryValid && !profileBusy);
  const canSaveSettings = Boolean(
    draft && draft.levelName.trim() && draft.maxPlayers >= 1 && draft.maxPlayers <= 1000 &&
    draft.serverPort >= 1 && draft.serverPort <= 65535 && draft.viewDistance >= 2 && draft.viewDistance <= 32 &&
    draft.simulationDistance >= 2 && draft.simulationDistance <= 32 && propsIssues.length === 0 && !settingsBusy,
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
  const settingsDirty = Boolean(properties && (
    eulaAccepted !== properties.eulaAccepted ||
    !sameProperties(propsText, properties.text ?? "")
  )) || iconDirty;
  const hasUnsavedChanges = profileDirty || settingsDirty;
  const gameDirty = Boolean(properties && draft && savedEditable && (
    eulaAccepted !== properties.eulaAccepted ||
    sortedRecordJson(draft) !== sortedRecordJson(savedEditable)
  )) || iconDirty;
  const advancedDirty = Boolean(properties && !sameProperties(propsText, properties.text ?? ""));
  const profileSectionDirty = profile.name !== server.name ||
    profile.type !== server.type ||
    profile.minecraftVersion !== server.minecraftVersion ||
    profile.loaderVersion !== server.loaderVersion;
  const runtimeDirty = profile.javaPath !== server.javaPath ||
    profile.minMemoryMb !== server.minMemoryMb ||
    profile.maxMemoryMb !== server.maxMemoryMb ||
    profile.launchJar !== server.launchJar ||
    profile.extraArgs !== server.extraArgs;
  const saveBlockedReason = settingsDirty && !canSaveSettings
    ? propsIssues.length > 0
      ? `server.properties has ${propsIssues.length} problem${propsIssues.length === 1 ? "" : "s"}. Fix ${propsIssues.length === 1 ? "it" : "them"} to save.`
      : "Fix the highlighted game settings to save."
    : profileDirty && !canSaveProfile
      ? metadata ? "Fix the highlighted profile fields to save." : "Version data is still loading."
      : undefined;

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
      const data = await saveServerProperties(server.id, { text: propsText, eulaAccepted });
      setProperties(data);
      setPropsText(data.text ?? "");
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

  function discardChanges() {
    setProfile({
      name: server.name, type: server.type, minecraftVersion: server.minecraftVersion, loaderVersion: server.loaderVersion,
      javaPath: server.javaPath, minMemoryMb: server.minMemoryMb, maxMemoryMb: server.maxMemoryMb, launchJar: server.launchJar, extraArgs: server.extraArgs,
    });
    if (properties) {
      setPropsText(properties.text ?? "");
      setEulaAccepted(properties.eulaAccepted);
    }
    setPendingIconFile(null);
    setIconResetPending(false);
    setIconPreviewUrl("");
    setIconFallback(false);
    setIconVersion(Date.now());
  }

  const saveDirtySectionsRef = useRef(saveDirtySections);
  const discardChangesRef = useRef(discardChanges);
  useEffect(() => {
    saveDirtySectionsRef.current = saveDirtySections;
    discardChangesRef.current = discardChanges;
  });

  useEffect(() => {
    onUnsavedChange(hasUnsavedChanges ? {
      id: `server-settings:${server.id}`,
      label: "Server settings",
      dirty: true,
      showSaveBar: true,
      saving: settingsBusy || profileBusy,
      canSave: (!settingsDirty || canSaveSettings) && (!profileDirty || canSaveProfile),
      disabledReason: saveBlockedReason,
      onSave: () => saveDirtySectionsRef.current(),
      onDiscard: () => discardChangesRef.current(),
    } : null);
    return () => onUnsavedChange(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasUnsavedChanges, settingsDirty, profileDirty, canSaveSettings, canSaveProfile, settingsBusy, profileBusy, saveBlockedReason, server.id]);

  function setField<K extends keyof ServerProperties["editable"]>(key: K, value: ServerProperties["editable"][K]) {
    const rawKey = Object.entries(editablePropertyMap).find(([, editableKey]) => editableKey === key)?.[0];
    if (!rawKey) return;
    setPropsText((current) => setPropertyInText(current, rawKey, rawValueForEditableField(key, value)));
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

  const navItems = [
    { id: "game", label: "Game", icon: <Gamepad2 size={16} aria-hidden="true" />, dirty: gameDirty },
    { id: "profile", label: "Profile", icon: <Server size={16} aria-hidden="true" />, dirty: profileSectionDirty },
    { id: "runtime", label: "Runtime", icon: <Cpu size={16} aria-hidden="true" />, dirty: runtimeDirty },
    { id: "advanced", label: "Advanced", icon: <Wrench size={16} aria-hidden="true" />, dirty: advancedDirty },
  ];
  const header = <PageHeader title="Settings" icon={<SlidersHorizontal size={20} aria-hidden="true" />} description="Configure game behavior and the server profile." />;

  if (!draft) return (
    <section className="server-settings-page">
      {header}
      <Card title="Loading settings"><SkeletonRows rows={4} label="Loading server settings" /></Card>
    </section>
  );

  const idPrefix = "server-settings";
  const restartNote = isRunning ? <Banner variant="warning">Most game settings require a restart to take effect.</Banner> : null;
  const profileNote = isRunning ? <Banner variant="warning">Profile changes apply the next time the server starts.</Banner> : null;
  const iconSrc = iconFallback ? "/assets/default-server-icon.png" : iconPreviewUrl || serverFileUrl(server.id, "server-icon.png", `raw=1&v=${iconVersion}`);

  return (
    <section className="server-settings-page">
      {header}
      <SettingsLayout ariaLabel="Server settings sections" items={navItems} activeId={activeSection} onChange={selectSection} idPrefix={idPrefix}>
        <SettingsSectionPanel idPrefix={idPrefix} id="game" activeId={activeSection}>
          {restartNote}
          <EulaCard accepted={eulaAccepted} onChange={setEulaAccepted} />
          <ServerListCard
            serverName={profile.name || server.name}
            draft={draft}
            onMotdChange={(value) => setField("motd", value)}
            iconSrc={iconSrc}
            onIconError={() => setIconFallback(true)}
            onIconFile={uploadServerIcon}
            onIconReset={resetIconToDefault}
            iconResetDisabled={iconResetPending || (!pendingIconFile && iconFallback)}
          />
          <WorldCard draft={draft} setField={setField} />
          <GameplayCard draft={draft} setField={setField} />
          <RulesCard draft={draft} setField={setField} />
        </SettingsSectionPanel>
        <SettingsSectionPanel idPrefix={idPrefix} id="profile" activeId={activeSection}>
          {profileNote}
          <ProfileGeneralCard profile={profile} setProfile={setProfile} />
          <ProfileVersionCard profile={profile} setProfile={setProfile} minecraftVersion={profileMinecraftVersion} needsLoader={profileNeedsLoader} metadata={metadata} metadataError={metadataError} />
        </SettingsSectionPanel>
        <SettingsSectionPanel idPrefix={idPrefix} id="runtime" activeId={activeSection}>
          {profileNote}
          <RuntimeSections profile={profile} setProfile={setProfile} />
        </SettingsSectionPanel>
        <SettingsSectionPanel idPrefix={idPrefix} id="advanced" activeId={activeSection}>
          <PropertiesEditorCard serverId={server.id} value={propsText} onChange={setPropsText} issues={propsIssues} running={isRunning} />
        </SettingsSectionPanel>
      </SettingsLayout>
      <ImageCropModal
        file={cropFile}
        onClose={() => setCropFile(null)}
        onCrop={(cropped) => { setCropFile(null); applyPendingIcon(cropped); }}
        title="Crop server thumbnail"
        description="Drag to position the square crop, then click Apply."
      />
    </section>
  );
}
