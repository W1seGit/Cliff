"use client";

import { useId, useRef } from "react";
import { RotateCcw, Upload } from "lucide-react";
import type { ServerProperties } from "../../lib/types";
import { Button, Card, Input } from "../../components/ui";

export function ServerListCard({
  serverName,
  draft,
  onMotdChange,
  iconSrc,
  onIconError,
  onIconFile,
  onIconReset,
  iconResetDisabled,
}: {
  serverName: string;
  draft: ServerProperties["editable"];
  onMotdChange: (value: string) => void;
  iconSrc: string;
  onIconError: () => void;
  onIconFile: (file: File | null) => void;
  onIconReset: () => void;
  iconResetDisabled: boolean;
}) {
  const fileRef = useRef<HTMLInputElement>(null);
  const iconLabelId = useId();

  return (
    <Card title="Server list" description="How your server appears in the Minecraft multiplayer menu.">
      <div className="server-list-editor">
        <div className="mc-server-preview" aria-label="Minecraft server list preview">
          <div className="mc-server-icon">
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img
              src={iconSrc}
              alt=""
              onLoad={(event) => {
                event.currentTarget.style.display = "block";
              }}
              onError={onIconError}
            />
          </div>
          <div className="mc-server-copy">
            <strong>{serverName}</strong>
            <span>{draft.motd || "A Minecraft Server"}</span>
          </div>
          <div className="mc-server-stats">
            <span>0/{draft.maxPlayers}</span>
            <span className="mc-signal" aria-hidden="true">
              <i />
              <i />
              <i />
              <i />
            </span>
          </div>
        </div>
        <div className="server-list-fields">
          <Input label="Server list description" value={draft.motd} onChange={(event) => onMotdChange(event.target.value)} />
          <div className="icon-picker" role="group" aria-labelledby={iconLabelId}>
            <span id={iconLabelId} className="icon-picker-label">
              Server thumbnail
              <small>PNG, cropped to a square.</small>
            </span>
            <input
              ref={fileRef}
              type="file"
              accept="image/png"
              hidden
              onChange={(event) => {
                onIconFile(event.target.files?.[0] ?? null);
                event.target.value = "";
              }}
            />
            <div className="icon-picker-actions">
              <Button iconLeft={<Upload size={14} />} onClick={() => fileRef.current?.click()}>
                Upload image
              </Button>
              <Button iconLeft={<RotateCcw size={14} />} onClick={onIconReset} disabled={iconResetDisabled}>
                Reset to default
              </Button>
            </div>
          </div>
        </div>
      </div>
    </Card>
  );
}
