"use client";

import { useEffect, useState } from "react";
import { serverFileUrl } from "../lib/runtime-client";
import type { ServerRecord } from "../lib/types";

const DEFAULT_ICON = "/assets/default-server-icon.png";

export function ServerAvatar({ server, className = "", on = false }: { server: ServerRecord; className?: string; on?: boolean }) {
  const [version, setVersion] = useState(0);
  const [useDefault, setUseDefault] = useState(false);
  const [prevServerId, setPrevServerId] = useState(server.id);

  if (prevServerId !== server.id) {
    setPrevServerId(server.id);
    setUseDefault(false);
    setVersion((v) => v + 1);
  }

  useEffect(() => {
    const handler = (event: Event) => {
      const detail = (event as CustomEvent).detail;
      if (detail?.serverId === server.id) {
        setUseDefault(false);
        setVersion((v) => v + 1);
      }
    };
    window.addEventListener("cliff:icon-updated", handler);
    return () => window.removeEventListener("cliff:icon-updated", handler);
  }, [server.id]);

  const url = useDefault
    ? DEFAULT_ICON
    : serverFileUrl(server.id, "server-icon.png", `raw=1&v=${version}`);

  return (
    <span className={`server-avatar ${on ? "on" : ""} ${className}`.trim()}>
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img key={url} src={url} alt="" loading="lazy" onError={() => setUseDefault(true)} />
    </span>
  );
}

export function notifyServerIconUpdated(serverId: string) {
  window.dispatchEvent(new CustomEvent("cliff:icon-updated", { detail: { serverId } }));
}
