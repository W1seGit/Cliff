"use client";

import { useEffect, useState } from "react";
import Image from "next/image";
import {
  CheckCircle2, Clipboard, RadioTower,
} from "lucide-react";
import {
  deletePublicAccessConfig,
  fetchPlayitAgent,
  fetchPublicAccessConfig,
  installPlayitAgent as installManagedPlayitAgent,
  uninstallPlayitAgent as uninstallManagedPlayitAgent,
  resetPlayitAgent as resetManagedPlayitAgent,
  savePublicAccessConfig,
  startPlayitAgent as startManagedPlayitAgent,
  stopPlayitAgent as stopManagedPlayitAgent,
} from "../lib/runtime-client";
import type { PlayitAgentInfo, PublicAccessRecord, ServerRecord } from "../lib/types";
import { publicAccessStorageKey } from "../lib/utils";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Panel } from "../components/ui/panel";
import { Toolbar } from "../components/ui/toolbar";
import { Tabs } from "../components/ui/tabs";
import { Hint } from "../components/ui/hint";
import { Toggle } from "../components/ui/toggle";
import { JoinAddress } from "../components/ui/join-address";
import { ConsoleView } from "../components/ui/console-view";

type InstallState = "not-installed" | "installing" | "installed" | "failed";
type ClaimState = "starting-agent" | "waiting-for-claim-link" | "claim-link-ready" | "waiting-for-user-to-claim" | "claimed" | "failed";
type TunnelState = "no-tunnel-found" | "waiting-for-tunnel" | "tunnel-detected" | "tunnel-ready" | "failed";
type PublicAccessConfig = {
  installed: boolean;
  claimed: boolean;
  claimUrl: string;
  publicAddress: string;
  agentPath: string;
  updatedAt: string;
  enabled: boolean;
};

const playitLinks = {
  homepage: "https://playit.gg/",
  support: "https://playit.gg/support/",
  tunnelSetup: "https://playit.gg/account/tunnels",
  releases: "https://github.com/playit-cloud/playit-agent/releases",
};

const emptyConfig: PublicAccessConfig = {
  installed: false,
  claimed: false,
  claimUrl: "",
  publicAddress: "",
  agentPath: "",
  updatedAt: "",
  enabled: false,
};
const AGENT_POLL_INTERVAL_MS = 5000;

function readStoredConfig(serverId: string) {
  if (typeof window === "undefined") return emptyConfig;
  const raw = window.localStorage.getItem(publicAccessStorageKey(serverId));
  if (!raw) return emptyConfig;
  try {
    const stored = JSON.parse(raw) as Partial<PublicAccessConfig>;
    const parsed = { ...emptyConfig, ...stored } as PublicAccessConfig;
    if (parsed.publicAddress && typeof stored.enabled !== "boolean") parsed.enabled = true;
    return parsed;
  } catch {
    return emptyConfig;
  }
}

function dbRecordToConfig(record?: PublicAccessRecord | null): Partial<PublicAccessConfig> {
  if (!record) return {};
  return {
    claimed: record.claimed,
    publicAddress: record.publicAddress,
    agentPath: record.agentPath,
    updatedAt: record.updatedAt,
  };
}

function validClaimUrl(value: string) {
  return /^https:\/\/playit\.gg\/claim\/[A-Za-z0-9_-]+\/?$/.test(value.trim());
}

function validJoinAddress(value: string) {
  return /^[A-Za-z0-9.-]+\.[A-Za-z]{2,}(?::\d{1,5})?$/.test(value.trim());
}

function normalizeTunnelAddress(value: string) {
  const withoutProtocol = value.replace(/^[a-z]+:\/\//i, "").trim();
  const withoutPath = withoutProtocol.split("/")[0];
  return withoutPath
    .replace(/^[>\]\)\}"'`]+/, "")
    .replace(/[<\[\(\{"'`.,;:]+$/, "")
    .toLowerCase();
}

function looksLikeTunnelAddress(line: string, localTarget: string) {
  const lower = line.toLowerCase();
  return lower.includes("localhost") || lower.includes("tunnel") || lower.includes("proxy") || lower.includes("endpoint") || lower.includes("address") || lower.includes(localTarget.toLowerCase());
}

function detectTunnelAddressFromAgent(agent: PlayitAgentInfo, localTarget: string, localPort: number) {
  const structuredTunnel = (agent.tunnels ?? []).find((tunnel) =>
    tunnel.publicAddress
    && tunnel.localPort === localPort
    && (!tunnel.tunnelType || tunnel.tunnelType === "minecraft-java"));
  if (structuredTunnel) return normalizeTunnelAddress(structuredTunnel.publicAddress);

  const fields = new Set<string>();
  const payload = agent as Record<string, unknown>;
  const addressPattern = /(?:https?:\/\/)?[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+(?:\.[A-Za-z]{2,})(?:\:\d{1,5})?/g;
  const maybePushAddress = (candidate: string) => {
    const normalized = normalizeTunnelAddress(candidate);
    if (!normalized || normalized === "playit.gg" || normalized.includes("localhost")) return;
    if (validJoinAddress(normalized)) fields.add(normalized);
  };

  const collectFromValue = (value: unknown) => {
    if (typeof value === "string") maybePushAddress(value);
    if (Array.isArray(value)) {
      value.forEach((entry) => {
        if (typeof entry === "string") maybePushAddress(entry);
        else if (entry && typeof entry === "object") collectFromValue((entry as { address?: string }).address);
      });
    } else if (value && typeof value === "object") {
      const nested = value as { address?: string };
      if (nested.address) maybePushAddress(nested.address);
    }
  };

  for (const key of ["address", "publicAddress", "public_address", "endpoint", "tunnel", "tunnelAddress", "tunnelUrl", "tunnel_url", "url"]) {
    collectFromValue(payload[key]);
  }

  collectFromValue(payload.tunnels);
  collectFromValue(payload.proxy);

  for (const line of (agent.logs ?? []).slice().reverse()) {
    if (!looksLikeTunnelAddress(line, localTarget)) continue;
    const matches = line.match(addressPattern);
    if (!matches) continue;
    for (const match of matches) {
      maybePushAddress(match);
    }
    if (fields.size) break;
  }

  return fields.size ? Array.from(fields)[0] : null;
}

function agentLogLevel(line: string): "error" | "warning" | "info" {
  if (/\b(error|exception|failed|fatal|crash)\b/i.test(line)) return "error";
  if (/\b(warn|warning|deprecated)\b/i.test(line)) return "warning";
  return "info";
}

// Format a raw playit log line into cleaner, more readable text.
// Raw format: "2026-06-23T08:19:03.986688Z  INFO playit_cli::playit_secret: loading secret file_path=..."
// Cleaned:    "08:19:03 INFO loading secret"
function formatPlayitLog(line: string): { time: string; text: string; level: "error" | "warning" | "info" } {
  // Match: TIMESTAMP  LEVEL  module_path: message
  const match = line.match(/^(\d{4}-\d{2}-\d{2}T(\d{2}:\d{2}:\d{2}))\.\d+Z\s+(INFO|WARN|ERROR|DEBUG|TRACE)\s+[\w:]+:\s*(.*)$/);
  if (match) {
    const time = match[2];
    const level = match[3];
    let msg = match[4];
    // Remove verbose pong struct output: pong=Pong { ... }
    msg = msg.replace(/pong=Pong\s*\{[^}]*\}/g, "pong received");
    // Remove verbose details= struct output
    msg = msg.replace(/details=\w+\s*\{[^}]*\}/g, "details received");
    // Remove addr=[...] verbose bracket content, keep just the addr value
    msg = msg.replace(/addr=\[([^\]]+)\]/g, "addr=$1");
    // Collapse multiple spaces
    msg = msg.replace(/\s+/g, " ").trim();
    const lvl: "error" | "warning" | "info" = level === "ERROR" ? "error" : level === "WARN" ? "warning" : "info";
    return { time, text: msg, level: lvl };
  }
  // Fallback: just collapse spaces
  return { time: "--:--:--", text: line.replace(/\s+/g, " ").trim(), level: agentLogLevel(line) };
}

export function PublicAccessPanel({
  mode = "dashboard",
  server,
  onConfigure,
  onBack,
  onMessage,
}: {
  mode?: "dashboard" | "setup";
  server: ServerRecord;
  onConfigure?: () => void;
  onBack?: () => void;
  onMessage: (message: string) => void;
}) {
  const [config, setConfig] = useState<PublicAccessConfig>(() => readStoredConfig(server.id));
  const [installState, setInstallState] = useState<InstallState>("not-installed");
  const [claimState, setClaimState] = useState<ClaimState>(() => {
    const stored = readStoredConfig(server.id);
    return stored.claimed ? "claimed" : stored.claimUrl ? "claim-link-ready" : "waiting-for-claim-link";
  });
  const [tunnelState, setTunnelState] = useState<TunnelState>(() => readStoredConfig(server.id).publicAddress ? "tunnel-ready" : "no-tunnel-found");
  const [agentError, setAgentError] = useState("");
  const [agentRunning, setAgentRunning] = useState(false);
  const [agentLogs, setAgentLogs] = useState<string[]>([]);
  const [tunnelReady, setTunnelReady] = useState(false);
  
  const [resetBusy, setResetBusy] = useState(false);
  const [uninstallBusy, setUninstallBusy] = useState(false);
  const [publicAccessBusy, setPublicAccessBusy] = useState(false);
  const [loadingConfig, setLoadingConfig] = useState(true);
  const [setupOpen, setSetupOpen] = useState(false);
  const [setupStep, setSetupStep] = useState(() => {
    const stored = readStoredConfig(server.id);
    if (stored.publicAddress) return 2;
    if (stored.claimed) return 2;
    if (stored.installed || stored.claimUrl) return 1;
    return 0;
  });
  const localTarget = `localhost:${server.port}`;
  const configured = Boolean(config.publicAddress);
  const agentInstalled = installState === "installed";
  const publicAccessRunning = configured && config.enabled !== false && agentRunning && tunnelReady;
  const publicAccessLoading = configured && config.enabled !== false && agentRunning && !tunnelReady;
  const flowSteps = ["Install Agent", "Connect Account", "Create Tunnel"];
  const setupStepValid = [agentInstalled, config.claimed, Boolean(config.publicAddress)];
  const setupActive = mode === "setup" || setupOpen;

  function goToSetupStep(nextStep: number) {
    setSetupStep(nextStep);
  }

  useEffect(() => {
    let alive = true;
    Promise.allSettled([fetchPublicAccessConfig(server.id), fetchPlayitAgent()])
      .then(async ([configResult, agentResult]) => {
        if (!alive) return;
        const hadStoredConfig = typeof window !== "undefined" && Boolean(window.localStorage.getItem(publicAccessStorageKey(server.id)));
        let nextConfig = { ...readStoredConfig(server.id) };
        if (configResult.status === "fulfilled" && configResult.value.config) {
          nextConfig = { ...nextConfig, ...dbRecordToConfig(configResult.value.config) };
          if (!hadStoredConfig && nextConfig.publicAddress) nextConfig.enabled = true;
        } else if (nextConfig.publicAddress) {
          try {
            const saved = await savePublicAccessConfig(server.id, {
              provider: "Playit",
              publicAddress: nextConfig.publicAddress,
              localHost: "localhost",
              localPort: server.port,
              agentPath: nextConfig.agentPath,
              claimed: nextConfig.claimed,
            });
            if (alive) nextConfig = { ...nextConfig, ...dbRecordToConfig(saved.config) };
          } catch {
            // Local storage migration is best-effort. The user can still re-save the address.
          }
        }
        if (!alive) return;
        setConfig(nextConfig);
        window.localStorage.setItem(publicAccessStorageKey(server.id), JSON.stringify(nextConfig));
        window.dispatchEvent(new CustomEvent("cliff:public-access-config", {
          detail: { serverId: server.id, configured: Boolean(nextConfig.publicAddress), enabled: nextConfig.enabled !== false },
        }));
        setTunnelState(nextConfig.publicAddress ? "tunnel-ready" : "no-tunnel-found");
        if (nextConfig.publicAddress) setSetupOpen(false);
        if (agentResult.status === "fulfilled") applyAgent(agentResult.value, nextConfig);
        if (configResult.status === "rejected") onMessage(configResult.reason instanceof Error ? configResult.reason.message : "Public access config failed");
        if (agentResult.status === "rejected") onMessage(agentResult.reason instanceof Error ? agentResult.reason.message : "Playit agent status failed");
      })
      .finally(() => {
        if (alive) setLoadingConfig(false);
      });
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [server.id]);

  useEffect(() => {
    if (!setupActive || !agentInstalled || config.claimed) return;
    let alive = true;
    let timer: number | null = null;
    startAgent().then(() => {
      if (!alive) return;
      timer = window.setInterval(() => {
        fetchPlayitAgent()
          .then((agent) => { if (alive) applyAgent(agent); })
          .catch(() => undefined);
      }, AGENT_POLL_INTERVAL_MS);
    });
    return () => {
      alive = false;
      if (timer !== null) window.clearInterval(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [setupActive, agentInstalled, config.claimed, server.id]);

  // Poll agent logs when configured and agent is running (for the read-only console display)
  useEffect(() => {
    if (!configured) return;
    let alive = true;
    const poll = () => {
      fetchPlayitAgent()
        .then((agent) => {
          if (!alive) return;
          setAgentRunning(Boolean(agent.running));
          setAgentLogs(agent.logs ?? []);
          // Detect tunnel ready from logs
          const ready = (agent.logs ?? []).some((line) =>
            line.includes("tunnel running") ||
            line.includes("tunnels registered"));
          setTunnelReady(Boolean(agent.running) && ready);
        })
        .catch(() => undefined);
    };
    poll();
    const timer = window.setInterval(poll, 2000);
    return () => {
      alive = false;
      window.clearInterval(timer);
    };
  }, [configured]);

  function persistLocal(next: PublicAccessConfig) {
    setConfig(next);
    window.localStorage.setItem(publicAccessStorageKey(server.id), JSON.stringify(next));
    window.dispatchEvent(new CustomEvent("cliff:public-access-config", {
      detail: { serverId: server.id, configured: Boolean(next.publicAddress), enabled: next.enabled !== false },
    }));
  }

  // Dispatch loading state to sidebar for flashing dot
  useEffect(() => {
    window.dispatchEvent(new CustomEvent("cliff:public-access-loading", {
      detail: { serverId: server.id, loading: publicAccessLoading },
    }));
  }, [publicAccessLoading, server.id]);

  function save(next: Partial<PublicAccessConfig>) {
    const merged = { ...config, ...next, updatedAt: new Date().toISOString() };
    persistLocal(merged);
    return merged;
  }

  function applyAgent(agent: PlayitAgentInfo, baseConfig = config) {
    setInstallState(agent.installed ? "installed" : "not-installed");
    setAgentError(agent.error ?? "");
    setAgentRunning(Boolean(agent.running));
    if (!agent.installed) return;
    const claimUrl = agent.claimUrl || (agent.running || agent.claiming ? "" : baseConfig.claimUrl);
    let nextConfig = { ...baseConfig, installed: true, agentPath: agent.path, claimUrl, updatedAt: new Date().toISOString() };
    const approvedByManager = agent.running && (agent.logs ?? []).some((line) =>
      line.includes("Playit account approved")
      || line.includes("Program approved")
      || line.includes("starting up tunnel connection")
      || line.includes("tunnel running"));
    if (baseConfig.claimed || approvedByManager) {
      nextConfig = { ...nextConfig, claimed: true };
      setClaimState("claimed");
      if (!baseConfig.claimed) {
        setTunnelState("no-tunnel-found");
        onMessage("Playit account connected");
      }
    } else if (agent.claiming && claimUrl) {
      setClaimState("waiting-for-user-to-claim");
    } else if (agent.claimUrl) {
      setClaimState("claim-link-ready");
    } else if (agent.running) {
      setClaimState("waiting-for-claim-link");
    }
    persistLocal(nextConfig);
  }

  async function copy(value: string, label: string) {
    try {
      await navigator.clipboard.writeText(value);
      onMessage(`${label} copied`);
    } catch {
      onMessage("Clipboard copy failed");
    }
  }

  function open(url: string) {
    window.open(url, "_blank", "noopener,noreferrer");
  }

  async function installAgent() {
    setInstallState("installing");
    try {
      const agent = await installManagedPlayitAgent();
      applyAgent(agent);
      onMessage(`Playit agent installed${agent.version ? ` (${agent.version})` : ""}`);
      await startAgent();
    } catch (error) {
      setInstallState("failed");
      onMessage(error instanceof Error ? error.message : "Playit agent install failed");
    }
  }

  async function uninstallAgent() {
    if (uninstallBusy) return;
    setUninstallBusy(true);
    try {
      const agent = await uninstallManagedPlayitAgent();
      await deletePublicAccessConfig(server.id).catch(() => undefined);
      const nextConfig = { ...emptyConfig, installed: agent.installed, agentPath: agent.path, updatedAt: new Date().toISOString() };
      persistLocal(nextConfig);
      setInstallState(agent.installed ? "installed" : "not-installed");
      setClaimState("waiting-for-claim-link");
      setTunnelState("no-tunnel-found");
      setAgentError(agent.error ?? "");
      goToSetupStep(agent.installed ? 1 : 0);
      onMessage(agent.installed ? "Playit agent is still installed" : "Playit agent uninstalled");
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Playit agent could not be uninstalled");
    } finally {
      setUninstallBusy(false);
    }
  }

  async function startAgent(baseConfig: PublicAccessConfig = config) {
    if (!baseConfig.claimed && claimState !== "claim-link-ready") setClaimState("starting-agent");
    try {
      const agent = await startManagedPlayitAgent();
      applyAgent(agent, baseConfig);
      if (!agent.claimUrl && !agent.claiming && !baseConfig.claimed) setClaimState("waiting-for-claim-link");
    } catch (error) {
      setClaimState("failed");
      onMessage(error instanceof Error ? error.message : "Playit agent could not start");
    }
  }

  async function savePublicAddress(value: string, isAutoDetected = false, autoAdvance = false) {
    const address = normalizeTunnelAddress(value);
    if (!validJoinAddress(address)) {
      setTunnelState("failed");
      onMessage(isAutoDetected ? "Could not detect a public Playit address automatically." : "Enter the public Playit join address.");
      return;
    }
    try {
      const saved = await savePublicAccessConfig(server.id, {
        provider: "Playit",
        publicAddress: address,
        localHost: "localhost",
        localPort: server.port,
        agentPath: config.agentPath,
        claimed: config.claimed,
      });
      const merged = save({ ...dbRecordToConfig(saved.config), publicAddress: address, enabled: true });
      setTunnelState("tunnel-ready");
      if (autoAdvance) goToSetupStep(3);
      if (mode !== "setup") setSetupOpen(false);
      if (!isAutoDetected) onMessage(`Public access configured for ${merged.publicAddress}`);
    } catch (error) {
      setTunnelState("failed");
      onMessage(error instanceof Error ? error.message : "Public access could not be saved");
    }
  }

  async function detectTunnelAddress() {
    if (config.publicAddress) return;
    setTunnelState("waiting-for-tunnel");
    try {
      const agent = await fetchPlayitAgent();
      if (agent.installed) setAgentError(agent.error ?? "");
      const detectedAddress = detectTunnelAddressFromAgent(agent, localTarget, server.port);
      if (detectedAddress) {
        setTunnelState("tunnel-detected");
        await savePublicAddress(detectedAddress, true, false);
      }
      if (!detectedAddress) {
        setTunnelState("failed");
      }
    } catch {
      if (!config.publicAddress) setTunnelState("failed");
    }
  }

  async function checkTunnelAddressAgain() {
    await detectTunnelAddress();
  }

  useEffect(() => {
    if (!setupActive || !agentInstalled || !config.claimed || setupStep !== 2 || config.publicAddress) return;
    let alive = true;
    let timer: number | null = null;
    let checking = false;

    const runCheck = async () => {
      if (!alive || checking) return;
      checking = true;
      try {
        const agent = await fetchPlayitAgent();
        if (!alive) return;
        if (agent.installed) setAgentError(agent.error ?? "");
        const detectedAddress = detectTunnelAddressFromAgent(agent, localTarget, server.port);
        if (detectedAddress) {
          setTunnelState("tunnel-detected");
          await savePublicAddress(detectedAddress, true, false);
        } else if (alive) {
          setTunnelState("failed");
        }
      } catch {
        if (alive) setTunnelState("failed");
      } finally {
        checking = false;
      }
    };

    runCheck();
    timer = window.setInterval(() => {
      void runCheck();
    }, AGENT_POLL_INTERVAL_MS);

    return () => {
      alive = false;
      if (timer !== null) window.clearInterval(timer);
    };
    // Keep this poll tied to setup state only; render-created callbacks would restart it and cause extra immediate requests.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [setupActive, agentInstalled, config.claimed, setupStep, config.publicAddress, localTarget]);

  async function stopPublicAccess() {
    if (publicAccessBusy) return;
    setPublicAccessBusy(true);
    try {
      const agent = await stopManagedPlayitAgent();
      applyAgent(agent, { ...config, enabled: false });
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Public access could not be stopped");
      setPublicAccessBusy(false);
      return;
    }
    save({ enabled: false });
    onMessage("Public access stopped");
    setPublicAccessBusy(false);
  }

  async function startPublicAccess() {
    if (publicAccessBusy) return;
    setPublicAccessBusy(true);
    try {
      const agent = await startManagedPlayitAgent();
      applyAgent(agent, { ...config, enabled: true });
      save({ enabled: true });
      onMessage("Public access started");
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Public access could not start");
    } finally {
      setPublicAccessBusy(false);
    }
  }

  async function resetProcess() {
    if (resetBusy) return;
    setResetBusy(true);
    try {
      const agent = await resetManagedPlayitAgent();
      await deletePublicAccessConfig(server.id).catch(() => undefined);
      const nextConfig = { ...emptyConfig, installed: agent.installed, agentPath: agent.path, updatedAt: new Date().toISOString() };
      persistLocal(nextConfig);
      applyAgent(agent, nextConfig);
      setClaimState("waiting-for-claim-link");
      setInstallState(agent.installed ? "installed" : "not-installed");
      setTunnelState("no-tunnel-found");
      setAgentError(agent.error ?? "");
      goToSetupStep(agent.installed ? 1 : 0);
      setSetupOpen(true);
      if (agent.installed) {
        await startAgent(nextConfig);
      }
      onMessage("Playit setup reset");
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Playit reset failed");
    } finally {
      setResetBusy(false);
    }
  }

  if (setupActive) {
    return (
      <section className="public-access-setup-view">
        <Tabs
          ariaLabel="Public access setup steps"
          items={flowSteps.map((label, index) => ({
            id: String(index),
            label: `${index + 1}. ${label}`,
            disabled: index > 0 && !setupStepValid[index - 1],
            extraClassName: setupStepValid[index] && index !== setupStep ? "done" : "",
          }))}
          activeId={String(setupStep)}
          onChange={(id) => goToSetupStep(Number(id))}
        />

        {setupStep === 0 && (
          <div className="form-section">
              {!agentInstalled ? (
                <div className="public-access-flow-hero public-access-install-hero">
                  <Image src="/assets/logos/playit.png" alt="Playit" width={256} height={256} priority />
                  <div className="public-access-install-copy">
                    <p>Let your friends join New Fabric Server without router setup or port forwarding. Install the Playit agent once, then generate your public address in the next steps. This runs a background process that creates and manages your Playit tunnel. You can start, stop, and uninstall it whenever you want.</p>
                  </div>
                </div>
              ) : null}
              {agentInstalled ? (
                <div className="public-access-complete-block">
                  <CheckCircle2 size={72} />
                  <h2>Installation complete</h2>
                  <p>The Playit agent is installed on this machine and ready to connect to your account.</p>
                </div>
              ) : null}
              <div className="public-access-flow-actions public-access-install-flow-actions">
                {agentInstalled ? (
                  <>
                    <Button className="public-access-install-button" disabled={uninstallBusy} onClick={uninstallAgent} loading={uninstallBusy} loadingText="Uninstalling...">Uninstall Agent</Button>
                    <Button variant="primary" className="public-access-install-button" onClick={() => goToSetupStep(1)}>Next</Button>
                  </>
                ) : (
                  <Button variant="primary" className="public-access-install-button" disabled={installState === "installing"} onClick={installAgent} loading={installState === "installing"} loadingText="Installing...">Install Playit Agent</Button>
                )}
              </div>
          </div>
        )}

        {setupStep === 1 && (
          <div className="form-section">
              {config.claimed ? (
                <>
                  <div className="public-access-complete-block">
                    <CheckCircle2 size={72} />
                    <h2>Agent connected</h2>
                    <p>Your Playit agent is claimed and ready to create a public tunnel.</p>
                  </div>
                  <div className="public-access-flow-actions public-access-install-flow-actions">
                    <Button className="public-access-install-button" disabled={resetBusy} onClick={resetProcess} loading={resetBusy} loadingText="Resetting...">Reset setup</Button>
                    <Button variant="primary" className="public-access-install-button" onClick={() => goToSetupStep(2)}>Next</Button>
                  </div>
                </>
              ) : (
                <>
                  <div className="public-access-flow-hero public-access-install-hero">
                    <Image src="/assets/logos/playit.png" alt="Playit" width={220} height={220} />
                    <div className="public-access-install-copy">
                      <p>Open the claim link, sign in or create a Playit account, then approve this device to connect the local agent.</p>
                    </div>
                  </div>
                  <div className="public-access-url-box">
                    <Input className="public-access-claim-url-input" value={config.claimUrl} placeholder="Waiting for Playit claim link..." readOnly />
                    <Button
                      className="public-access-inline-icon-button"
                      disabled={!config.claimUrl}
                      onClick={() => copy(config.claimUrl, "Claim URL")}
                      aria-label="Copy claim link"
                      title="Copy claim link"
                    >
                      <Clipboard size={18} />
                    </Button>
                  </div>
                  <div className="public-access-flow-actions public-access-install-flow-actions">
                    <Button variant="primary" className="public-access-install-button" disabled={!validClaimUrl(config.claimUrl) || config.claimed} onClick={() => { setClaimState("waiting-for-user-to-claim"); open(config.claimUrl); }}>
                      Open Claim Link
                    </Button>
                    <Button className="public-access-install-button" disabled={!agentInstalled || resetBusy} onClick={resetProcess} loading={resetBusy} loadingText="Resetting...">Reset setup</Button>
                  </div>
                  {agentError ? <Hint warn>{agentError}</Hint> : null}
                </>
              )}
          </div>
        )}

        {setupStep === 2 && (
          <div className="form-section">
              {config.publicAddress ? (
                <>
                  <div className="public-access-complete-block">
                    <CheckCircle2 size={72} />
                    <h2>Tunnel detected</h2>
                    <p>Cliff found your Playit tunnel and captured the public address.</p>
                  </div>
                  <div className="public-access-url-box">
                    <Input className="public-access-claim-url-input" value={config.publicAddress} readOnly />
                  </div>
                  <div className="public-access-flow-actions public-access-install-flow-actions">
                    <Button className="public-access-install-button" onClick={() => open(playitLinks.tunnelSetup)}>
                      Open Playit Tunnel Setup
                    </Button>
                    <Button variant="primary" className="public-access-install-button" onClick={() => (mode === "setup" ? onBack?.() : setSetupOpen(false))}>
                      Done
                    </Button>
                  </div>
                </>
              ) : (
                <>
                  <div className="public-access-flow-hero public-access-install-hero">
                    <Image src="/assets/logos/playit.png" alt="Playit" width={220} height={220} />
                    <div className="public-access-tunnel-steps" aria-label="Tunnel setup instructions">
                      <div><span>1</span><p>Open the Playit tunnel setup page and sign in.</p></div>
                      <div><span>2</span><p>Create a Minecraft Java tunnel for this server and set Local Host + Local Port to <strong>{localTarget}</strong>.</p></div>
                      <div><span>3</span><p>Start the tunnel and return here. Cliff will detect the public address automatically.</p></div>
                    </div>
                  </div>
                  <div className="public-access-url-box">
                    <Input className="public-access-claim-url-input" value={playitLinks.tunnelSetup} readOnly />
                    <Button
                      className="public-access-inline-icon-button"
                      onClick={() => copy(playitLinks.tunnelSetup, "Playit tunnel setup link")}
                      aria-label="Copy Playit tunnel setup link"
                      title="Copy Playit tunnel setup link"
                    >
                      <Clipboard size={18} />
                    </Button>
                  </div>
                  <div className="public-access-flow-actions public-access-install-flow-actions">
                    <Button className="public-access-install-button" disabled={!config.claimed} onClick={() => open(playitLinks.tunnelSetup)}>
                      Open Playit Tunnel Setup
                    </Button>
                    {tunnelState === "failed" || tunnelState === "waiting-for-tunnel" ? (
                      <Button variant="primary" className="public-access-install-button" disabled={tunnelState === "waiting-for-tunnel"} onClick={checkTunnelAddressAgain}>Check Again</Button>
                    ) : null}
                  </div>
                  {tunnelState === "failed" ? (
                    <Hint warn>
                      We couldn&apos;t detect the tunnel yet. Make sure your Minecraft Java tunnel points to <strong>{localTarget}</strong> and is running, then click Check Again.
                    </Hint>
                  ) : <Hint>We&apos;ll auto-detect the tunnel as soon as it appears.</Hint>}
                </>
              )}
          </div>
        )}

      </section>
    );
  }

  return (
    <Panel className="public-access-panel public-access-layout" title="Public Access" description="Expose this server to the internet so friends can join remotely." icon={<RadioTower />} headerActions={
      <Toolbar>
        <Button variant="primary" onClick={() => { if (onConfigure) onConfigure(); else { setSetupOpen(true); goToSetupStep(config.claimed ? 2 : agentInstalled ? 1 : 0); } }}>
          {configured ? "Manage setup" : "Configure Public Access"}
        </Button>
      </Toolbar>
    }>
      {loadingConfig ? (
        <div className="public-access-skeleton" aria-hidden="true">
          <div className="public-access-skeleton-row">
            <div className="skeleton skeleton-line short" />
            <div className="skeleton skeleton-line medium" />
          </div>
          <div className="public-access-skeleton-toggle">
            <div className="skeleton skeleton-line wide" />
            <div className="skeleton skeleton-toggle" />
          </div>
          <div className="public-access-skeleton-stats">
            <div className="skeleton skeleton-stat" />
            <div className="skeleton skeleton-stat" />
          </div>
        </div>
      ) : configured ? (
        <>
          {config.publicAddress ? (
            <JoinAddress
              label="Public join address"
              address={config.publicAddress}
              onCopy={() => copy(config.publicAddress, "Link")}
            />
          ) : null}
          <div className={`public-access-toggle-card ${publicAccessRunning ? "on" : ""} ${publicAccessLoading ? "loading" : ""}`}>
            <div className="public-access-toggle-copy">
              <strong>{publicAccessLoading ? "Public access is starting..." : publicAccessRunning ? "Public access is on" : "Public access is off"}</strong>
              <span>{publicAccessLoading ? "Waiting for tunnel connection to be established." : publicAccessRunning ? "Your friends can join using the link above." : "Turn it on to generate a shareable join link your friends can use."}</span>
            </div>
            <Toggle
              checked={publicAccessRunning || publicAccessLoading}
              disabled={publicAccessBusy || publicAccessLoading}
              onChange={(next) => { if (next) startPublicAccess(); else stopPublicAccess(); }}
              aria-label="Toggle public access"
            />
          </div>
          <div className="public-access-provider-row">
            <span className="public-access-provider-label">Provider</span>
            <Image src="/assets/logos/playit.png" alt="Playit" width={210} height={40} />
          </div>
          {configured && agentRunning ? (
            <ConsoleView
              lines={[]}
              parsedLines={agentLogs.slice(-100).map((line, i) => {
                const f = formatPlayitLog(line);
                return { key: `pa-${i}-${line.slice(0, 24)}`, time: f.time, text: f.text, level: f.level };
              })}
              emptyMessage="Starting agent..."
              className="public-access-console"
            />
          ) : null}
        </>
      ) : (
        <div className="public-access-empty-state">
          <div className="public-access-empty-copy">
            <h3>Your server is currently not configured for public access.</h3>
            <p>Public Access uses Playit to create a secure tunnel, allowing your friends to join this server from anywhere without requiring you to configure router port forwarding.</p>
          </div>
        </div>
      )}
    </Panel>
  );
}
