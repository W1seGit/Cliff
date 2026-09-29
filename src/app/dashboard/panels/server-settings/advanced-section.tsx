"use client";

import { useMemo, useState } from "react";
import { Card, FieldGrid, Input } from "../../components/ui";

export function RawPropertiesCard({ raw, onChange }: { raw: Record<string, string>; onChange: (key: string, value: string) => void }) {
  const [query, setQuery] = useState("");
  const entries = useMemo(() => {
    const needle = query.trim().toLowerCase();
    const all = Object.entries(raw);
    return needle ? all.filter(([key, value]) => key.toLowerCase().includes(needle) || value.toLowerCase().includes(needle)) : all;
  }, [raw, query]);
  const total = Object.keys(raw).length;

  return (
    <Card
      title="server.properties"
      description={`All ${total} values currently loaded for this server. Edits here stay in sync with the fields on the Game tab.`}
    >
      <Input type="search" aria-label="Filter properties" placeholder="Filter by name or value" value={query} onChange={(event) => setQuery(event.target.value)} />
      {entries.length === 0 ? (
        <p className="muted">No properties match &ldquo;{query}&rdquo;.</p>
      ) : (
        <FieldGrid columns={2}>
          {entries.map(([key, value]) => (
            <Input key={key} label={key} value={value} onChange={(event) => onChange(key, event.target.value)} />
          ))}
        </FieldGrid>
      )}
    </Card>
  );
}
