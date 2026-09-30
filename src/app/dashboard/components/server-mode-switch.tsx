"use client";

import { FolderInput, Plus } from "lucide-react";
import { Tabs } from "./ui/tabs";

/**
 * Switches between making a new server and importing one you already have, so
 * neither page is a dead end. It sits above the wizard's own steps.
 */
export function ServerModeSwitch({ mode, onChange }: { mode: "create" | "import"; onChange: (mode: "create" | "import") => void }) {
  return (
    <div className="server-mode-switch">
      <Tabs
        ariaLabel="Add a server"
        items={[
          { id: "create", label: "Create new server", icon: <Plus size={15} aria-hidden="true" /> },
          { id: "import", label: "Import existing server", icon: <FolderInput size={15} aria-hidden="true" /> },
        ]}
        activeId={mode}
        onChange={(id) => onChange(id as "create" | "import")}
      />
    </div>
  );
}
