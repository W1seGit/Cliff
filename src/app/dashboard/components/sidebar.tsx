"use client";

import { type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import {
  Archive, ChevronDown, ChevronLeft, ChevronRight,
  FolderOpen, Globe, LayoutDashboard, LogOut, MoreHorizontal,
  Package, Plus, Puzzle, RadioTower, Search, Settings, Terminal, Upload, UserRound, Users,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";

import { api, externalApiUrl, serverTypeSupportsContent, publicAccessStorageKey } from "../lib/utils";
import type { RuntimeStatus, ServerRecord, User } from "../lib/types";
import { fetchPlayitAgent } from "../lib/runtime-client";
import { ServerAvatar } from "./server-avatar";

type NavItem = { id: string; label: string; Icon: LucideIcon; requiresContent?: boolean };
type ModsChildItem = { id: "mods/installed" | "mods/discover"; label: string; Icon: LucideIcon };
type FloatingMenuProps = {
  children: ReactNode;
  className: string;
  position: { top: number; left: number };
  width?: number;
};

const serverNavItems: NavItem[] = [
  { id: "overview", label: "Overview", Icon: LayoutDashboard },
  { id: "console", label: "Console", Icon: Terminal },
  { id: "files", label: "Files", Icon: FolderOpen },
  { id: "mods", label: "Mods / Plugins", Icon: Puzzle, requiresContent: true },
  { id: "worlds", label: "Worlds", Icon: Globe },
  { id: "backups", label: "Backups", Icon: Archive },
  { id: "players", label: "Players", Icon: Users },
  { id: "public-access", label: "Public Access", Icon: RadioTower },
  { id: "settings", label: "Settings", Icon: Settings },
];

const utilityNavItems: NavItem[] = [
  { id: "import", label: "Import server", Icon: Upload },
  { id: "create", label: "Create server", Icon: Plus },
];

const modsChildItems: ModsChildItem[] = [
  { id: "mods/installed", label: "Installed", Icon: Package },
  { id: "mods/discover", label: "Discover", Icon: Search },
];

function serverIsRunning(runtime: RuntimeStatus, serverId: string) {
  return runtime.servers?.[serverId]?.runningServerId === serverId || runtime.runningServerId === serverId;
}

function FloatingMenu({ children, className, position, width }: FloatingMenuProps) {
  if (typeof document === "undefined") return null;
  return createPortal(
    <div
      className={className}
      role="menu"
      style={{
        position: "fixed",
        top: position.top,
        left: position.left,
        width,
        right: "auto",
        bottom: "auto",
      }}
    >
      {children}
    </div>,
    document.body,
  );
}

export function Sidebar({
  user,
  servers,
  runtime,
  selected,
  showSelectedServer,
  collapsed,
  setCollapsed,
  serverActionMenu,
  setServerActionMenu,
  setSelectedId,
  setTab,
  tab,
  consoleAttention,
  onRename,
  onDuplicate,
  onDelete,
  loading = false,
}: {
  user: User;
  servers: ServerRecord[];
  runtime: RuntimeStatus;
  selected?: ServerRecord;
  showSelectedServer: boolean;
  collapsed: boolean;
  setCollapsed: (value: boolean) => void;
  serverActionMenu: string;
  setServerActionMenu: (value: string) => void;
  setSelectedId: (id: string) => void;
  setTab: (tab: string) => void;
  tab: string;
  consoleAttention: boolean;
  onRename: (server: ServerRecord) => void;
  onDuplicate: (server: ServerRecord) => void;
  onDelete: (server: ServerRecord) => void;
  loading?: boolean;
}) {
  const [menuPosition, setMenuPosition] = useState({ top: 0, left: 0 });
  const [switcherOpen, setSwitcherOpen] = useState(false);
  const [switcherPosition, setSwitcherPosition] = useState({ top: 0, left: 0 });
  const [switcherQuery, setSwitcherQuery] = useState("");
  const [accountOpen, setAccountOpen] = useState(false);
  const [accountPosition, setAccountPosition] = useState({ top: 0, left: 0 });
  const [modsOpen, setModsOpen] = useState<boolean | null>(null);
  const [publicAccessStatus, setPublicAccessStatus] = useState<"off" | "loading" | "on">("off");
  const switcherRef = useRef<HTMLButtonElement | null>(null);
  const accountRef = useRef<HTMLButtonElement | null>(null);
  const switcherMenuWidth = 290;
  const accountMenuWidth = 220;
  const closeMobileSidebar = () => {
    if (typeof window !== "undefined" && window.matchMedia("(max-width: 900px)").matches) setCollapsed(true);
  };
  async function logout() {
    await api(externalApiUrl("/api/auth/logout"), { method: "POST" }).catch(() => undefined);
    window.location.replace("/");
  }
  const sidebarServers = useMemo(() => servers.toSorted((a, b) => {
    if (serverIsRunning(runtime, a.id) && !serverIsRunning(runtime, b.id)) return -1;
    if (serverIsRunning(runtime, b.id) && !serverIsRunning(runtime, a.id)) return 1;
    return a.name.localeCompare(b.name);
  }), [servers, runtime]);
  const filteredServers = useMemo(() => {
    const q = switcherQuery.trim().toLowerCase();
    if (!q) return sidebarServers;
    return sidebarServers.filter((server) => server.name.toLowerCase().includes(q) || server.type.toLowerCase().includes(q));
  }, [sidebarServers, switcherQuery]);
  const openMenuServer = sidebarServers.find((server) => server.id === serverActionMenu);

  function openServerMenu(serverId: string, button: HTMLButtonElement) {
    const rect = button.getBoundingClientRect();
    const width = 150;
    const menuHeight = 128;
    const left = Math.max(8, Math.min(rect.right - width, window.innerWidth - width - 8));
    const preferredTop = rect.bottom + 6;
    const top = Math.max(8, Math.min(preferredTop, window.innerHeight - menuHeight - 8));
    setAccountOpen(false);
    setMenuPosition({ top, left });
    setServerActionMenu(serverActionMenu === serverId ? "" : serverId);
  }

  function openSwitcher() {
    const button = switcherRef.current;
    if (!button || typeof window === "undefined") {
      setSwitcherOpen((open) => !open);
      return;
    }
    if (switcherOpen) {
      setSwitcherOpen(false);
      return;
    }
    const rect = button.getBoundingClientRect();
    const menuHeight = Math.min(380, window.innerHeight - rect.bottom - 16);
    const left = Math.max(8, Math.min(rect.left, window.innerWidth - switcherMenuWidth - 8));
    const top = Math.max(8, Math.min(rect.bottom + 6, window.innerHeight - menuHeight - 8));
    setAccountOpen(false);
    setServerActionMenu("");
    setSwitcherQuery("");
    setSwitcherPosition({ top, left });
    setSwitcherOpen(true);
  }

  function openAccount() {
    const button = accountRef.current;
    if (!button || typeof window === "undefined") {
      setAccountOpen((open) => !open);
      return;
    }
    if (accountOpen) {
      setAccountOpen(false);
      return;
    }
    const rect = button.getBoundingClientRect();
    const menuHeight = 124;
    const abovePreferred = rect.top - menuHeight - 12;
    const top = Math.max(8, Math.min(abovePreferred, window.innerHeight - menuHeight - 8));
    const left = Math.max(8, Math.min(rect.left, window.innerWidth - accountMenuWidth - 8));
    setSwitcherOpen(false);
    setServerActionMenu("");
    setAccountPosition({ top, left });
    setAccountOpen(true);
  }

  useEffect(() => {
    if (!serverActionMenu && !switcherOpen) return;
    function closeOnOutside(event: PointerEvent) {
      const target = event.target as HTMLElement | null;
      if (target?.closest(".server-menu, .server-menu-button, .server-switcher, .server-switcher-menu, .account-button, .account-menu")) return;
      setServerActionMenu("");
      setSwitcherOpen(false);
    }
    function closeOnEscape(event: KeyboardEvent) {
      if (event.key === "Escape") {
        setServerActionMenu("");
        setSwitcherOpen(false);
      }
    }
    function closeOnViewportChange() {
      setServerActionMenu("");
      setSwitcherOpen(false);
    }
    document.addEventListener("pointerdown", closeOnOutside);
    document.addEventListener("keydown", closeOnEscape);
    window.addEventListener("resize", closeOnViewportChange);
    window.addEventListener("scroll", closeOnViewportChange, true);
    return () => {
      document.removeEventListener("pointerdown", closeOnOutside);
      document.removeEventListener("keydown", closeOnEscape);
      window.removeEventListener("resize", closeOnViewportChange);
      window.removeEventListener("scroll", closeOnViewportChange, true);
    };
  }, [serverActionMenu, switcherOpen, setServerActionMenu]);

  useEffect(() => {
    if (!accountOpen) return;
    function closeOnOutside(event: PointerEvent) {
      const target = event.target as HTMLElement | null;
      if (target?.closest(".account-menu, .account-button")) return;
      setAccountOpen(false);
    }
    function closeOnEscape(event: KeyboardEvent) {
      if (event.key === "Escape") setAccountOpen(false);
    }
    function closeOnViewportChange() {
      setAccountOpen(false);
    }
    document.addEventListener("pointerdown", closeOnOutside);
    document.addEventListener("keydown", closeOnEscape);
    window.addEventListener("resize", closeOnViewportChange);
    window.addEventListener("scroll", closeOnViewportChange, true);
    return () => {
      document.removeEventListener("pointerdown", closeOnOutside);
      document.removeEventListener("keydown", closeOnEscape);
      window.removeEventListener("resize", closeOnViewportChange);
      window.removeEventListener("scroll", closeOnViewportChange, true);
    };
  }, [accountOpen]);

  const isUtilityTab = tab === "app" || tab === "account" || tab === "import" || tab === "create";
  const navItems = showSelectedServer && selected ? serverNavItems : [];
  const utilityActiveItem = utilityNavItems.find((item) => item.id === tab);
  const modsActive = tab === "mods" || tab === "mods/installed" || tab === "mods/discover";
  const modsExpanded = modsOpen ?? modsActive;

  useEffect(() => {
    if (!selected?.id) return;
    const selectedId: string = selected.id;
    let cancelled = false;

    const verifyAgentStatus = () => {
      fetchPlayitAgent()
        .then((agent) => {
          if (!cancelled) setPublicAccessStatus(agent.running ? "on" : "off");
        })
        .catch(() => {
          if (!cancelled) setPublicAccessStatus("off");
        });
    };

    Promise.resolve().then(() => { if (!cancelled) setPublicAccessStatus("off"); });
    verifyAgentStatus();

    function updateFromStorage(event?: Event) {
      if (event instanceof StorageEvent && event.key && event.key !== publicAccessStorageKey(selectedId)) return;
      if (event instanceof CustomEvent && event.detail?.serverId && event.detail.serverId !== selectedId) return;
      verifyAgentStatus();
    }
    window.addEventListener("storage", updateFromStorage);
    window.addEventListener("cliff:public-access-config", updateFromStorage);

    function updateLoading(e: Event) {
      if (e instanceof CustomEvent && e.detail?.serverId && e.detail.serverId !== selectedId) return;
      const loading = Boolean(e instanceof CustomEvent && e.detail?.loading);
      if (loading) {
        setPublicAccessStatus("loading");
      } else {
        verifyAgentStatus();
      }
    }
    window.addEventListener("cliff:public-access-loading", updateLoading);

    return () => {
      cancelled = true;
      window.removeEventListener("storage", updateFromStorage);
      window.removeEventListener("cliff:public-access-config", updateFromStorage);
      window.removeEventListener("cliff:public-access-loading", updateLoading);
    };
  }, [selected?.id]);

  function handleNavClick(item: NavItem) {
    if (item.id === "mods") {
      setModsOpen(!modsExpanded);
      setSwitcherOpen(false);
      return;
    }
    setTab(item.id === "mods" ? "mods/installed" : item.id);
    setSwitcherOpen(false);
    closeMobileSidebar();
  }

  function handleModsChildClick(id: ModsChildItem["id"]) {
    setModsOpen(true);
    setTab(id);
    setSwitcherOpen(false);
    closeMobileSidebar();
  }

  function handleServerPick(server: ServerRecord) {
    setSelectedId(server.id);
    setSwitcherOpen(false);
    setServerActionMenu("");
    closeMobileSidebar();
  }

  function handleUtilityPick(id: string) {
    setTab(id);
    setSwitcherOpen(false);
    closeMobileSidebar();
  }

  return (
    <>
    <aside className={`sidebar ${collapsed ? "collapsed" : ""}`}>
      <div className="sidebar-top">
        <span className="sidebar-brand" aria-label="Cliff">
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src="/assets/cliff-logo.svg" alt="" />
        </span>
        <button
          className="sidebar-toggle"
          aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
          aria-expanded={!collapsed}
          onClick={() => setCollapsed(!collapsed)}
          title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
        >
          {collapsed ? <ChevronRight size={14} /> : <ChevronLeft size={14} />}
        </button>
      </div>

      <div className="server-switcher-wrap">
        <button
          ref={switcherRef}
          className="server-switcher"
          aria-haspopup="menu"
          aria-expanded={switcherOpen}
          onClick={() => openSwitcher()}
          title={collapsed && selected ? selected.name : undefined}
        >
          {selected ? (
            <>
              <ServerAvatar server={selected} className="server-switcher-initials" />
              <span className="server-switcher-meta">
                <span className="server-switcher-name">{selected.name}</span>
              </span>
              <ChevronDown size={14} className="server-switcher-caret" aria-hidden="true" />
            </>
          ) : loading ? (
            <>
              <span className="skeleton skeleton-dot" />
              <span className="server-switcher-meta">
                <span className="skeleton skeleton-line wide" />
                <span className="skeleton skeleton-line short" />
              </span>
              <ChevronDown size={14} className="server-switcher-caret" aria-hidden="true" />
            </>
          ) : (
            <>
              <span className="server-switcher-status placeholder" aria-hidden="true" />
              <span className="server-switcher-meta">
                <span className="server-switcher-name">No server</span>
                <span className="server-switcher-sub"><span className="server-switcher-detail">Pick or create a server</span></span>
              </span>
              <ChevronDown size={14} className="server-switcher-caret" aria-hidden="true" />
            </>
          )}
        </button>
      </div>

      <nav className="sidebar-nav" aria-label="Server sections">
        {navItems.map((item) => {
          const disabled = item.requiresContent && selected ? !serverTypeSupportsContent(selected.type) : false;
          const active = item.id === "mods" ? modsActive : tab === item.id;
          const Icon = item.Icon;
          if (item.id === "mods") {
            return (
              <div key={item.id} className={`sidebar-nav-group ${modsExpanded ? "expanded" : ""}`}>
                <button
                  className={`sidebar-nav-item ${active ? "active parent-active" : ""}`}
                  disabled={disabled}
                  title={disabled ? "Vanilla servers do not load mods or plugins" : collapsed ? item.label : undefined}
                  aria-expanded={modsExpanded}
                  onClick={() => handleNavClick(item)}
                >
                  <span className="sidebar-nav-icon"><Icon size={17} /></span>
                  <span className="sidebar-nav-label">{item.label}</span>
                  <ChevronDown size={14} className="sidebar-nav-caret" aria-hidden="true" />
                </button>
                {modsExpanded && !disabled && (
                  <div className="sidebar-nav-children" aria-label="Mods / Plugins views">
                    {modsChildItems.map((child) => {
                      const ChildIcon = child.Icon;
                      return (
                        <button
                          key={child.id}
                          className={`sidebar-nav-item sidebar-nav-child ${tab === child.id ? "active" : ""}`}
                          onClick={() => handleModsChildClick(child.id)}
                        >
                          <span className="sidebar-nav-icon"><ChildIcon size={17} /></span>
                          <span className="sidebar-nav-label">{child.label}</span>
                        </button>
                      );
                    })}
                  </div>
                )}
              </div>
            );
          }
          return (
            <button
              key={item.id}
              className={`sidebar-nav-item ${active ? "active" : ""} ${item.id === "public-access" && publicAccessStatus === "on" ? "public-access-on" : ""} ${item.id === "console" && consoleAttention ? "attention-flash" : ""}`}
              disabled={disabled}
              title={disabled ? "Vanilla servers do not load mods" : undefined}
              onClick={() => handleNavClick(item)}
            >
              <span className="sidebar-nav-icon"><Icon size={17} /></span>
              <span className="sidebar-nav-label">{item.label}</span>
              {item.id === "public-access" ? (
                <span
                  className={`sidebar-nav-status-dot ${publicAccessStatus === "loading" ? "loading" : publicAccessStatus === "on" ? "on" : ""}`}
                  aria-label={publicAccessStatus === "loading" ? "Public access starting" : publicAccessStatus === "on" ? "Public access enabled" : "Public access disabled"}
                  title={publicAccessStatus === "loading" ? "Public access starting" : publicAccessStatus === "on" ? "Public access enabled" : "Public access disabled"}
                />
              ) : null}
            </button>
          );
        })}
        {isUtilityTab && (
          <button
            className={`sidebar-nav-item active`}
            onClick={() => handleUtilityPick(tab)}
          >
            <span className="sidebar-nav-icon">{utilityActiveItem ? <utilityActiveItem.Icon size={17} /> : <Settings size={17} />}</span>
            <span className="sidebar-nav-label">{utilityActiveItem ? utilityActiveItem.label : tab === "app" ? "App settings" : tab === "account" ? "Manage account" : tab}</span>
          </button>
        )}
        {!showSelectedServer && !isUtilityTab && !loading && servers.length > 0 && (
          <p className="sidebar-nav-hint muted">Select a server to see its sections.</p>
        )}
      </nav>

      <div className="sidebar-footer">
        <button
          ref={accountRef}
          className="account-button"
          aria-haspopup="menu"
          aria-expanded={accountOpen}
          onClick={() => openAccount()}
          title={collapsed ? user.username : undefined}
        >
          <span className="account-avatar" aria-hidden="true">{(user.username[0] ?? "?").toUpperCase()}</span>
          {!collapsed && <span className="account-name">{user.username}</span>}
        </button>
      </div>
    </aside>

    {accountOpen && (
      <FloatingMenu className="floating-menu account-menu" position={accountPosition} width={accountMenuWidth}>
        <button role="menuitem" onClick={() => { setAccountOpen(false); setSwitcherOpen(false); setTab("account"); closeMobileSidebar(); }}><UserRound size={16} />Manage account</button>
        <button role="menuitem" onClick={() => { setAccountOpen(false); setSwitcherOpen(false); setTab("app"); closeMobileSidebar(); }}><Settings size={16} />App settings</button>
        <button role="menuitem" onClick={logout}><LogOut size={16} />Logout</button>
      </FloatingMenu>
    )}

    {switcherOpen && (
      <FloatingMenu className="server-switcher-menu" position={switcherPosition} width={switcherMenuWidth}>
        <div className="server-switcher-search">
          <input
            autoFocus
            type="search"
            placeholder="Search servers"
            value={switcherQuery}
            onChange={(event) => setSwitcherQuery(event.target.value)}
          />
        </div>
        <div className="server-switcher-list">
          {filteredServers.map((server) => {
            const running = serverIsRunning(runtime, server.id);
            const active = selected?.id === server.id;
            return (
              <div key={server.id} className={`switcher-server-row ${active ? "active" : ""} ${running ? "running" : ""}`}>
                <button className="switcher-server-pick" onClick={() => handleServerPick(server)}>
                  <ServerAvatar server={server} on={running} className="switcher-server-avatar" />
                  <span className="switcher-server-meta">
                    <span className="switcher-server-name">{server.name}</span>
                    <span className="switcher-server-sub">
                      <span className={`switcher-server-status ${running ? "on" : ""}`}>{running ? "Online" : "Stopped"}</span>
                      <span>{server.type}</span>
                      <span>:{server.port}</span>
                    </span>
                  </span>
                </button>
                <button
                  className="server-menu-button"
                  aria-label={`Actions for ${server.name}`}
                  aria-expanded={serverActionMenu === server.id}
                  aria-haspopup="menu"
                  onClick={(event) => { event.stopPropagation(); openServerMenu(server.id, event.currentTarget); }}
                >
                  <MoreHorizontal size={14} />
                </button>
              </div>
            );
          })}
          {filteredServers.length === 0 && (
            <div className="switcher-empty muted">{sidebarServers.length === 0 ? "No servers yet." : "No servers match your search."}</div>
          )}
        </div>
        <div className="server-switcher-footer">
          <button role="menuitem" onClick={() => handleUtilityPick("create")}><Plus size={16} />Create new server</button>
          <button role="menuitem" onClick={() => handleUtilityPick("import")}><Upload size={16} />Import server</button>
        </div>
      </FloatingMenu>
    )}

    {openMenuServer && (
      <FloatingMenu className="floating-menu server-menu" position={menuPosition} width={150}>
        <button role="menuitem" onClick={() => onRename(openMenuServer)}>Rename</button>
        <button role="menuitem" disabled={serverIsRunning(runtime, openMenuServer.id)} onClick={() => onDuplicate(openMenuServer)}>Clone server</button>
        <button role="menuitem" className="danger-button" disabled={serverIsRunning(runtime, openMenuServer.id)} onClick={() => onDelete(openMenuServer)}>Delete</button>
      </FloatingMenu>
    )}
    </>
  );
}
