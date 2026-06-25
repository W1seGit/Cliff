"use client";

import { useEffect, useRef, useState } from "react";
import { FolderOpen, FolderPlus, FilePlus, Plus, Upload } from "lucide-react";
import { formatBytes, joinDisplayPath, shortDate } from "../lib/utils";
import { fetchServerFile, runFileAction, uploadServerFile } from "../lib/runtime-client";
import type { ConfirmRequest, FileListing, FilePayload, ServerRecord, UnsavedChangesRegistration } from "../lib/types";
import { Button } from "../components/ui/button";
import { Panel } from "../components/ui/panel";
import { Toolbar } from "../components/ui/toolbar";
import { Checkbox } from "../components/ui/checkbox";
import { Input } from "../components/ui/input";
import { Textarea } from "../components/ui/textarea";
import { SelectionBar } from "../components/ui/selection-bar";
import { FilterBar } from "../components/ui/filter-bar";
import { Modal } from "../components/ui/modal";

export function FilesPanel({ server, onConfirm, onMessage, onUnsavedChange }: { server: ServerRecord; onConfirm: (request: ConfirmRequest) => void; onMessage: (message: string) => void; onUnsavedChange: (change: UnsavedChangesRegistration | null) => void }) {
  const [listing, setListing] = useState<FileListing | null>(null);
  const [openFile, setOpenFile] = useState<FilePayload["file"] | null>(null);
  const [content, setContent] = useState("");
  const [folderName, setFolderName] = useState("");
  const [newFileName, setNewFileName] = useState("");
  const [uploadFile, setUploadFile] = useState<File | null>(null);
  const [busy, setBusy] = useState("");
  const [query, setQuery] = useState("");
  const [addMenuOpen, setAddMenuOpen] = useState(false);
  const [addModal, setAddModal] = useState<null | "upload" | "folder" | "file">(null);
  const [selectedPaths, setSelectedPaths] = useState<string[]>([]);
  const fileDirty = Boolean(openFile?.editable && content !== openFile.content);
  const saveFileRef = useRef<() => Promise<boolean>>(async () => false);
  const addMenuRef = useRef<HTMLDivElement>(null);
  useEffect(() => { saveFileRef.current = saveFile; });

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
      canSave: !busy,
      onSave: async () => {
        const saved = await saveFileRef.current();
        if (!saved) throw new Error("Save failed");
      },
    } : null);
    return () => onUnsavedChange(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fileDirty, openFile?.path, openFile?.name, server.id, busy]);

  useEffect(() => {
    if (!addMenuOpen) return;
    function handleClickOutside(event: MouseEvent) {
      if (addMenuRef.current && !addMenuRef.current.contains(event.target as Node)) {
        setAddMenuOpen(false);
      }
    }
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, [addMenuOpen]);

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
      await runFileAction(server.id, { action: "delete", path: targetPath });
      if (openFile?.path === targetPath) { setOpenFile(null); setContent(""); }
      await loadPath(listing?.cwd ?? "", true);
    } catch (error) { onMessage(error instanceof Error ? error.message : `Delete ${targetName} failed`); }
    finally { setBusy(""); }
  }

  async function deleteSelectedPaths() {
    if (busy || selectedPaths.length === 0) return;
    setBusy("delete-selected");
    try {
      await runFileAction(server.id, { action: "delete-selected", paths: selectedPaths });
      if (openFile && selectedPaths.includes(openFile.path)) { setOpenFile(null); setContent(""); }
      setSelectedPaths([]);
      await loadPath(listing?.cwd ?? "", true);
    } catch (error) { onMessage(error instanceof Error ? error.message : "Delete selected failed"); }
    finally { setBusy(""); }
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

  const currentFolderPath = listing ? joinDisplayPath(server.path, listing.cwd) : server.path;
  const allEntriesSelected = entries.length > 0 && entries.every((entry) => selectedPaths.includes(entry.path));

  if (openFile) {
    return (
      <section className="file-manager file-manager-editor">
        <Panel className="editor-panel">
          <Toolbar spread>
            <div>
              <h2>{openFile.name}</h2>
              <p className="muted">{formatBytes(openFile.size)}</p>
            </div>
            <Toolbar>
              <Button disabled={Boolean(busy)} onClick={() => guardFileDiscard(() => { setOpenFile(null); setContent(""); })}>Back to files</Button>
              <Button variant="primary" disabled={!openFile.editable || Boolean(busy)} onClick={saveFile} loading={busy === "save"} loadingText="Saving...">Save</Button>
              <Button variant="danger" disabled={Boolean(busy)} onClick={() => onConfirm({
                title: "Delete file", message: `${openFile.name} will be removed.`, confirmLabel: "Delete", dangerous: true,
                onConfirm: () => deletePath(openFile.path, openFile.name),
              })} loading={busy === `delete:${openFile.path}`} loadingText="Deleting...">Delete</Button>
            </Toolbar>
          </Toolbar>
          <Textarea className="file-editor" value={content} onChange={(event) => setContent(event.target.value)} disabled={!openFile.editable} />
        </Panel>
      </section>
    );
  }

  return (
    <section className="file-manager">
      <Panel className="files-list-panel" title="Files" description="Browse, edit, and manage files in your server folder." icon={<FolderOpen />}>
        <div className="compact-path">{currentFolderPath}</div>

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
            <div className="more-menu-wrap" ref={addMenuRef}>
              <Button variant="primary" onClick={() => setAddMenuOpen((v) => !v)}><Plus size={14} />Add</Button>
              {addMenuOpen && (
                <div className="more-menu files-add-menu" role="menu">
                  <button type="button" role="menuitem" onClick={() => { setAddMenuOpen(false); setAddModal("upload"); }}><Upload size={15} />Upload file</button>
                  <button type="button" role="menuitem" onClick={() => { setAddMenuOpen(false); setAddModal("folder"); }}><FolderPlus size={15} />New folder</button>
                  <button type="button" role="menuitem" onClick={() => { setAddMenuOpen(false); setAddModal("file"); }}><FilePlus size={15} />New file</button>
                </div>
              )}
            </div>
          }
        />

        <div className="file-list">
          {entries.length > 0 && (
            <div className="selection-actions">
              <Checkbox compact checked={allEntriesSelected} onChange={(checked) => setSelectedPaths(checked ? entries.map((entry) => entry.path) : [])} label="Select all" />
              {selectedPaths.length > 0 && (
                <SelectionBar
                  selectedCount={selectedPaths.length}
                  actions={[
                    {
                      label: "Delete selected",
                      variant: "danger",
                      disabled: Boolean(busy),
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
            </div>
          )}
          {listing?.parent !== undefined && listing.cwd && (
            <Button className="file-row" disabled={Boolean(busy)} onClick={() => loadPath(listing.parent)}><span>..</span><small>parent</small></Button>
          )}
          {entries.map((entry) => (
            <div className="file-row file-row-actions" key={entry.path}>
              <Input type="checkbox" aria-label={`Select ${entry.name}`} checked={selectedPaths.includes(entry.path)} onChange={(event) => setSelectedPaths((current) => event.target.checked ? [...current, entry.path] : current.filter((item) => item !== entry.path))} />
              <Button className="file-open-button" disabled={Boolean(busy)} onClick={() => openEntry(entry)}>
                <span>{entry.type === "directory" ? "📁" : entry.editable ? "📝" : "📄"} {entry.name}</span>
                <small>{entry.type === "file" ? `${formatBytes(entry.size)}${entry.editable ? " / editable" : ""}` : "folder"} / {shortDate(entry.updatedAt)}</small>
              </Button>
              <Button variant="danger" disabled={Boolean(busy)} onClick={() => onConfirm({
                title: entry.type === "directory" ? "Delete folder" : "Delete file", message: `${entry.name} will be removed.${entry.type === "directory" ? " This also removes everything inside it." : ""}`, confirmLabel: "Delete", dangerous: true,
                onConfirm: () => deletePath(entry.path, entry.name),
              })} loading={busy === `delete:${entry.path}`} loadingText="Deleting...">Delete</Button>
            </div>
          ))}
          {entries.length === 0 && listing && <p className="muted">No entries match.</p>}
        </div>
      </Panel>

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
    </section>
  );
}
