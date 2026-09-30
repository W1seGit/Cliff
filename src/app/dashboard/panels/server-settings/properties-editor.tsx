"use client";

import { Download } from "lucide-react";
import { serverPropertiesUrl } from "../../lib/runtime-client";
import type { PropertiesIssue } from "../../lib/properties-text";
import { Banner, Button, CodeEditor, CopyButton } from "../../components/ui";

/** The raw server.properties editor, meant to sit inside another card's "Advanced" section. */
export function PropertiesEditorPanel({
  serverId,
  value,
  onChange,
  issues,
  running,
}: {
  serverId: string;
  value: string;
  onChange: (text: string) => void;
  issues: PropertiesIssue[];
  running: boolean;
}) {
  return (
    <div className="properties-editor-panel">
      <div className="properties-editor-tools">
        <span className="muted">server.properties</span>
        <span className="properties-editor-buttons">
          <CopyButton text={value} label="Copy server.properties" />
          <Button size="sm" iconLeft={<Download size={14} />} href={serverPropertiesUrl(serverId, "?download=1")} download>
            Download
          </Button>
        </span>
      </div>
      {running && <Banner variant="warning">The server is running. Changes to this file take effect after a restart.</Banner>}
      <CodeEditor value={value} onChange={onChange} issues={issues} language="properties" ariaLabel="server.properties" />
    </div>
  );
}
