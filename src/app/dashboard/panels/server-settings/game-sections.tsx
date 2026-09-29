"use client";

import type { ServerProperties } from "../../lib/types";
import { Card, FieldGrid, Input, Select, ToggleRow } from "../../components/ui";
import { rangeError } from "./validation";

type Editable = ServerProperties["editable"];
type SetField = <K extends keyof Editable>(key: K, value: Editable[K]) => void;

export function EulaCard({ accepted, onChange }: { accepted: boolean; onChange: (accepted: boolean) => void }) {
  return (
    <Card title="Minecraft EULA" description="Mojang requires you to accept the EULA before a server can run.">
      <ToggleRow
        label="I accept the Minecraft EULA"
        description={<>Required before the server can start. Read it at <a href="https://aka.ms/MinecraftEULA" target="_blank" rel="noopener noreferrer">aka.ms/MinecraftEULA</a>.</>}
        checked={accepted}
        onChange={onChange}
      />
    </Card>
  );
}

export function WorldCard({ draft, setField }: { draft: Editable; setField: SetField }) {
  return (
    <Card title="World" description="Which world folder the server loads, and how new terrain is generated.">
      <FieldGrid columns={2}>
        <Input
          label="World folder"
          value={draft.levelName}
          onChange={(event) => setField("levelName", event.target.value)}
          error={draft.levelName.trim() ? "" : "A world folder name is required."}
        />
        <Input label="Seed" value={draft.levelSeed} onChange={(event) => setField("levelSeed", event.target.value)} placeholder="random" />
      </FieldGrid>
    </Card>
  );
}

export function GameplayCard({ draft, setField }: { draft: Editable; setField: SetField }) {
  return (
    <Card title="Gameplay" description="How the game plays and who can join.">
      <FieldGrid columns={2}>
        <Select label="Gamemode" value={draft.gamemode} onChange={(event) => setField("gamemode", event.target.value)}>
          <option>survival</option>
          <option>creative</option>
          <option>adventure</option>
          <option>spectator</option>
        </Select>
        <Select label="Difficulty" value={draft.difficulty} onChange={(event) => setField("difficulty", event.target.value)}>
          <option>peaceful</option>
          <option>easy</option>
          <option>normal</option>
          <option>hard</option>
        </Select>
        <Input
          label="Max players"
          type="number"
          min={1}
          max={1000}
          value={draft.maxPlayers}
          onChange={(event) => setField("maxPlayers", Number(event.target.value))}
          error={rangeError(draft.maxPlayers, 1, 1000)}
        />
        <Input
          label="Port"
          type="number"
          min={1}
          max={65535}
          value={draft.serverPort}
          onChange={(event) => setField("serverPort", Number(event.target.value))}
          error={rangeError(draft.serverPort, 1, 65535)}
        />
      </FieldGrid>
    </Card>
  );
}

export function RulesCard({ draft, setField }: { draft: Editable; setField: SetField }) {
  return (
    <Card title="Rules" description="Performance limits and server rules.">
      <FieldGrid columns={2}>
        <Input
          label="View distance"
          type="number"
          min={2}
          max={32}
          value={draft.viewDistance}
          onChange={(event) => setField("viewDistance", Number(event.target.value))}
          error={rangeError(draft.viewDistance, 2, 32)}
        />
        <Input
          label="Simulation distance"
          type="number"
          min={2}
          max={32}
          value={draft.simulationDistance}
          onChange={(event) => setField("simulationDistance", Number(event.target.value))}
          error={rangeError(draft.simulationDistance, 2, 32)}
        />
      </FieldGrid>
      <div>
        <ToggleRow label="Online mode" description="Verify players against Minecraft servers." checked={draft.onlineMode} onChange={(checked) => setField("onlineMode", checked)} />
        <ToggleRow label="Whitelist" description="Only allow listed players to join." checked={draft.whiteList} onChange={(checked) => setField("whiteList", checked)} />
        <ToggleRow label="PVP" description="Allow players to damage each other." checked={draft.pvp} onChange={(checked) => setField("pvp", checked)} />
        <ToggleRow label="Command blocks" description="Enable command block functionality." checked={draft.enableCommandBlock} onChange={(checked) => setField("enableCommandBlock", checked)} />
        <ToggleRow label="Allow flight" description="Let players fly in survival mode." checked={draft.allowFlight} onChange={(checked) => setField("allowFlight", checked)} />
      </div>
    </Card>
  );
}
