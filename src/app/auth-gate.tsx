"use client";

import { useEffect, useState } from "react";
import AuthForm from "./auth-form";
import DashboardClient from "./dashboard-client";
import { externalApiUrl } from "./dashboard/lib/utils";
import type { User } from "./dashboard/lib/types";

const serverTabs = new Set(["overview", "console", "mods", "mods/installed", "mods/discover", "worlds", "players", "backups", "files", "public-access", "public-access/setup", "settings"]);
const utilityTabs = new Set(["app", "account", "import", "create"]);

type AuthState = {
  loading: boolean;
  user: User | null;
  needsSetup: boolean;
  error: string;
};

export default function AuthGate({ initialServerId = "", initialTab = "overview" }: { initialServerId?: string; initialTab?: string }) {
  const [state, setState] = useState<AuthState>({ loading: true, user: null, needsSetup: false, error: "" });
  const [routeState, setRouteState] = useState({ serverId: initialServerId, tab: initialTab });

  async function loadAuth(): Promise<AuthState> {
    try {
      const response = await fetch(externalApiUrl("/api/auth/me"), { credentials: "include" });
      const data = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(data.error || "Authentication check failed");
      return { loading: false, user: data.user ?? null, needsSetup: Boolean(data.needsSetup), error: "" };
    } catch (error) {
      return {
        loading: false,
        user: null,
        needsSetup: false,
        error: error instanceof Error ? error.message : "Authentication check failed",
      };
    }
  }

  useEffect(() => {
    let alive = true;
    loadAuth().then((nextState) => {
      if (alive) setState(nextState);
    });
    return () => {
      alive = false;
    };
  }, []);
  useEffect(() => {
    const timer = window.setTimeout(() => setRouteState(readRouteState(initialServerId, initialTab)), 0);
    const syncRouteState = () => setRouteState(readRouteState(initialServerId, initialTab));
    window.addEventListener("popstate", syncRouteState);
    return () => {
      window.clearTimeout(timer);
      window.removeEventListener("popstate", syncRouteState);
    };
  }, [initialServerId, initialTab]);

  if (state.loading) {
    return (
      <main className="center-panel">
        <section className="auth-card">
          <h1>Loading dashboard</h1>
          <p>Checking your local session.</p>
        </section>
      </main>
    );
  }

  if (!state.user) {
    return (
      <AuthForm
        needsSetup={state.needsSetup}
        initialError={state.error}
        onAuthenticated={(user) => setState({ loading: false, user, needsSetup: false, error: "" })}
      />
    );
  }

  return <DashboardClient key={`${routeState.serverId}:${routeState.tab}`} user={state.user} initialServerId={routeState.serverId} initialTab={routeState.tab} />;
}

function readRouteState(fallbackServerId: string, fallbackTab: string) {
  if (typeof window === "undefined") return { serverId: fallbackServerId, tab: fallbackTab };
  const parts = window.location.pathname.split("/").filter(Boolean).map(decodeURIComponent);
  if (parts[0] === "servers" && parts[1]) {
    if (parts[2] === "mods") {
      const subview = parts[3] === "discover" ? "discover" : "installed";
      return { serverId: parts[1], tab: `mods/${subview}` };
    }
    if (parts[2] === "public-access" && parts[3] === "setup") {
      return { serverId: parts[1], tab: "public-access/setup" };
    }
    const tab = parts[2] && serverTabs.has(parts[2]) ? parts[2] : "overview";
    return { serverId: parts[1], tab };
  }
  if (parts[0] === "app-settings") return { serverId: fallbackServerId, tab: "app" };
  if (parts[0] === "account") return { serverId: fallbackServerId, tab: "account" };
  if (parts[0] && utilityTabs.has(parts[0])) return { serverId: fallbackServerId, tab: parts[0] };
  return { serverId: fallbackServerId, tab: fallbackTab };
}
