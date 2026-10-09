import type { PermissionGrants, ServerPermission, User } from "./types";

/**
 * Whether `user` may do `perm` on one server. Admins can do everything. Members need a grant
 * for that server id or for "*" (every server); any grant implies "view".
 * The daemon enforces the same rules, so this only decides what the UI shows.
 */
export function can(user: Pick<User, "role"> | null | undefined, permissions: PermissionGrants | null | undefined, serverId: string, perm: ServerPermission): boolean {
  if (!user) return false;
  if (user.role !== "member") return true; // admin (older daemons send no role: treat as admin)
  if (!permissions) return false;
  const granted = [...(permissions[serverId] ?? []), ...(permissions["*"] ?? [])];
  if (granted.length === 0) return false;
  return perm === "view" || granted.includes(perm);
}

export function isAdmin(user: Pick<User, "role"> | null | undefined): boolean {
  return Boolean(user) && user?.role !== "member";
}

/** Permission a server tab needs. Unknown tabs need only "view". */
export function permissionForTab(tab: string): ServerPermission {
  const base = tab.split("/")[0];
  switch (base) {
    case "console": return "console";
    case "mods": return "mods";
    case "worlds": return "worlds";
    case "players": return "players";
    case "backups": return "backups";
    case "files": return "files";
    case "settings": return "settings";
    default: return "view"; // overview and public-access (reading) only need view
  }
}

/** Tabs that are not about one server and are only for admins. */
export function tabIsAdminOnly(tab: string): boolean {
  return tab === "app" || tab === "import" || tab === "create";
}

/** Whether the user may open `tab` (for the given server, if it is a server tab). */
export function canOpenTab(user: Pick<User, "role"> | null | undefined, permissions: PermissionGrants | null | undefined, serverId: string, tab: string): boolean {
  if (tab === "account") return Boolean(user);
  if (tabIsAdminOnly(tab)) return isAdmin(user);
  if (!serverId) return true;
  // Setting up public access changes the server's tunnel, which needs settings.
  if (tab === "public-access/setup") return can(user, permissions, serverId, "settings");
  return can(user, permissions, serverId, permissionForTab(tab));
}
