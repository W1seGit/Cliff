"use client";

import { useEffect, useState } from "react";
import { Globe, RotateCw, Upload } from "lucide-react";
import { fetchServerWorlds, runWorldAction, uploadWorldFile, worldUrl } from "../lib/runtime-client";
import type { ConfirmRequest, ServerRecord, WorldsPayload } from "../lib/types";
import { Button } from "../components/ui/button";
import { Panel } from "../components/ui/panel";
import { Input } from "../components/ui/input";
import { Modal } from "../components/ui/modal";
import { Hint } from "../components/ui/hint";
import { Table, SortableTh } from "../components/ui/table";
import { Pill } from "../components/ui/pill";
import { FilterBar } from "../components/ui/filter-bar";
import { SelectionBar } from "../components/ui/selection-bar";

export function WorldsPanel({
  server,
  isRunning,
  onMessage,
  onConfirm,
}: {
  server: ServerRecord;
  isRunning: boolean;
  onMessage: (message: string) => void;
  onConfirm: (request: ConfirmRequest) => void;
}) {
  const [data, setData] = useState<WorldsPayload | null>(null);

  const [worldZip, setWorldZip] = useState<File | null>(null);
  const [worldImportName, setWorldImportName] = useState("");
  const [query, setQuery] = useState("");
  const [worldStatus, setWorldStatus] = useState<string>("");
  const [worldSortKey, setWorldSortKey] = useState<"status" | "name" | "players" | "datapacks" | null>(null);
  const [worldSortDir, setWorldSortDir] = useState<"asc" | "desc">("desc");
  const [showImport, setShowImport] = useState(false);
  const [selectedWorlds, setSelectedWorlds] = useState<string[]>([]);

  useEffect(() => {
    load().catch((error) => onMessage(error.message));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [server.id]);

  async function load() {
    const payload = await fetchServerWorlds(server.id);
    setData(payload);

  }

  async function makeActive(worldName: string) {
    const payload = await runWorldAction(server.id, { action: "set-active", worldName });
    setData(payload);
    onMessage(`${worldName} is now active`);
  }

  async function removeWorld(worldName: string) {
    onConfirm({
      title: "Delete world", message: `${worldName} will be deleted. Active worlds cannot be deleted.`, confirmLabel: "Delete world", dangerous: true,
      onConfirm: async () => {
        const payload = await runWorldAction(server.id, { action: "delete-world", worldName });
        setData(payload);
        onMessage("World deleted");
      },
    });
  }

  async function importWorldZip() {
    if (!worldZip) return;
    const form = new FormData();
    form.set("action", "import-world-zip");
    form.set("worldName", worldImportName);
    form.set("file", worldZip);
    const payload = await uploadWorldFile(server.id, form);
    setData(payload);
    setWorldZip(null);
    setWorldImportName("");
    setShowImport(false);
    onMessage("World zip imported");
  }

  const filteredWorlds = data?.worlds
    .filter((world) => {
      const matchesQuery = !query.trim() || world.name.toLowerCase().includes(query.trim().toLowerCase());
      const statusFilters = worldStatus ? worldStatus.split(",") : [];
      const matchesStatus = statusFilters.length === 0 ||
        (statusFilters.includes("active") && world.active) ||
        (statusFilters.includes("available") && !world.active);
      return matchesQuery && matchesStatus;
    })
    .toSorted((a, b) => {
      if (worldSortKey === null) {
        return Number(b.active) - Number(a.active) || a.name.localeCompare(b.name);
      }
      let cmp = 0;
      if (worldSortKey === "name") cmp = a.name.localeCompare(b.name);
      else if (worldSortKey === "players") cmp = a.playerFiles - b.playerFiles;
      else if (worldSortKey === "datapacks") cmp = a.datapacks.length - b.datapacks.length;
      else cmp = Number(a.active) - Number(b.active);
      if (cmp === 0) cmp = a.name.localeCompare(b.name);
      return worldSortDir === "asc" ? cmp : -cmp;
    }) ?? [];

  const allFilteredSelected = filteredWorlds.length > 0 && filteredWorlds.every((world) => selectedWorlds.includes(world.name));

  function handleSort(key: string) {
    const k = key as "status" | "name" | "players" | "datapacks";
    if (worldSortKey === null) {
      setWorldSortKey(k);
      setWorldSortDir("desc");
    } else if (worldSortKey === k) {
      if (worldSortDir === "desc") {
        setWorldSortDir("asc");
      } else {
        setWorldSortKey(null);
        setWorldSortDir("desc");
      }
    } else {
      setWorldSortKey(k);
      setWorldSortDir("desc");
    }
  }

  return (
    <section className="worlds-layout">
      <Panel
        className="worlds-list-panel"
        title="Worlds"
        description="Manage saved worlds and their datapacks."
        icon={<Globe />}
      >
        {isRunning && <Hint warn>World changes are safest while stopped.</Hint>}
        <FilterBar
          fields={[
            {
              key: "search",
              label: "Filter worlds",
              type: "text",
              placeholder: "Search by world name",
              value: query,
              onChange: setQuery,
            },
            {
              key: "status",
              label: "Status",
              type: "select",
              multi: true,
              value: worldStatus,
              onChange: setWorldStatus,
              options: [
                { value: "all", label: "All" },
                { value: "active", label: "Active" },
                { value: "available", label: "Available" },
              ],
            },
          ]}
          actions={
            <>
              <Button onClick={() => load().catch((error) => onMessage(error.message))}><RotateCw size={14} />Refresh</Button>
              <Button variant="primary" onClick={() => setShowImport(true)}><Upload size={14} />Import world</Button>
            </>
          }
        />
        {selectedWorlds.length > 0 && (
          <SelectionBar
            selectedCount={selectedWorlds.length}
            actions={[
              { label: "Download", disabled: selectedWorlds.length !== 1, onClick: () => {
                const world = filteredWorlds.find((w) => selectedWorlds.includes(w.name));
                if (world) window.open(worldUrl(server.id, `?download=${encodeURIComponent(world.name)}`), "_blank");
              }},
              { label: "Activate", disabled: selectedWorlds.length !== 1 || filteredWorlds.find((w) => w.name === selectedWorlds[0])?.active, onClick: () => {
                const world = filteredWorlds.find((w) => selectedWorlds.includes(w.name));
                if (world && !world.active) makeActive(world.name);
              }},
              { label: "Delete", variant: "danger", disabled: selectedWorlds.length === 0, onClick: () => {
                selectedWorlds.forEach((name) => {
                  const world = filteredWorlds.find((w) => w.name === name);
                  if (world && !world.active) removeWorld(name);
                });
              }},
            ]}
          />
        )}
        <Table>
          <thead>
            <tr>
              <th><Input type="checkbox" aria-label="Select all worlds" checked={allFilteredSelected} onChange={(event) => setSelectedWorlds(event.target.checked ? filteredWorlds.map((w) => w.name) : [])} /></th>
              <SortableTh label="Status" sortKey="status" activeSort={worldSortKey} sortDir={worldSortDir} onSort={handleSort} />
              <SortableTh label="Name" sortKey="name" activeSort={worldSortKey} sortDir={worldSortDir} onSort={handleSort} />
              <SortableTh label="Player Files" sortKey="players" activeSort={worldSortKey} sortDir={worldSortDir} onSort={handleSort} />
              <SortableTh label="Datapacks" sortKey="datapacks" activeSort={worldSortKey} sortDir={worldSortDir} onSort={handleSort} />
              <th><span className="table-count">{filteredWorlds.length} of {data?.worlds.length ?? 0}</span></th>
            </tr>
          </thead>
          <tbody>
            {filteredWorlds.map((world) => (
              <tr key={world.name}>
                <td><Input type="checkbox" aria-label={`Select ${world.name}`} checked={selectedWorlds.includes(world.name)} onChange={(event) => setSelectedWorlds((current) => event.target.checked ? [...current, world.name] : current.filter((name) => name !== world.name))} /></td>
                <td>
                  <Pill variant={world.active ? "success" : "default"}>
                    {world.active ? "active" : "available"}
                  </Pill>
                </td>
                <td><strong>{world.name}</strong></td>
                <td>{world.playerFiles}</td>
                <td>{world.datapacks.length}</td>
                <td></td>
              </tr>
            ))}
            {data && filteredWorlds.length === 0 && (
              <tr>
                <td colSpan={6} className="muted">No worlds match.</td>
              </tr>
            )}
          </tbody>
        </Table>

        <Modal
          isOpen={showImport}
          onClose={() => { setShowImport(false); setWorldZip(null); setWorldImportName(""); }}
          title="Import world"
          description="Select a zipped world folder and optionally give it a dashboard name."
          confirmLabel="Import zip"
          confirmDisabled={!worldZip}
          onConfirm={importWorldZip}
        >
          <Input label="World zip" type="file" accept=".zip" onChange={(event) => setWorldZip(event.target.files?.[0] ?? null)} />
          <Input label="Name (optional)" value={worldImportName} onChange={(event) => setWorldImportName(event.target.value)} placeholder="Use zip name" />
        </Modal>
      </Panel>
    </section>
  );
}
