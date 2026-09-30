"use client";

import { HardDrive, ScrollText, Package, Coffee, Download } from "lucide-react";
import { browserOrigin, externalApiBase } from "../lib/utils";
import { useHashSection } from "../lib/use-hash-section";
import type { ConfirmRequest, MinecraftMetadata, Settings, UnsavedChangesRegistration, UpdateCheckResult, User } from "../lib/types";
import { SettingsLayout, SettingsSectionPanel } from "../components/ui";
import { AccountSettings } from "./app-settings/account-settings";
import { GeneralTab } from "./app-settings/general-tab";
import { JavaTab } from "./app-settings/java-tab";
import { LogsTab } from "./app-settings/logs-tab";
import { StorageTab } from "./app-settings/storage-tab";
import { UpdatesTab } from "./app-settings/updates-tab";
import { useDaemonLogs, useJavaRuntimes, useTypeVersionCounts, useUpdates } from "./app-settings/hooks";

const sections = ["general", "java", "network", "logs", "updates"] as const;
type SettingsSection = (typeof sections)[number];

const navItems = [
  { id: "general", label: "Versions", icon: <Package size={16} aria-hidden="true" /> },
  { id: "java", label: "Java", icon: <Coffee size={16} aria-hidden="true" /> },
  { id: "network", label: "Network & Storage", icon: <HardDrive size={16} aria-hidden="true" /> },
  { id: "logs", label: "Logs", icon: <ScrollText size={16} aria-hidden="true" /> },
  { id: "updates", label: "Updates", icon: <Download size={16} aria-hidden="true" /> },
];

type AppSettingsPanelProps = {
  mode?: "settings" | "account";
  user: User;
  settings: Settings;
  metadata: MinecraftMetadata | null;
  metadataError: string;
  metadataBusy: boolean;
  updateCheck?: UpdateCheckResult | null;
  onRefreshVersions: () => void;
  onAccountSaved: (user: User) => void;
  onSaved: () => void;
  onMessage: (message: string) => void;
  onUnsavedChange: (change: UnsavedChangesRegistration | null) => void;
  onConfirm: (request: ConfirmRequest) => void;
};

export function AppSettingsPanel(props: AppSettingsPanelProps) {
  if (props.mode === "account") {
    return <AccountSettings user={props.user} onAccountSaved={props.onAccountSaved} onMessage={props.onMessage} onUnsavedChange={props.onUnsavedChange} />;
  }
  return <AppSettings {...props} />;
}

function AppSettings({ settings, metadata, metadataError, metadataBusy, updateCheck, onRefreshVersions, onMessage, onConfirm }: AppSettingsPanelProps) {
  const [section, selectSection] = useHashSection<SettingsSection>(sections, "general");
  const java = useJavaRuntimes(true, onMessage);
  const versions = useTypeVersionCounts(true);
  const logs = useDaemonLogs(section === "logs", onMessage);
  const updates = useUpdates(updateCheck, onMessage);

  const lanAddresses = settings.access?.lanAddresses ?? [];
  const backendUrl = externalApiBase() || (typeof window !== "undefined" ? browserOrigin() : "");
  const dashboardPort = (() => {
    if (typeof window === "undefined") return "8080";
    const port = new URL(window.location.origin).port;
    return port || (window.location.protocol === "https:" ? "443" : "80");
  })();

  const idPrefix = "app-settings";
  return (
    <SettingsLayout ariaLabel="App settings sections" items={navItems} activeId={section} onChange={selectSection} idPrefix={idPrefix}>
      <SettingsSectionPanel idPrefix={idPrefix} id="general" activeId={section}>
        <GeneralTab
          metadata={metadata}
          metadataError={metadataError}
          counts={versions.counts}
          experimental={versions.experimental}
          refreshing={metadataBusy || versions.busy}
          onRefresh={() => {
            onRefreshVersions();
            void versions.refresh();
          }}
        />
      </SettingsSectionPanel>
      <SettingsSectionPanel idPrefix={idPrefix} id="java" activeId={section}>
        <JavaTab
          runtimes={java.runtimes}
          loaded={java.loaded}
          busy={java.busy}
          onInstall={java.install}
          onUninstall={java.uninstall}
          onRefresh={java.refresh}
          onConfirm={onConfirm}
        />
      </SettingsSectionPanel>
      <SettingsSectionPanel idPrefix={idPrefix} id="network" activeId={section}>
        <StorageTab settings={settings} lanAddresses={lanAddresses} dashboardPort={dashboardPort} backendUrl={backendUrl} onMessage={onMessage} />
      </SettingsSectionPanel>
      <SettingsSectionPanel idPrefix={idPrefix} id="logs" activeId={section}>
        <LogsTab
          settings={settings}
          lines={logs.lines}
          loaded={logs.loaded}
          busy={logs.busy}
          mode={logs.mode}
          onModeChange={logs.setMode}
          onRefresh={logs.refresh}
          onCopy={logs.copy}
        />
      </SettingsSectionPanel>
      <SettingsSectionPanel idPrefix={idPrefix} id="updates" activeId={section}>
        <UpdatesTab
          check={updates.check}
          checking={updates.checking}
          installing={updates.installing}
          progress={updates.progress}
          installError={updates.installError}
          failedStage={updates.failedStage}
          safety={updates.safety}
          clearing={updates.clearing}
          onCheck={updates.checkNow}
          onInstall={updates.install}
          onClearSafety={() => onConfirm({
            title: "Delete the update safety copies?",
            message: "This removes the previous version and the database copies Cliff kept from before updates. You will no longer be able to undo the last update. Your servers, worlds and settings are not touched.",
            confirmLabel: "Delete safety copies",
            dangerous: true,
            onConfirm: updates.clearSafety,
          })}
        />
      </SettingsSectionPanel>
    </SettingsLayout>
  );
}
