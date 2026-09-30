"use client";

import { useEffect, useRef, useState } from "react";
import { ArrowLeft, File as FileIcon, FilePlus, FileText, Folder, FolderInput, FolderOpen, FolderPlus, Lock, Pencil, Plus, ShieldCheck, ShieldOff, Trash2, Upload } from "lucide-react";
import { formatBytes, shortDate } from "../lib/utils";
import { fetchServerFile, runFileAction, uploadServerFile } from "../lib/runtime-client";
import type { ConfirmRequest, FileListing, FilePayload, ServerRecord, UnsavedChangesRegistration } from "../lib/types";
import { Button } from "../components/ui/button";
import { Page } from "../components/ui/page-layout";
import { Breadcrumb } from "../components/ui/breadcrumb";
import { Table } from "../components/ui/table";
import { Input } from "../components/ui/input";
import { CodeEditor, languageForFile } from "../components/ui/code-editor";
import { SelectionBar } from "../components/ui/selection-bar";
import { FilterBar } from "../components/ui/filter-bar";
import { Modal } from "../components/ui/modal";
import { IconButton } from "../components/ui/icon-button";
import { Menu, MenuItem } from "../components/ui/menu";
import { MoveDialog } from "./files/move-dialog";

const safeModeKey = "cliff.files.safeMode";

/** Safe mode is on unless the user turned it off in this browser. */
function readSafeMode() {
  try {
    return window.localStorage.getItem(safeModeKey) !== "off";
  } catch {
    return true;
  }
}


export function FilesPanel({ server, onConfirm, onMessage, onUnsavedChange }: { server: ServerRecord; onConfirm: (request: ConfirmRequest) => void; onMessage: (message: string) => void; onUnsavedChange: (change: UnsavedChangesRegistration | null) => void }) {
  const [listing, setListing] = useState<FileListing | null>(null);
  const [openFile, setOpenFile] = useState<FilePayload["file"] | null>(null);
  const [content, setContent] = useState("");
  const [folderName, setFolderName] = useState("");
  const [newFileName, setNewFileName] = useState("");
  const [uploadFile, setUploadFile] = useState<File | null>(null);
  const [busy, setBusy] = useState("");
  const [query, setQuery] = useState("");
  const [addModal, setAddModal] = useState<null | "upload" | "folder" | "file">(null);
  const [selectedPaths, setSelectedPaths] = useState<string[]>([]);
  const [safeMode, setSafeMode] = useState(() => (typeof window === "undefined" ? true : readSafeMode()));
  const [renameTarget, setRenameTarget] = useState<FileListing["entries"][number] | null>(null);
  const [renameValue, setRenameValue] = useState("");
  const [moveCandidates, setMoveCandidates] = useState<string[]>([]);
  const [dragPaths, setDragPaths] = useState<string[]>([]);
  const [dropTarget, setDropTarget] = useState("");
  const fileDirty = Boolean(openFile?.editable && content !== openFile.content);
  const saveFileRef = useRef<() => Promise<boolean>>(async () => false);
  const discardEditsRef = useRef<() => void>(() => undefined);
  useEffect(() => {
    saveFileRef.current = saveFile;
    discardEditsRef.current = () => { if (openFile) setContent(openFile.content); };
  });

  useEffect(() => {
    loadPath().catch((error) => onMessage(error.message));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [server.id]);

  useEffect(() => {
    onUnsavedChange(fileDirty && openFile ? {
      id: `file:${server.id}:${openFile.path}`,
      label: openFile.name,
      dirty: true,
      message: `${openFile.name} has unsaved changes. Save before leaving, or discard them?`,
      showSaveBar: true,
      canSave: !busy,
      saving: busy === "save",
      saveLabel: "Save file",
      onSave: async () => {
        const saved = await saveFileRef.current();
        if (!saved) throw new Error("Save failed");
      },
      onDiscard: () => discardEditsRef.current(),
    } : null);
    return () => onUnsavedChange(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fileDirty, openFile?.path, openFile?.name, server.id, busy]);

  async function loadPath(relativePath = "", force = false) {
    if (busy && !force) return;
    setBusy("load");
    try {
      const data = await fetchServerFile(server.id, relativePath);
      if ("file" in data) { setOpenFile(data.file); setContent(data.file.content); }
      else { setListing(data); setOpenFile(null); setSelectedPaths([]); }
    } finally { setBusy(""); }
  }

  async function saveFile() {
    if (!openFile?.editable || busy) return false;
    setBusy("save");
    try {
      await runFileAction(server.id, { action: "write", path: openFile.path, content });
      onMessage("File saved");
      await loadPath(openFile.path, true);
      return true;
    } catch (error) { onMessage(error instanceof Error ? error.message : "Save failed"); return false; }
    finally { setBusy(""); }
  }

  function guardFileDiscard(action: () => void | Promise<void>) {
    if (!fileDirty || !openFile) {
      void action();
      return;
    }
    onConfirm({
      title: "Unsaved file changes",
      message: `${openFile.name} has unsaved changes. Save before continuing, or discard them?`,
      confirmLabel: "Save",
      cancelLabel: "Discard changes",
      confirmDisabled: Boolean(busy),
      disableBackdropCancel: true,
      onConfirm: async () => {
        const saved = await saveFile();
        if (saved) await action();
      },
      onCancel: action,
    });
  }

  async function createFolder() {
    if (!listing || !folderName.trim() || busy) return;
    const folderPath = [listing.cwd, folderName.trim()].filter(Boolean).join("/");
    setBusy("mkdir");
    try {
      await runFileAction(server.id, { action: "mkdir", path: folderPath });
      setFolderName("");
      setAddModal(null);
      await loadPath(listing.cwd, true);
      onMessage("Folder created");
    } catch (error) { onMessage(error instanceof Error ? error.message : "Create folder failed"); }
    finally { setBusy(""); }
  }

  async function createFile() {
    if (!listing || !newFileName.trim() || busy) return;
    const filePath = [listing.cwd, newFileName.trim()].filter(Boolean).join("/");
    setBusy("create-file");
    try {
      await runFileAction(server.id, { action: "create-file", path: filePath });
      setNewFileName("");
      setAddModal(null);
      await loadPath(filePath, true);
      onMessage("File created");
    } catch (error) { onMessage(error instanceof Error ? error.message : "Create file failed"); }
    finally { setBusy(""); }
  }

  async function upload() {
    if (!listing || !uploadFile || busy) return;
    setBusy("upload");
    try {
      const form = new FormData();
      form.set("action", "upload");
      form.set("path", listing.cwd);
      form.set("file", uploadFile);
      await uploadServerFile(server.id, form);
      setUploadFile(null);
      setAddModal(null);
      await loadPath(listing.cwd, true);
      onMessage("File uploaded");
    } catch (error) { onMessage(error instanceof Error ? error.message : "Upload failed"); }
    finally { setBusy(""); }
  }

  async function deletePath(targetPath: string, targetName: string) {
    if (busy) return;
    setBusy(`delete:${targetPath}`);
    try {
      await runFileAction(server.id, { action: "delete", path: targetPath, safeMode });
      if (openFile?.path === targetPath) { setOpenFile(null); setContent(""); }
      await loadPath(listing?.cwd ?? "", true);
    } catch (error) { onMessage(error instanceof Error ? error.message : `Delete ${targetName} failed`); }
    finally { setBusy(""); }
  }

  async function deleteSelectedPaths() {
    if (busy || selectedPaths.length === 0) return;
    setBusy("delete-selected");
    try {
      await runFileAction(server.id, { action: "delete-selected", paths: selectedPaths, safeMode });
      if (openFile && selectedPaths.includes(openFile.path)) { setOpenFile(null); setContent(""); }
      setSelectedPaths([]);
      await loadPath(listing?.cwd ?? "", true);
    } catch (error) { onMessage(error instanceof Error ? error.message : "Delete selected failed"); }
    finally { setBusy(""); }
  }

  function changeSafeMode(next: boolean) {
    const apply = () => {
      setSafeMode(next);
      try {
        window.localStorage.setItem(safeModeKey, next ? "on" : "off");
      } catch {
        // Not remembered, but it still applies for this visit.
      }
      onMessage(next ? "Safe mode is on" : "Safe mode is off");
    };
    if (next) {
      apply();
      return;
    }
    onConfirm({
      title: "Turn off Safe mode?",
      message: "Safe mode stops you from moving, renaming or deleting the files a server needs to start, such as run.bat, server jars, eula.txt, server.properties, libraries, and the world folder. Without it, one wrong move can stop the server from starting.",
      confirmLabel: "Turn off Safe mode",
      dangerous: true,
      onConfirm: apply,
    });
  }

  function startRename(entry: FileListing["entries"][number]) {
    setRenameTarget(entry);
    setRenameValue(entry.name);
  }

  async function submitRename() {
    if (!renameTarget || busy) return;
    const name = renameValue.trim();
    if (!name || name === renameTarget.name) { setRenameTarget(null); return; }
    setBusy("rename");
    try {
      await runFileAction(server.id, { action: "rename", path: renameTarget.path, newName: name, safeMode });
      setRenameTarget(null);
      await loadPath(listing?.cwd ?? "", true);
      onMessage(`Renamed to ${name}`);
    } catch (error) { onMessage(error instanceof Error ? error.message : "Rename failed"); }
    finally { setBusy(""); }
  }

  async function moveItems(paths: string[], destination: string) {
    if (busy || paths.length === 0) return;
    setBusy("move");
    try {
      await runFileAction(server.id, { action: "move", paths, destination, safeMode });
      setMoveCandidates([]);
      setSelectedPaths([]);
      await loadPath(listing?.cwd ?? "", true);
      const where = destination ? destination : "the server folder";
      onMessage(paths.length === 1 ? `Moved to ${where}` : `Moved ${paths.length} items to ${where}`);
    } catch (error) { onMessage(error instanceof Error ? error.message : "Move failed"); }
    finally { setBusy(""); setDragPaths([]); setDropTarget(""); }
  }

  /** Whether the dragged items may be dropped into this folder. */
  function canDropInto(folderPath: string) {
    return dragPaths.length > 0
      && !dragPaths.includes(folderPath)
      && !dragPaths.some((path) => folderPath.startsWith(`${path}/`));
  }

  async function doOpenEntry(entry: FileListing["entries"][number]) {
    if (entry.type === "directory") {
      await loadPath(entry.path);
      return;
    }
    if (!entry.editable) {
      onMessage("Only editable text files can be opened here");
      return;
    }
    await loadPath(entry.path);
  }

  async function openEntry(entry: FileListing["entries"][number]) {
    if (fileDirty) {
      guardFileDiscard(() => doOpenEntry(entry));
      return;
    }
    await doOpenEntry(entry);
  }

  const entries = listing?.entries
    .filter((entry) => !query.trim() || entry.name.toLowerCase().includes(query.trim().toLowerCase()))
    .toSorted((a, b) => (a.type === b.type ? a.name.localeCompare(b.name) : a.type === "directory" ? -1 : 1)) ?? [];

  const allEntriesSelected = entries.length > 0 && entries.every((entry) => selectedPaths.includes(entry.path));
  const cwdParts = (listing?.cwd ?? "").split(/[\\/]/).filter(Boolean);
  const crumbs = [
    {
      label: "Server files",
      onClick: cwdParts.length > 0 ? () => loadPath("") : undefined,
      onDrop: cwdParts.length > 0 && dragPaths.length > 0 ? () => { void moveItems(dragPaths, ""); } : undefined,
    },
    ...cwdParts.map((part, index) => {
      const crumbPath = cwdParts.slice(0, index + 1).join("/");
      return {
        label: part,
        onClick: index < cwdParts.length - 1 ? () => loadPath(crumbPath) : undefined,
        onDrop: index < cwdParts.length - 1 && dragPaths.length > 0 ? () => { void moveItems(dragPaths, crumbPath); } : undefined,
      };
    }),
  ];
  const protectedSelected = safeMode && listing ? selectedPaths.some((path) => listing.entries.find((entry) => entry.path === path)?.protected) : false;

  if (openFile) {
    const fileCrumbs = openFile.path.split(/[\\/]/).filter(Boolean);
    const openFileEntry = listing?.entries.find((entry) => entry.path === openFile.path);
    const openFileLocked = safeMode ? openFileEntry?.protected ?? "" : "";
    return (
      <Page
        className="file-editor-page"
        back={
          <Button variant="link" iconLeft={<ArrowLeft size={14} />} disabled={Boolean(busy)} onClick={() => guardFileDiscard(() => { setOpenFile(null); setContent(""); })}>
            Back to files
          </Button>
        }
        title={openFile.name}
        description={`${formatBytes(openFile.size)}${fileCrumbs.length > 1 ? ` · ${fileCrumbs.slice(0, -1).join("/")}` : ""}${openFile.editable ? "" : " · read-only"}`}
        icon={<FileText size={20} />}
        actions={
          <Button
            variant="danger-ghost"
            aria-label={`Delete ${openFile.name}`}
            title={openFileLocked ? `Protected by Safe mode (${openFileLocked.toLowerCase()})` : "Delete file"}
            disabled={Boolean(busy) || Boolean(openFileLocked)}
            onClick={() => onConfirm({
              title: "Delete file", message: `${openFile.name} will be removed.`, confirmLabel: "Delete", dangerous: true,
              onConfirm: () => deletePath(openFile.path, openFile.name),
            })}
            loading={busy === `delete:${openFile.path}`}
            loadingText="Deleting..."
          >
            <Trash2 size={15} />
          </Button>
        }
      >
        <CodeEditor tall value={content} onChange={setContent} language={languageForFile(openFile.name)} disabled={!openFile.editable} ariaLabel={`Contents of ${openFile.name}`} />
      </Page>
    );
  }

  return (
    <Page
      className="files-page"
      title="Files"
      description="Browse, edit, and manage files in your server folder."
      icon={<FolderOpen size={20} />}
      toolbar={
        <FilterBar
          fields={[
            {
              key: "search",
              label: "Filter files",
              type: "text",
              placeholder: "Filter files",
              value: query,
              onChange: setQuery,
            },
          ]}
          actions={
            <>
            <Button
              iconLeft={safeMode ? <ShieldCheck size={14} /> : <ShieldOff size={14} />}
              aria-pressed={safeMode}
              title={safeMode ? "Safe mode protects launcher scripts, server jars and other files a server needs. Click to turn it off." : "Safe mode is off. Click to protect the files a server needs."}
              onClick={() => changeSafeMode(!safeMode)}
            >
              Safe mode: {safeMode ? "On" : "Off"}
            </Button>
            <Menu
              trigger={<Button variant="primary" iconLeft={<Plus size={14} />}>Add</Button>}
            >
              <MenuItem icon={<Upload size={15} />} onSelect={() => setAddModal("upload")}>Upload file</MenuItem>
              <MenuItem icon={<FolderPlus size={15} />} onSelect={() => setAddModal("folder")}>New folder</MenuItem>
              <MenuItem icon={<FilePlus size={15} />} onSelect={() => setAddModal("file")}>New file</MenuItem>
            </Menu>
            </>
          }
        />
      }
    >
      <Breadcrumb items={crumbs} disabled={Boolean(busy)} />

      {selectedPaths.length > 0 && (
        <SelectionBar
            selectedCount={selectedPaths.length}
            actions={[
              {
                label: "Move to...",
                disabled: Boolean(busy) || protectedSelected,
                onClick: () => setMoveCandidates(selectedPaths),
              },
              {
                label: "Delete selected",
                variant: "danger",
                disabled: Boolean(busy) || protectedSelected,
                onClick: () => onConfirm({
                  title: "Delete selected entries",
                  message: `${selectedPaths.length} selected file entr${selectedPaths.length === 1 ? "y" : "ies"} will be removed.`,
                  confirmLabel: "Delete selected",
                  dangerous: true,
                  onConfirm: deleteSelectedPaths,
                }),
              },
            ]}
        />
      )}

      <Table className="files-table">
        <thead>
          <tr>
            <th className="col-check">
              <Input type="checkbox" aria-label="Select all" checked={allEntriesSelected} disabled={entries.length === 0} onChange={(event) => setSelectedPaths(event.target.checked ? entries.map((entry) => entry.path) : [])} />
            </th>
            <th>Name</th>
            <th>Size</th>
            <th>Modified</th>
            <th className="col-actions"><span className="sr-only">Actions</span></th>
          </tr>
        </thead>
        <tbody>
          {listing?.parent !== undefined && listing.cwd && (
            <tr
              className={dropTarget === "::parent" ? "drop-target" : undefined}
              onDragOver={dragPaths.length > 0 ? (event) => { event.preventDefault(); event.dataTransfer.dropEffect = "move"; setDropTarget("::parent"); } : undefined}
              onDragLeave={dragPaths.length > 0 ? () => setDropTarget("") : undefined}
              onDrop={dragPaths.length > 0 ? (event) => { event.preventDefault(); void moveItems(dragPaths, listing.parent); } : undefined}
            >
              <td className="col-check" />
              <td colSpan={4}>
                <Button plain className="cell-link" disabled={Boolean(busy)} onClick={() => loadPath(listing.parent)}>
                  <ArrowLeft size={15} /><span className="cell-link-name">Parent folder</span>
                </Button>
              </td>
            </tr>
          )}
          {entries.map((entry) => {
            const lock = safeMode ? entry.protected ?? "" : "";
            const isFolder = entry.type === "directory";
            const dropping = dropTarget === entry.path;
            return (
              <tr
                key={entry.path}
                draggable={!busy && !lock}
                className={[dropping ? "drop-target" : "", dragPaths.includes(entry.path) ? "dragging" : ""].filter(Boolean).join(" ") || undefined}
                onDragStart={(event) => {
                  const paths = selectedPaths.includes(entry.path) ? selectedPaths : [entry.path];
                  event.dataTransfer.effectAllowed = "move";
                  event.dataTransfer.setData("text/plain", paths.join("\n"));
                  setDragPaths(paths);
                }}
                onDragEnd={() => { setDragPaths([]); setDropTarget(""); }}
                onDragOver={isFolder && canDropInto(entry.path) ? (event) => { event.preventDefault(); event.dataTransfer.dropEffect = "move"; setDropTarget(entry.path); } : undefined}
                onDragLeave={isFolder ? () => setDropTarget((current) => (current === entry.path ? "" : current)) : undefined}
                onDrop={isFolder && canDropInto(entry.path) ? (event) => { event.preventDefault(); void moveItems(dragPaths, entry.path); } : undefined}
              >
                <td className="col-check">
                  <Input type="checkbox" aria-label={`Select ${entry.name}`} checked={selectedPaths.includes(entry.path)} onChange={(event) => setSelectedPaths((current) => event.target.checked ? [...current, entry.path] : current.filter((item) => item !== entry.path))} />
                </td>
                <td>
                  <Button plain className="cell-link" disabled={Boolean(busy)} onClick={() => openEntry(entry)}>
                    {isFolder ? <Folder size={16} /> : entry.editable ? <FileText size={16} /> : <FileIcon size={16} />}
                    <span className="cell-link-name">{entry.name}</span>
                    {lock && <Lock size={12} className="file-lock" aria-label={`Protected by Safe mode: ${lock}`}><title>{`Protected by Safe mode: ${lock}`}</title></Lock>}
                  </Button>
                </td>
                <td className="col-num">{entry.type === "file" ? formatBytes(entry.size) : "—"}</td>
                <td className="col-num">{shortDate(entry.updatedAt)}</td>
                <td className="col-actions">
                  {selectedPaths.length === 0 && (
                    <span className="row-actions">
                      <IconButton size="sm" aria-label={`Rename ${entry.name}`} title={lock ? `Protected by Safe mode (${lock.toLowerCase()})` : "Rename"} disabled={Boolean(busy) || Boolean(lock)} onClick={() => startRename(entry)}><Pencil size={15} /></IconButton>
                      <IconButton size="sm" aria-label={`Move ${entry.name}`} title={lock ? `Protected by Safe mode (${lock.toLowerCase()})` : "Move to a folder"} disabled={Boolean(busy) || Boolean(lock)} onClick={() => setMoveCandidates([entry.path])}><FolderInput size={15} /></IconButton>
                      <Button variant="danger-ghost" size="sm" aria-label={`Delete ${entry.name}`} title={lock ? `Protected by Safe mode (${lock.toLowerCase()})` : "Delete"} disabled={Boolean(busy) || Boolean(lock)} onClick={() => onConfirm({
                        title: isFolder ? "Delete folder" : "Delete file", message: `${entry.name} will be removed.${isFolder ? " This also removes everything inside it." : ""}`, confirmLabel: "Delete", dangerous: true,
                        onConfirm: () => deletePath(entry.path, entry.name),
                      })} loading={busy === `delete:${entry.path}`} loadingText="..."><Trash2 size={15} /></Button>
                    </span>
                  )}
                </td>
              </tr>
            );
          })}
          {entries.length === 0 && listing && (
            <tr><td colSpan={5} className="table-empty">{query.trim() ? "No files match your filter." : "This folder is empty."}</td></tr>
          )}
        </tbody>
      </Table>

      <Modal
        isOpen={addModal === "upload"}
        onClose={() => { setAddModal(null); setUploadFile(null); }}
        title="Upload file"
        description="Choose a file to upload to the current folder."
        confirmLabel="Upload"
        confirmDisabled={!uploadFile || Boolean(busy)}
        confirmLoading={busy === "upload"}
        onConfirm={upload}
      >
        <Input label="File" type="file" disabled={Boolean(busy)} onChange={(event) => setUploadFile(event.target.files?.[0] ?? null)} />
      </Modal>

      <Modal
        isOpen={addModal === "folder"}
        onClose={() => { setAddModal(null); setFolderName(""); }}
        title="New folder"
        description="Create a new folder in the current directory."
        confirmLabel="Create folder"
        confirmDisabled={!folderName.trim() || Boolean(busy)}
        confirmLoading={busy === "mkdir"}
        onConfirm={createFolder}
      >
        <Input label="Folder name" disabled={Boolean(busy)} placeholder="New folder" value={folderName} onChange={(event) => setFolderName(event.target.value)} autoFocus />
      </Modal>

      <Modal
        isOpen={addModal === "file"}
        onClose={() => { setAddModal(null); setNewFileName(""); }}
        title="New file"
        description="Create a new text file in the current directory."
        confirmLabel="Create file"
        confirmDisabled={!newFileName.trim() || Boolean(busy)}
        confirmLoading={busy === "create-file"}
        onConfirm={createFile}
      >
        <Input label="File name" disabled={Boolean(busy)} placeholder="new-file.txt" value={newFileName} onChange={(event) => setNewFileName(event.target.value)} autoFocus />
      </Modal>
      <Modal
        isOpen={renameTarget !== null}
        onClose={() => setRenameTarget(null)}
        title={renameTarget?.type === "directory" ? "Rename folder" : "Rename file"}
        description="Enter a new name. It stays in the same folder."
        confirmLabel="Rename"
        confirmDisabled={!renameValue.trim() || renameValue.trim() === renameTarget?.name || Boolean(busy)}
        confirmLoading={busy === "rename"}
        onConfirm={submitRename}
      >
        <Input
          label="Name"
          autoFocus
          disabled={Boolean(busy)}
          value={renameValue}
          onChange={(event) => setRenameValue(event.target.value)}
          onKeyDown={(event) => { if (event.key === "Enter") void submitRename(); }}
        />
      </Modal>

      <MoveDialog
        serverId={server.id}
        paths={moveCandidates}
        startPath={listing?.cwd ?? ""}
        busy={busy === "move"}
        onClose={() => setMoveCandidates([])}
        onMove={(destination) => { void moveItems(moveCandidates, destination); }}
      />
    </Page>
  );
}
