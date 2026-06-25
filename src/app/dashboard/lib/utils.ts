export function formatBytes(value: number) {
  if (!value) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(units.length - 1, Math.floor(Math.log(value) / Math.log(1024)));
  return `${(value / 1024 ** index).toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
}

export function formatDuration(seconds: number) {
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const remaining = seconds % 60;
  if (hours) return `${hours}h ${minutes}m`;
  if (minutes) return `${minutes}m ${remaining}s`;
  return `${remaining}s`;
}

export function shortDate(value?: string | null) {
  if (!value || typeof value !== "string") return "unknown";
  const trimmed = value.trim();
  if (!trimmed) return "unknown";
  return trimmed.slice(0, 10);
}

export function formatDate(value?: string | null) {
  if (!value) return "unknown";
  const date = new Date(value);
  if (isNaN(date.getTime())) return "unknown";
  return date.toLocaleDateString("en-US", { year: "numeric", month: "short", day: "numeric" });
}

export function formatDateTime(value?: string | null) {
  if (!value) return "unknown";
  const date = new Date(value);
  if (isNaN(date.getTime())) return "unknown";
  return date.toLocaleDateString("en-US", { year: "numeric", month: "short", day: "numeric" })
    + " "
    + date.toLocaleTimeString("en-US", { hour: "numeric", minute: "2-digit" });
}

export function compactNumber(value?: number) {
  if (!value) return "0";
  return Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 }).format(value);
}

export function validPort(value: number) {
  return Number.isInteger(value) && value >= 1 && value <= 65535;
}

export function serverTypeNeedsLoader(type: string) {
  return type === "fabric" || type === "forge" || type === "neoforge";
}

export function serverTypeNeedsPlugins(type: string) {
  return type === "paper" || type === "purpur" || type === "folia";
}

export function serverTypeSupportsContent(type: string) {
  return serverTypeNeedsLoader(type) || serverTypeNeedsPlugins(type);
}

export function validMemoryRange(minMemoryMb: number, maxMemoryMb: number) {
  return Number.isFinite(minMemoryMb) && Number.isFinite(maxMemoryMb) && minMemoryMb >= 512 && maxMemoryMb >= minMemoryMb;
}

export function joinDisplayPath(root: string, ...parts: string[]) {
  const separator = root.includes("\\") ? "\\" : "/";
  const trimmedRoot = root.replace(/[\\/]+$/, "");
  return [trimmedRoot, ...parts.map((part) => part.replace(/^[\\/]+|[\\/]+$/g, "")).filter(Boolean)].join(separator);
}

export function externalApiBase() {
  const configured = (process.env.NEXT_PUBLIC_DAEMON_API_BASE ?? "").replace(/\/+$/, "");
  if (!configured) return "";
  // In development, NEXT_PUBLIC_DAEMON_API_BASE is typically set to
  // http://localhost:PORT. When the dashboard is accessed from a LAN IP
  // (e.g. http://192.168.x.x:3000), API calls to localhost would be
  // cross-site, and SameSite=Lax cookies wouldn't be sent. Replace
  // localhost with the current hostname so requests stay same-site.
  // In production the Go daemon serves frontend + API from the same
  // origin, so this branch is never hit.
  if (typeof window !== "undefined") {
    try {
      const parsed = new URL(configured);
      if (parsed.hostname === "localhost" || parsed.hostname === "127.0.0.1") {
        const currentHost = new URL(window.location.origin).hostname;
        if (currentHost !== "localhost" && currentHost !== "127.0.0.1") {
          parsed.hostname = currentHost;
          return parsed.toString().replace(/\/+$/, "");
        }
      }
    } catch {
      // ignore malformed URL, fall through to configured value
    }
  }
  return configured;
}

export function externalApiUrl(path: string) {
  const base = externalApiBase();
  if (!base) return path;
  return `${base}${path.startsWith("/") ? path : `/${path}`}`;
}

export async function api<T>(url: string, init?: RequestInit): Promise<T> {
  const isForm = init?.body instanceof FormData;
  const response = await fetch(url, {
    credentials: "include",
    ...init,
    headers: isForm ? init?.headers : { "Content-Type": "application/json", ...(init?.headers ?? {}) },
  });
  const data = await response.json().catch(() => ({}));
  if (response.status === 401) {
    window.location.replace("/");
    throw new Error("Authentication required");
  }
  if (!response.ok) throw new Error(data.error || "Request failed");
  return data;
}

export function subscribeNoop() {
  return () => undefined;
}

export function browserOrigin() {
  return window.location.origin;
}

export function browserHost() {
  if (typeof window === "undefined") return "localhost";
  try {
    return new URL(window.location.origin).hostname;
  } catch {
    return "localhost";
  }
}

export function serverInitials(name: string) {
  return name
    .split(/[\s-]+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((part) => part[0]?.toUpperCase())
    .join("") || "SV";
}

export function publicAccessStorageKey(serverId: string) {
  return `cliff:public-access:${serverId}:playit`;
}

export function readPublicJoinAddress(serverId: string) {
  if (typeof window === "undefined") return "";
  try {
    const raw = window.localStorage.getItem(publicAccessStorageKey(serverId));
    if (!raw) return "";
    const config = JSON.parse(raw) as { enabled?: boolean; publicAddress?: string };
    if (config.enabled === false) return "";
    return config.publicAddress?.trim() ?? "";
  } catch {
    return "";
  }
}

export function readPublicAccessConfigured(serverId?: string) {
  if (!serverId || typeof window === "undefined") return false;
  const raw = window.localStorage.getItem(publicAccessStorageKey(serverId));
  if (!raw) return false;
  try {
    const config = JSON.parse(raw) as { publicAddress?: string; enabled?: boolean };
    return Boolean(config.publicAddress && config.enabled !== false);
  } catch {
    return false;
  }
}

export function joinAddressFor(server: { id: string; port: number }) {
  const publicAddress = readPublicJoinAddress(server.id);
  return publicAddress || `${browserHost()}:${server.port}`;
}

export function isPublicAddressActive(serverId: string) {
  return Boolean(readPublicJoinAddress(serverId));
}
