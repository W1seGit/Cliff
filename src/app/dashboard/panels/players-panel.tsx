"use client";

import { useEffect, useMemo, useState } from "react";
import { Users, UserPlus } from "lucide-react";
import { formatDateTime } from "../lib/utils";
import { fetchServerPlayers, lookupServerPlayer, runPlayerAccessAction } from "../lib/runtime-client";
import type { PlayerAccess, PlayerSession, ServerRecord } from "../lib/types";
import { Button } from "../components/ui/button";
import { Panel } from "../components/ui/panel";
import { Input } from "../components/ui/input";
import { Modal } from "../components/ui/modal";
import { Table, SortableTh } from "../components/ui/table";
import { Pill } from "../components/ui/pill";
import { FilterBar } from "../components/ui/filter-bar";
import { SelectionBar } from "../components/ui/selection-bar";

const defaultPlayerHead = "/assets/logos/steve-head.svg";

export function PlayersPanel({
  server,
  onMessage,
}: {
  server: ServerRecord;
  onMessage: (message: string) => void;
}) {
  const [access, setAccess] = useState<PlayerAccess | null>(null);
  const [sessions, setSessions] = useState<PlayerSession[]>([]);
  const [query, setQuery] = useState("");
  const [roleFilter, setRoleFilter] = useState<string>("");
  const [playerSortKey, setPlayerSortKey] = useState<"player" | "joined" | "ip" | null>(null);
  const [playerSortDir, setPlayerSortDir] = useState<"asc" | "desc">("desc");
  const [busyAction, setBusyAction] = useState("");
  const [manualPlayers, setManualPlayers] = useState<string[]>([]);
  const [showAddPlayer, setShowAddPlayer] = useState(false);
  const [newPlayerName, setNewPlayerName] = useState("");
  const [lookupBusy, setLookupBusy] = useState(false);
  const [selectedPlayers, setSelectedPlayers] = useState<string[]>([]);

  useEffect(() => {
    fetchServerPlayers(server.id)
      .then((data) => { setAccess(data.access); setSessions(data.sessions); })
      .catch((error) => onMessage(error.message));
  }, [server.id, onMessage]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      try {
        setManualPlayers(JSON.parse(window.localStorage.getItem(`manual-players:${server.id}`) ?? "[]"));
      } catch {
        setManualPlayers([]);
      }
    }, 0);
    return () => window.clearTimeout(timer);
  }, [server.id]);

  function saveManualPlayers(players: string[]) {
    setManualPlayers(players);
    window.localStorage.setItem(`manual-players:${server.id}`, JSON.stringify(players));
  }

  const q = query.trim().toLowerCase();
  const ops = useMemo(() => new Set(access?.ops.map((entry) => entry.name) ?? []), [access?.ops]);
  const whitelist = useMemo(() => new Set(access?.whitelist.map((entry) => entry.name) ?? []), [access?.whitelist]);
  const banned = useMemo(() => new Set(access?.bannedPlayers.map((entry) => entry.name) ?? []), [access?.bannedPlayers]);
  const players = useMemo(() => {
    const sessionNames = new Set(sessions.map((session) => session.name.toLowerCase()));
    return [
      ...sessions.map((session) => ({ name: session.name, ip: session.ip, lastJoinedAt: session.lastJoinedAt, manual: false })),
      ...manualPlayers
        .filter((name) => !sessionNames.has(name.toLowerCase()))
        .map((name) => ({ name, ip: "added manually", lastJoinedAt: "added manually", manual: true })),
    ];
  }, [sessions, manualPlayers]);
  const filteredPlayers = useMemo(() =>
    players
      .filter((player) => {
        const matchesQuery = !q || player.name.toLowerCase().includes(q) || player.ip.toLowerCase().includes(q);
        const roleFilters = roleFilter ? roleFilter.split(",") : [];
        const matchesRole =
          roleFilters.length === 0 ||
          (roleFilters.includes("ops") && ops.has(player.name)) ||
          (roleFilters.includes("whitelist") && whitelist.has(player.name)) ||
          (roleFilters.includes("banned") && banned.has(player.name));
        return matchesQuery && matchesRole;
      })
      .toSorted((a, b) => {
        if (playerSortKey === null) {
          if (a.manual !== b.manual) return Number(a.manual) - Number(b.manual);
          if (a.manual && b.manual) return a.name.localeCompare(b.name);
          return new Date(b.lastJoinedAt).getTime() - new Date(a.lastJoinedAt).getTime();
        }
        let cmp = 0;
        if (playerSortKey === "player") cmp = a.name.localeCompare(b.name);
        else if (playerSortKey === "ip") cmp = a.ip.localeCompare(b.ip);
        else {
          if (a.manual !== b.manual) cmp = Number(a.manual) - Number(b.manual);
          else if (a.manual && b.manual) cmp = a.name.localeCompare(b.name);
          else cmp = new Date(a.lastJoinedAt).getTime() - new Date(b.lastJoinedAt).getTime();
        }
        if (cmp === 0) cmp = a.name.localeCompare(b.name);
        return playerSortDir === "asc" ? cmp : -cmp;
      }),
  [players, q, roleFilter, playerSortKey, playerSortDir, ops, whitelist, banned]);
  const allFilteredSelected = filteredPlayers.length > 0 && filteredPlayers.every((player) => selectedPlayers.includes(player.name));

  function handleSort(key: string) {
    const k = key as "player" | "joined" | "ip";
    if (playerSortKey === null) {
      setPlayerSortKey(k);
      setPlayerSortDir("desc");
    } else if (playerSortKey === k) {
      if (playerSortDir === "desc") {
        setPlayerSortDir("asc");
      } else {
        setPlayerSortKey(null);
        setPlayerSortDir("desc");
      }
    } else {
      setPlayerSortKey(k);
      setPlayerSortDir("desc");
    }
  }

  async function addManualPlayer() {
    const name = newPlayerName.trim();
    if (!/^[A-Za-z0-9_]{3,16}$/.test(name)) {
      onMessage("Minecraft usernames must be 3-16 letters, numbers, or underscores");
      return;
    }
    setLookupBusy(true);
    try {
      const profile = await lookupServerPlayer(server.id, name);
      const next = [...new Set([...manualPlayers, profile.name])].toSorted((a, b) => a.localeCompare(b));
      saveManualPlayers(next);
      setNewPlayerName("");
      setShowAddPlayer(false);
      onMessage("Player added");
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Player lookup failed");
    } finally {
      setLookupBusy(false);
    }
  }

  async function bulkAccessAction(kind: keyof PlayerAccess, label: string) {
    if (busyAction || selectedPlayers.length === 0) return;
    const isAdd = label.endsWith(":add");
    const actionName = isAdd ? "add-selected" : "remove-selected";
    setBusyAction(label);
    try {
      const entries = selectedPlayers.map((name) => {
        if (kind === "ops") return { name, level: 4 };
        if (kind === "bannedPlayers") return { name, reason: "Banned by an operator." };
        return { name };
      });
      const data = await runPlayerAccessAction(server.id, { kind, action: actionName, entries });
      setAccess(data.access);
      setSessions(data.sessions);
      const count = selectedPlayers.length;
      const verb = isAdd ? "added to" : "removed from";
      const list = kind === "ops" ? "ops" : kind === "whitelist" ? "whitelist" : "banned players";
      onMessage(`${count} player${count === 1 ? "" : "s"} ${verb} ${list}`);
      setSelectedPlayers([]);
    } catch (error) {
      onMessage(error instanceof Error ? error.message : "Player access update failed");
    } finally {
      setBusyAction("");
    }
  }

  return (
    <Panel
      className="players-list-panel"
      title="Joined players"
      description="Manage ops, whitelist, and bans for this server."
      icon={<Users />}
    >
      <FilterBar
        fields={[
          {
            key: "search",
            label: "Filter joined players",
            type: "text",
            placeholder: "Search by player name or IP",
            value: query,
            onChange: setQuery,
          },
          {
            key: "role",
            label: "Role",
            type: "select",
            multi: true,
            value: roleFilter,
            onChange: setRoleFilter,
            options: [
              { value: "all", label: "All" },
              { value: "ops", label: "Ops" },
              { value: "whitelist", label: "Whitelisted" },
              { value: "banned", label: "Banned" },
            ],
          },
        ]}
        actions={<Button variant="primary" onClick={() => setShowAddPlayer(true)}><UserPlus size={14} />Add player</Button>}
      />

      {selectedPlayers.length > 0 && (
        <SelectionBar
          selectedCount={selectedPlayers.length}
          actions={[
            {
              label: "Op selected",
              disabled: Boolean(busyAction),
              onClick: () => {},
              toggle: [
                { label: "Op selected", onClick: () => bulkAccessAction("ops", "bulk:ops:add") },
                { label: "Deop selected", onClick: () => bulkAccessAction("ops", "bulk:ops:remove") },
              ],
            },
            {
              label: "Whitelist selected",
              disabled: Boolean(busyAction),
              onClick: () => {},
              toggle: [
                { label: "Whitelist selected", onClick: () => bulkAccessAction("whitelist", "bulk:whitelist:add") },
                { label: "Unwhitelist selected", onClick: () => bulkAccessAction("whitelist", "bulk:whitelist:remove") },
              ],
            },
            {
              label: "Ban selected",
              variant: "danger",
              disabled: Boolean(busyAction),
              onClick: () => {},
              toggle: [
                { label: "Ban selected", variant: "danger", onClick: () => bulkAccessAction("bannedPlayers", "bulk:banned:add") },
                { label: "Unban selected", onClick: () => bulkAccessAction("bannedPlayers", "bulk:banned:remove") },
              ],
            },
          ]}
        />
      )}
      <Table className="players-table">
        <colgroup>
          <col className="players-table-select-col" />
          <col className="players-table-player-col" />
          <col className="players-table-joined-col" />
          <col className="players-table-ip-col" />
          <col className="players-table-tags-col" />
          <col className="players-table-count-col" />
        </colgroup>
        <thead>
          <tr><th><Input type="checkbox" aria-label="Select all players" checked={allFilteredSelected} onChange={(event) => setSelectedPlayers(event.target.checked ? filteredPlayers.map((player) => player.name) : [])} /></th><SortableTh label="Player" sortKey="player" activeSort={playerSortKey} sortDir={playerSortDir} onSort={handleSort} /><SortableTh label="Last joined" sortKey="joined" activeSort={playerSortKey} sortDir={playerSortDir} onSort={handleSort} /><SortableTh label="IP" sortKey="ip" activeSort={playerSortKey} sortDir={playerSortDir} onSort={handleSort} /><th>Tags</th><th><span className="table-count">{filteredPlayers.length} of {players.length}</span></th></tr>
        </thead>
        <tbody>
          {filteredPlayers.map((player) => (
            <tr key={player.name}>
              <td><Input type="checkbox" aria-label={`Select ${player.name}`} checked={selectedPlayers.includes(player.name)} onChange={(event) => setSelectedPlayers((current) => event.target.checked ? [...current, player.name] : current.filter((name) => name !== player.name))} /></td>
              <td>
                <span className="player-cell">
                  {/* eslint-disable-next-line @next/next/no-img-element */}
                  <img
                    src={`https://minotar.net/avatar/${encodeURIComponent(player.name)}/32.png`}
                    alt=""
                    loading="lazy"
                    onError={(event) => {
                      if (event.currentTarget.src.endsWith(defaultPlayerHead)) return;
                      event.currentTarget.src = defaultPlayerHead;
                    }}
                  />
                  <strong>{player.name}</strong>
                </span>
              </td>
              <td>{player.manual ? "added manually" : formatDateTime(player.lastJoinedAt)}</td>
              <td>{player.ip}</td>
              <td className="player-tags-cell">
                <span className="player-tags-list">
                  {ops.has(player.name) && <Pill variant="accent">Op</Pill>}
                  {whitelist.has(player.name) && <Pill variant="success">Whitelisted</Pill>}
                  {banned.has(player.name) && <Pill variant="danger">Banned</Pill>}
                  {!ops.has(player.name) && !whitelist.has(player.name) && !banned.has(player.name) && <span className="muted">—</span>}
                </span>
              </td>
              <td></td>
            </tr>
          ))}
          {players.length === 0 && <tr><td colSpan={6} className="muted">No players have joined yet.</td></tr>}
          {players.length > 0 && filteredPlayers.length === 0 && <tr><td colSpan={6} className="muted">No players match.</td></tr>}
        </tbody>
      </Table>
      <Modal
        isOpen={showAddPlayer}
        onClose={() => { setShowAddPlayer(false); setNewPlayerName(""); }}
        title="Add player"
        description="Add a Minecraft player to the table by username."
        confirmLabel="Add player"
        confirmDisabled={lookupBusy || !newPlayerName.trim()}
        confirmLoading={lookupBusy}
        onConfirm={addManualPlayer}
      >
        <Input
          autoFocus
          value={newPlayerName}
          onChange={(event) => setNewPlayerName(event.target.value)}
          onKeyDown={(event) => { if (event.key === "Enter") addManualPlayer(); }}
          placeholder="Minecraft username"
        />
      </Modal>
    </Panel>
  );
}
