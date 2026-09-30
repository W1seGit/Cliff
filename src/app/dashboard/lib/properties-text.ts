import type { ServerPropertiesEditable } from "./types";

// Helpers for editing the text of server.properties without losing comments,
// blank lines, or key order. These mirror the daemon's parsing rules
// (daemon/internal/httpserver/properties.go) so the editor and the daemon
// always agree about what the file means.

export type PropertiesIssue = { line: number; message: string };

/** Splits into lines, tolerating both LF and CRLF. */
export function splitLines(text: string): string[] {
  return text.split("\n").map((line) => line.replace(/\r$/, ""));
}

function isCommentOrBlank(line: string) {
  const trimmed = line.trim();
  return trimmed === "" || trimmed.startsWith("#") || trimmed.startsWith("!");
}

/** key -> value, later lines win. Whitespace around the key and before the value is ignored. */
export function parsePropertiesText(text: string): Record<string, string> {
  const raw: Record<string, string> = {};
  for (const line of splitLines(text)) {
    if (isCommentOrBlank(line)) continue;
    const at = line.indexOf("=");
    if (at < 0) continue;
    raw[line.slice(0, at).trim()] = line.slice(at + 1).replace(/^[ \t]+/, "");
  }
  return raw;
}

/** Replaces the value of a key in place, or appends `key=value`. Everything else is untouched. */
export function setPropertyInText(text: string, key: string, value: string): string {
  const eol = text.includes("\r\n") ? "\r\n" : "\n";
  const lines = text === "" ? [] : text.split(/\r?\n/);
  let target = -1;
  lines.forEach((line, index) => {
    if (isCommentOrBlank(line)) return;
    const at = line.indexOf("=");
    if (at >= 0 && line.slice(0, at).trim() === key) target = index; // the last one wins, like the parser
  });
  if (target >= 0) {
    lines[target] = `${key}=${value}`;
    return lines.join(eol);
  }
  // Append, keeping the file's trailing newline.
  if (lines.length > 0 && lines[lines.length - 1] === "") {
    lines.splice(lines.length - 1, 0, `${key}=${value}`);
    return lines.join(eol);
  }
  lines.push(`${key}=${value}`);
  return lines.join(eol) + eol;
}

const LIMITS: Record<string, [number, number]> = {
  "max-players": [1, 1000],
  "server-port": [1, 65535],
  "view-distance": [2, 32],
  "simulation-distance": [2, 32],
};

/** Problems the daemon would reject on save, with 1-based line numbers. */
export function validatePropertiesText(text: string): PropertiesIssue[] {
  const issues: PropertiesIssue[] = [];
  splitLines(text).forEach((line, index) => {
    if (isCommentOrBlank(line)) return;
    const number = index + 1;
    const at = line.indexOf("=");
    const key = at >= 0 ? line.slice(0, at).trim() : "";
    if (at < 0 || key === "") {
      issues.push({ line: number, message: "Must look like key=value" });
      return;
    }
    const value = line.slice(at + 1).trim();
    const limit = LIMITS[key];
    if (limit && value !== "") {
      const parsed = Number(value);
      if (!Number.isInteger(parsed) || parsed < limit[0] || parsed > limit[1]) {
        issues.push({ line: number, message: `${key} must be a number from ${limit[0]} to ${limit[1]}` });
      }
    }
    if (key === "level-name" && value === "") issues.push({ line: number, message: "level-name cannot be empty" });
  });
  return issues;
}

function intOr(raw: Record<string, string>, key: string, fallback: number) {
  const parsed = Number.parseInt(raw[key] ?? "", 10);
  return Number.isFinite(parsed) && String(parsed) === (raw[key] ?? "").trim() ? parsed : fallback;
}

function boolOr(raw: Record<string, string>, key: string, fallback: boolean) {
  const value = (raw[key] ?? "").trim().toLowerCase();
  if (value === "true") return true;
  if (value === "false") return false;
  return fallback;
}

/** The structured form fields, derived from the file. Defaults match the daemon's. */
export function editableFromRaw(raw: Record<string, string>): ServerPropertiesEditable {
  return {
    motd: raw["motd"] ?? "A Minecraft Server",
    levelName: raw["level-name"] ?? "world",
    levelSeed: raw["level-seed"] ?? "",
    gamemode: raw["gamemode"] ?? "survival",
    difficulty: raw["difficulty"] ?? "easy",
    maxPlayers: intOr(raw, "max-players", 20),
    serverPort: intOr(raw, "server-port", 25565),
    viewDistance: intOr(raw, "view-distance", 10),
    simulationDistance: intOr(raw, "simulation-distance", 10),
    onlineMode: boolOr(raw, "online-mode", true),
    whiteList: boolOr(raw, "white-list", false),
    pvp: boolOr(raw, "pvp", true),
    enableCommandBlock: boolOr(raw, "enable-command-block", false),
    allowFlight: boolOr(raw, "allow-flight", false),
  };
}

/** Compare file contents ignoring line-ending style and a missing final newline. */
export function sameProperties(a: string, b: string) {
  const normalize = (text: string) => text.replace(/\r\n/g, "\n").replace(/\n+$/, "");
  return normalize(a) === normalize(b);
}
