"use client";

import { Download } from "lucide-react";
import { serverPropertiesUrl } from "../../lib/runtime-client";
import type { PropertiesIssue } from "../../lib/properties-text";
import { Banner, Button, Card, CodeEditor, CopyButton } from "../../components/ui";

export function PropertiesEditorCard({
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
    <Card
      title="server.properties"
      actions={
        <>
          <CopyButton text={value} label="Copy server.properties" />
          <Button iconLeft={<Download size={14} />} href={serverPropertiesUrl(serverId, "?download=1")} download>
            Download
          </Button>
        </>
      }
    >
      {running && <Banner variant="warning">The server is running. Changes to this file take effect after a restart.</Banner>}
      <CodeEditor value={value} onChange={onChange} issues={issues} language="properties" ariaLabel="server.properties" />
    </Card>
  );
}
