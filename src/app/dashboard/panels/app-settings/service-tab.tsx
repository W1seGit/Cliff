"use client";

import { Power, RotateCw } from "lucide-react";
import { Banner, Button, Card, SettingRow } from "../../components/ui";

/** Restart or stop the Cliff program that serves this dashboard. */
export function ServiceTab({
  busy,
  stopped,
  onRestart,
  onStop,
}: {
  busy: "" | "restart" | "stop";
  stopped: boolean;
  onRestart: () => void;
  onStop: () => void;
}) {
  if (stopped) {
    return (
      <Card title="Cliff service">
        <Banner variant="info" title="Cliff has stopped">
          This page can no longer reach it. To start it again, open a terminal on the machine Cliff runs on and run <code>cliff start</code>.
        </Banner>
      </Card>
    );
  }

  return (
    <Card title="Cliff service" description="Control the Cliff program itself, not your Minecraft servers.">
      <SettingRow
        label="Restart Cliff"
        description="Restarts Cliff and reloads this page. Any server that is running is stopped first and started again afterwards."
      >
        <Button iconLeft={<RotateCw size={14} />} disabled={busy !== ""} loading={busy === "restart"} loadingText="Restarting..." onClick={onRestart}>
          Restart
        </Button>
      </SettingRow>
      <SettingRow
        label="Stop Cliff"
        description={<>Stops Cliff and any running server. To start it again, run <code>cliff start</code> in a terminal on the machine Cliff runs on. This page stops working until then.</>}
      >
        <Button variant="danger" iconLeft={<Power size={14} />} disabled={busy !== ""} loading={busy === "stop"} loadingText="Stopping..." onClick={onStop}>
          Stop
        </Button>
      </SettingRow>
    </Card>
  );
}
