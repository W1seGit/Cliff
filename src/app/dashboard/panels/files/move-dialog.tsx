"use client";

import { useEffect, useState } from "react";
import { ArrowUp, Folder } from "lucide-react";
import { fetchServerFile } from "../../lib/runtime-client";
import type { FileListing } from "../../lib/types";
import { Modal } from "../../components/ui/modal";

function parentOf(path: string) {
  return path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : "";
}

/** True when `path` is one of `roots` or lies inside one of them. */
function insideAny(path: string, roots: string[]) {
  return roots.some((root) => path === root || path.startsWith(`${root}/`));
}

/**
 * Pick a folder to move files into. It browses the server folder on its own,
 * so the file list behind it does not change until something is moved.
 */
export function MoveDialog({
  serverId,
  paths,
  startPath,
  busy,
  onClose,
  onMove,
}: {
  serverId: string;
  /** What is being moved; empty closes the dialog. */
  paths: string[];
  startPath: string;
  busy: boolean;
  onClose: () => void;
  onMove: (destination: string) => void;
}) {
  const open = paths.length > 0;
  const [current, setCurrent] = useState(startPath);
  const [listing, setListing] = useState<FileListing | null>(null);
  const [error, setError] = useState("");

  // Start in the folder the user is looking at each time the dialog opens.
  const [openedFor, setOpenedFor] = useState("");
  const openKey = open ? `${startPath}|${paths.join(",")}` : "";
  if (openKey !== openedFor) {
    setOpenedFor(openKey);
    if (open) {
      setCurrent(startPath);
      setListing(null);
      setError("");
    }
  }

  useEffect(() => {
    if (!open) return;
    let alive = true;
    fetchServerFile(serverId, current)
      .then((data) => {
        if (!alive) return;
        if ("file" in data) return;
        setListing(data);
        setError("");
      })
      .catch((failure) => { if (alive) setError(failure instanceof Error ? failure.message : "Could not open that folder"); });
    return () => { alive = false; };
  }, [open, serverId, current]);

  const folders = (listing?.entries ?? []).filter((entry) => entry.type === "directory" && !insideAny(entry.path, paths));
  const alreadyThere = paths.every((path) => parentOf(path) === current);
  const insideMoved = insideAny(current, paths);
  const count = paths.length;
  const place = current ? `/${current}` : "the server folder";

  return (
    <Modal
      isOpen={open}
      onClose={onClose}
      title={count === 1 ? "Move 1 item" : `Move ${count} items`}
      description={`Choose the folder to move ${count === 1 ? "it" : "them"} into.`}
      confirmLabel={`Move to ${current ? current.split("/").pop() : "server folder"}`}
      confirmDisabled={busy || alreadyThere || insideMoved || !listing}
      confirmLoading={busy}
      onConfirm={() => onMove(current)}
      busy={busy}
    >
      <div className="move-dialog">
        <div className="move-dialog-path" aria-live="polite">
          <span className="muted">Moving to</span> <strong>{place}</strong>
        </div>
        <ul className="move-dialog-list" aria-label="Folders">
          {current && (
            <li>
              <button type="button" className="move-dialog-row" onClick={() => setCurrent(parentOf(current))}>
                <ArrowUp size={15} aria-hidden="true" />
                <span>Up one folder</span>
              </button>
            </li>
          )}
          {folders.map((folder) => (
            <li key={folder.path}>
              <button type="button" className="move-dialog-row" onClick={() => setCurrent(folder.path)}>
                <Folder size={15} aria-hidden="true" />
                <span>{folder.name}</span>
              </button>
            </li>
          ))}
          {listing && folders.length === 0 && <li className="move-dialog-empty muted">No folders here. You can still move into this one.</li>}
          {!listing && !error && <li className="move-dialog-empty muted">Loading...</li>}
        </ul>
        {error && <p className="move-dialog-error">{error}</p>}
        {alreadyThere && listing && <p className="muted move-dialog-note">That is where {count === 1 ? "it is" : "they are"} already. Pick another folder.</p>}
      </div>
    </Modal>
  );
}
