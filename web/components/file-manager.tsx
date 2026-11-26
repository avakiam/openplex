"use client";
import {
  Archive,
  ChevronRight,
  CornerLeftUp,
  Download,
  File as FileIcon,
  FileArchive,
  Folder,
  FolderInput,
  FolderOpen,
  FolderPlus,
  FolderUp,
  Home,
  MoreHorizontal,
  Pencil,
  RefreshCw,
  Save,
  Shield,
  Trash2,
  Upload,
  X,
} from "lucide-react";
import dynamic from "next/dynamic";
import { useRef, useState } from "react";
import { toast } from "sonner";

import { api, download, request } from "@/lib/api";
import { keys, useAction, useFiles } from "@/lib/hooks";
import { useT } from "@/lib/i18n";
import type { FileEntry } from "@/lib/types";
import { ago, bytes, cn } from "@/lib/utils";

import {
  Button,
  Card,
  Confirm,
  Dialog,
  DialogContent,
  Dropdown,
  DropdownContent,
  DropdownItem,
  DropdownSeparator,
  DropdownTrigger,
  Empty,
  ErrorBox,
  Field,
  Input,
  Spinner,
} from "./ui";

const CodeEditor = dynamic(() => import("./code-editor").then((m) => m.CodeEditor), {
  ssr: false,
  loading: () => <div className="flex h-full items-center justify-center"><Spinner /></div>,
});

const join = (dir: string, name: string) => (dir === "." || dir === "" ? name : `${dir}/${name}`);
const parentOf = (p: string) => (p.includes("/") ? p.slice(0, p.lastIndexOf("/")) : ".");
const isArchive = (n: string) => /\.(zip|tar\.gz|tgz|tar)$/i.test(n);
const archiveBase = (n: string) => n.replace(/\.(zip|tar\.gz|tgz|tar)$/i, "") || "archive";

// Files dragged between folders of the file manager carry their paths in this type.
const DRAG_TYPE = "application/x-openplex-paths";

type Upload = { file: File; rel: string };

type Prompt =
  | { kind: "mkdir" }
  | { kind: "newfile" }
  | { kind: "rename"; entry: FileEntry }
  | { kind: "chmod"; entry: FileEntry }
  | { kind: "compress"; entries: FileEntry[] }
  | { kind: "move"; entries: FileEntry[] }
  | { kind: "extract"; entry: FileEntry };

type MenuAction = { label: string; icon: React.ReactNode; run: () => void; destructive?: boolean } | "separator";

// filesFromDrop reads the files (and folders, recursively) dropped from the
// computer. The entries must be taken before the drop handler returns.
async function filesFromDrop(dt: DataTransfer): Promise<{ files: Upload[]; emptyDirs: string[] }> {
  const roots = Array.from(dt.items)
    .filter((i) => i.kind === "file")
    .map((i) => i.webkitGetAsEntry?.())
    .filter((e): e is FileSystemEntry => !!e);
  if (roots.length === 0) return { files: Array.from(dt.files).map((f) => ({ file: f, rel: f.name })), emptyDirs: [] };
  const files: Upload[] = [];
  const emptyDirs: string[] = [];
  const walk = async (entry: FileSystemEntry, prefix: string): Promise<void> => {
    const rel = prefix ? `${prefix}/${entry.name}` : entry.name;
    if (entry.isFile) {
      const file = await new Promise<File>((ok, fail) => (entry as FileSystemFileEntry).file(ok, fail));
      files.push({ file, rel });
      return;
    }
    const reader = (entry as FileSystemDirectoryEntry).createReader();
    const children: FileSystemEntry[] = [];
    for (;;) {
      const batch = await new Promise<FileSystemEntry[]>((ok, fail) => reader.readEntries(ok, fail));
      if (batch.length === 0) break;
      children.push(...batch);
    }
    if (children.length === 0) emptyDirs.push(rel);
    for (const c of children) await walk(c, rel);
  };
  for (const r of roots) await walk(r, "");
  return { files, emptyDirs };
}

export function FileManager({ domainId }: { domainId: number }) {
  const { t } = useT();
  const [dir, setDir] = useState(".");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [prompt, setPrompt] = useState<Prompt | null>(null);
  const [value, setValue] = useState("");
  const [toDelete, setToDelete] = useState<FileEntry[] | null>(null);
  const [editing, setEditing] = useState<{ path: string; content: string; dirty: boolean } | null>(null);
  const [uploading, setUploading] = useState(false);
  const [menu, setMenu] = useState<{ x: number; y: number; entry: FileEntry | null } | null>(null);
  const [dropOn, setDropOn] = useState<string | null>(null);
  const uploadRef = useRef<HTMLInputElement>(null);
  const folderRef = useRef<HTMLInputElement>(null);
  const files = useFiles(domainId, dir);
  const entries = files.data?.entries ?? [];
  const base = `/domains/${domainId}/files`;
  const inv = { invalidate: [keys.files(domainId, dir)] };

  const op = useAction((a: { op: string; body: Record<string, unknown> }) => api.post(`${base}/${a.op}`, a.body), {
    ...inv,
    onSuccess: () => {
      setPrompt(null);
      setToDelete(null);
      setSelected(new Set());
    },
  });
  // Moves made by dragging report errors as toasts: there is no dialog to show them in.
  const move = useAction((a: { paths: string[]; dest: string }) => api.post(`${base}/move`, a), {
    ...inv,
    onSuccess: () => setSelected(new Set()),
  });

  const goTo = (p: string) => {
    setDir(p);
    setSelected(new Set());
  };

  const open = (e: FileEntry) => {
    if (e.is_dir) {
      goTo(e.path);
      return;
    }
    api
      .get<{ content: string }>(`${base}/content?path=${encodeURIComponent(e.path)}`)
      .then((r) => setEditing({ path: e.path, content: r.content, dirty: false }))
      .catch((err: Error) => toast.error(err.message));
  };

  const save = useAction(() => api.put(`${base}/content`, { path: editing!.path, content: editing!.content }), {
    success: t("Saved"),
    ...inv,
    onSuccess: () => setEditing((e) => (e ? { ...e, dirty: false } : e)),
  });

  async function upload(list: Upload[], target = dir, emptyDirs: string[] = []) {
    if (list.length === 0 && emptyDirs.length === 0) return;
    setUploading(true);
    try {
      if (list.length > 0) {
        const fd = new FormData();
        list.forEach((u) => fd.append("files", u.file, u.rel));
        await request(`POST`, `${base}/upload?path=${encodeURIComponent(target)}`, fd);
      }
      for (const d of emptyDirs) await api.post(`${base}/mkdir`, { path: join(target, d) });
      toast.success(t("Uploaded {count} files", { count: list.length }));
      files.refetch();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t("Upload failed"));
    } finally {
      setUploading(false);
      if (uploadRef.current) uploadRef.current.value = "";
      if (folderRef.current) folderRef.current.value = "";
    }
  }
  const fromInput = (list: FileList | null): Upload[] =>
    Array.from(list ?? []).map((f) => ({ file: f, rel: f.webkitRelativePath || f.name }));

  const crumbs = dir === "." ? [] : dir.split("/");
  const sel = entries.filter((e) => selected.has(e.path));
  const toggle = (p: string) =>
    setSelected((s) => {
      const n = new Set(s);
      if (n.has(p)) n.delete(p);
      else n.add(p);
      return n;
    });

  // ---- drag and drop ----
  // The entries a drag or a right-click acts on: the whole selection when the
  // entry is part of it, otherwise the entry alone.
  const targetsOf = (e: FileEntry) => (selected.has(e.path) && sel.length > 1 ? sel : [e]);
  const isInternal = (ev: React.DragEvent) => ev.dataTransfer.types.includes(DRAG_TYPE);
  const dropProps = (target: string) => ({
    onDragOver: (ev: React.DragEvent) => {
      ev.preventDefault();
      ev.stopPropagation();
      ev.dataTransfer.dropEffect = isInternal(ev) ? "move" : "copy";
      if (dropOn !== target) setDropOn(target);
    },
    onDragLeave: (ev: React.DragEvent) => {
      if (!ev.currentTarget.contains(ev.relatedTarget as Node)) setDropOn((d) => (d === target ? null : d));
    },
    onDrop: (ev: React.DragEvent) => {
      ev.preventDefault();
      ev.stopPropagation();
      setDropOn(null);
      if (isInternal(ev)) {
        const paths = (JSON.parse(ev.dataTransfer.getData(DRAG_TYPE) || "[]") as string[]).filter(
          (p) => p !== target && parentOf(p) !== target,
        );
        if (paths.length) move.mutate({ paths, dest: target });
        return;
      }
      filesFromDrop(ev.dataTransfer).then((r) => upload(r.files, target, r.emptyDirs));
    },
  });

  // ---- actions, shared by the ⋯ menu and the right-click menu ----
  const entryActions = (e: FileEntry): MenuAction[] => {
    const many = targetsOf(e);
    if (many.length > 1) {
      return [
        { label: t("Move to…"), icon: <FolderInput />, run: () => { setValue(dir === "." ? "" : dir); setPrompt({ kind: "move", entries: many }); } },
        { label: t("Compress ({count})", { count: many.length }), icon: <Archive />, run: () => { setValue("archive.tar.gz"); setPrompt({ kind: "compress", entries: many }); } },
        "separator",
        { label: t("Delete ({count})", { count: many.length }), icon: <Trash2 />, destructive: true, run: () => setToDelete(many) },
      ];
    }
    const acts: MenuAction[] = [];
    if (e.is_dir) acts.push({ label: t("Open"), icon: <FolderOpen />, run: () => open(e) });
    else {
      if (!isArchive(e.name)) acts.push({ label: t("Edit"), icon: <Pencil />, run: () => open(e) });
      acts.push({
        label: t("Download"),
        icon: <Download />,
        run: () => download(`${base}/download?path=${encodeURIComponent(e.path)}`, e.name).catch((err: Error) => toast.error(err.message)),
      });
    }
    if (isArchive(e.name)) {
      acts.push({ label: t("Extract here"), icon: <FileArchive />, run: () => op.mutate({ op: "extract", body: { path: e.path } }) });
      acts.push({
        label: t("Extract to folder…"),
        icon: <FileArchive />,
        run: () => { setValue(join(dir, archiveBase(e.name))); setPrompt({ kind: "extract", entry: e }); },
      });
    } else {
      acts.push({ label: t("Compress"), icon: <Archive />, run: () => { setValue(`${e.name}.tar.gz`); setPrompt({ kind: "compress", entries: [e] }); } });
    }
    acts.push(
      "separator",
      { label: t("Rename"), icon: <Pencil />, run: () => { setValue(e.name); setPrompt({ kind: "rename", entry: e }); } },
      { label: t("Move to…"), icon: <FolderInput />, run: () => { setValue(dir === "." ? "" : dir); setPrompt({ kind: "move", entries: [e] }); } },
      { label: t("Permissions"), icon: <Shield />, run: () => { setValue(e.mode); setPrompt({ kind: "chmod", entry: e }); } },
      "separator",
      { label: t("Delete"), icon: <Trash2 />, destructive: true, run: () => setToDelete([e]) },
    );
    return acts;
  };
  const folderActions: MenuAction[] = [
    { label: t("New folder"), icon: <FolderPlus />, run: () => { setValue(""); setPrompt({ kind: "mkdir" }); } },
    { label: t("New file"), icon: <Pencil />, run: () => { setValue("index.html"); setPrompt({ kind: "newfile" }); } },
    "separator",
    { label: t("Upload files"), icon: <Upload />, run: () => uploadRef.current?.click() },
    { label: t("Upload folder"), icon: <FolderUp />, run: () => folderRef.current?.click() },
    "separator",
    { label: t("Refresh"), icon: <RefreshCw />, run: () => files.refetch() },
  ];
  const renderActions = (acts: MenuAction[]) =>
    acts.map((a, i) =>
      a === "separator" ? (
        <DropdownSeparator key={i} />
      ) : (
        <DropdownItem key={i} destructive={a.destructive} onSelect={a.run}>
          {a.icon} {a.label}
        </DropdownItem>
      ),
    );

  const submitPrompt = () => {
    if (!prompt) return;
    const target = value.trim().replace(/^\/+|\/+$/g, "") || ".";
    if (prompt.kind === "mkdir") op.mutate({ op: "mkdir", body: { path: join(dir, value) } });
    if (prompt.kind === "rename") op.mutate({ op: "rename", body: { from: prompt.entry.path, to: join(dir, value) } });
    if (prompt.kind === "chmod") op.mutate({ op: "chmod", body: { path: prompt.entry.path, mode: value } });
    if (prompt.kind === "compress")
      op.mutate({ op: "compress", body: { paths: prompt.entries.map((e) => e.path), dest: join(dir, value) } });
    if (prompt.kind === "move") op.mutate({ op: "move", body: { paths: prompt.entries.map((e) => e.path), dest: target } });
    if (prompt.kind === "extract") op.mutate({ op: "extract", body: { path: prompt.entry.path, dest: target } });
  };

  if (editing) {
    return (
      <Card className="flex h-[70vh] flex-col overflow-hidden">
        <div className="flex items-center gap-2 border-b px-3 py-2">
          <FileIcon className="size-4 text-muted-foreground" />
          <span className="truncate font-mono text-sm">{editing.path}</span>
          {editing.dirty && <span className="size-2 rounded-full bg-warning" title={t("Unsaved changes")} />}
          <div className="ml-auto flex gap-2">
            <Button size="sm" loading={save.isPending} disabled={!editing.dirty} onClick={() => save.mutate(undefined)}>
              <Save /> {t("Save")}
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => {
                if (!editing.dirty || confirm(t("Discard unsaved changes?"))) setEditing(null);
              }}
            >
              <X /> {t("Close")}
            </Button>
          </div>
        </div>
        <div className="min-h-0 flex-1">
          <CodeEditor
            path={editing.path}
            value={editing.content}
            onChange={(v) => setEditing((e) => (e ? { ...e, content: v, dirty: true } : e))}
            onSave={() => save.mutate(undefined)}
          />
        </div>
      </Card>
    );
  }

  const promptTitle = (p: Prompt) => {
    switch (p.kind) {
      case "mkdir":
        return t("New folder");
      case "newfile":
        return t("New file");
      case "chmod":
        return t("Permissions of {name}", { name: p.entry.name });
      case "compress":
        return t("Compress to archive");
      case "rename":
        return t("Rename {name}", { name: p.entry.name });
      case "move":
        return p.entries.length === 1 ? t("Move {name}", { name: p.entries[0].name }) : t("Move {count} items", { count: p.entries.length });
      case "extract":
        return t("Extract {name}", { name: p.entry.name });
    }
  };
  const isPathPrompt = prompt?.kind === "move" || prompt?.kind === "extract";
  const subfolders = entries.filter((e) => e.is_dir).map((e) => e.path);

  return (
    <Card
      className={cn(dropOn === dir && "ring-2 ring-primary/50")}
      {...dropProps(dir)}
      onContextMenu={(ev) => {
        if ((ev.target as HTMLElement).closest("input,textarea,[role=dialog]")) return;
        ev.preventDefault();
        setMenu({ x: ev.clientX, y: ev.clientY, entry: null });
      }}
    >
      <div className="flex flex-wrap items-center gap-2 border-b px-3 py-2">
        <nav className="flex min-w-0 flex-1 items-center gap-1 text-sm">
          <button
            className={cn("rounded p-1 hover:bg-muted", dropOn === "." && dir !== "." && "bg-primary/15 ring-1 ring-primary")}
            onClick={() => goTo(".")}
            aria-label={t("Site root")}
            {...(dir !== "." ? dropProps(".") : {})}
          >
            <Home className="size-4" />
          </button>
          {crumbs.map((c, i) => {
            const p = crumbs.slice(0, i + 1).join("/");
            return (
              <span key={i} className="flex items-center gap-1">
                <ChevronRight className="size-3.5 text-muted-foreground" />
                <button
                  className={cn("rounded px-1 hover:bg-muted", dropOn === p && p !== dir && "bg-primary/15 ring-1 ring-primary")}
                  onClick={() => goTo(p)}
                  {...(p !== dir ? dropProps(p) : {})}
                >
                  {c}
                </button>
              </span>
            );
          })}
        </nav>
        {sel.length > 0 && (
          <>
            <Button size="sm" variant="outline" onClick={() => { setValue(dir === "." ? "" : dir); setPrompt({ kind: "move", entries: sel }); }}>
              <FolderInput /> {t("Move ({count})", { count: sel.length })}
            </Button>
            <Button size="sm" variant="outline" onClick={() => { setValue("archive.tar.gz"); setPrompt({ kind: "compress", entries: sel }); }}>
              <Archive /> {t("Compress ({count})", { count: sel.length })}
            </Button>
            <Button size="sm" variant="outline" className="text-destructive" onClick={() => setToDelete(sel)}>
              <Trash2 /> {t("Delete ({count})", { count: sel.length })}
            </Button>
          </>
        )}
        <Button size="sm" variant="outline" onClick={() => { setValue(""); setPrompt({ kind: "mkdir" }); }}>
          <FolderPlus /> {t("New folder")}
        </Button>
        <Button size="sm" variant="outline" onClick={() => { setValue("index.html"); setPrompt({ kind: "newfile" }); }}>
          <Pencil /> {t("New file")}
        </Button>
        <Dropdown>
          <DropdownTrigger asChild>
            <Button size="sm" loading={uploading}>
              <Upload /> {t("Upload")}
            </Button>
          </DropdownTrigger>
          <DropdownContent>
            <DropdownItem onSelect={() => uploadRef.current?.click()}>
              <Upload /> {t("Files")}
            </DropdownItem>
            <DropdownItem onSelect={() => folderRef.current?.click()}>
              <FolderUp /> {t("Folder")}
            </DropdownItem>
          </DropdownContent>
        </Dropdown>
        <input ref={uploadRef} type="file" multiple hidden onChange={(e) => upload(fromInput(e.target.files))} />
        <input
          ref={folderRef}
          type="file"
          multiple
          hidden
          onChange={(e) => upload(fromInput(e.target.files))}
          {...({ webkitdirectory: "", directory: "" } as Record<string, string>)}
        />
        <Button size="icon" variant="ghost" onClick={() => files.refetch()} aria-label={t("Refresh")}>
          <RefreshCw className={cn(files.isFetching && "animate-spin")} />
        </Button>
      </div>

      {files.isLoading ? (
        <div className="flex justify-center py-14"><Spinner /></div>
      ) : files.error ? (
        <div className="p-4"><ErrorBox error={files.error} /></div>
      ) : entries.length === 0 && dir === "." ? (
        <Empty icon={Folder} title={t("This folder is empty")}>{t("Drop files or folders here, or use Upload.")}</Empty>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left text-xs uppercase tracking-wide text-muted-foreground">
                <th className="w-10 border-b px-3 py-2">
                  <input
                    type="checkbox"
                    aria-label={t("Select all")}
                    checked={entries.length > 0 && sel.length === entries.length}
                    onChange={(e) => setSelected(e.target.checked ? new Set(entries.map((x) => x.path)) : new Set())}
                  />
                </th>
                <th className="border-b px-2 py-2 font-medium">{t("Name")}</th>
                <th className="border-b px-2 py-2 text-right font-medium">{t("Size")}</th>
                <th className="hidden border-b px-2 py-2 font-medium sm:table-cell">{t("Permissions")}</th>
                <th className="hidden border-b px-2 py-2 font-medium md:table-cell">{t("Modified")}</th>
                <th className="w-10 border-b px-2 py-2" />
              </tr>
            </thead>
            <tbody>
              {dir !== "." && (
                <tr className={cn("hover:bg-muted/40", dropOn === parentOf(dir) && "bg-primary/15")} {...dropProps(parentOf(dir))}>
                  <td className="border-b px-3 py-2" />
                  <td className="border-b px-2 py-2" colSpan={5}>
                    <button
                      className="flex items-center gap-2 text-left text-muted-foreground hover:underline"
                      onClick={() => goTo(parentOf(dir))}
                      title={t("Up one folder")}
                    >
                      <CornerLeftUp className="size-4 shrink-0" />
                      <span>..</span>
                    </button>
                  </td>
                </tr>
              )}
              {entries.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-3 py-8 text-center text-muted-foreground">
                    {t("This folder is empty. Drop files or folders here, or use Upload.")}
                  </td>
                </tr>
              )}
              {entries.map((e) => (
                <tr
                  key={e.path}
                  draggable
                  onDragStart={(ev) => {
                    const paths = targetsOf(e).map((x) => x.path);
                    ev.dataTransfer.setData(DRAG_TYPE, JSON.stringify(paths));
                    ev.dataTransfer.effectAllowed = "move";
                  }}
                  onDragEnd={() => setDropOn(null)}
                  onContextMenu={(ev) => {
                    ev.preventDefault();
                    ev.stopPropagation();
                    setMenu({ x: ev.clientX, y: ev.clientY, entry: e });
                  }}
                  {...(e.is_dir ? dropProps(e.path) : {})}
                  className={cn(
                    "hover:bg-muted/40",
                    selected.has(e.path) && "bg-accent/50",
                    e.is_dir && dropOn === e.path && "bg-primary/15 outline outline-2 -outline-offset-2 outline-primary",
                  )}
                >
                  <td className="border-b px-3 py-2">
                    <input type="checkbox" aria-label={t("Select {name}", { name: e.name })} checked={selected.has(e.path)} onChange={() => toggle(e.path)} />
                  </td>
                  <td className="border-b px-2 py-2">
                    <button className="flex items-center gap-2 text-left hover:underline" onClick={() => open(e)}>
                      {e.is_dir ? (
                        <Folder className="size-4 shrink-0 fill-primary/20 text-primary" />
                      ) : isArchive(e.name) ? (
                        <FileArchive className="size-4 shrink-0 text-muted-foreground" />
                      ) : (
                        <FileIcon className="size-4 shrink-0 text-muted-foreground" />
                      )}
                      <span className="truncate">{e.name}</span>
                    </button>
                  </td>
                  <td className="tabular border-b px-2 py-2 text-right text-muted-foreground">{e.is_dir ? "—" : bytes(e.size)}</td>
                  <td className="hidden border-b px-2 py-2 font-mono text-xs text-muted-foreground sm:table-cell">{e.mode}</td>
                  <td className="hidden border-b px-2 py-2 text-muted-foreground md:table-cell">{ago(e.mod_time)}</td>
                  <td className="border-b px-2 py-1">
                    <Dropdown>
                      <DropdownTrigger className="rounded p-1.5 hover:bg-muted" aria-label={t("Actions for {name}", { name: e.name })}>
                        <MoreHorizontal className="size-4" />
                      </DropdownTrigger>
                      <DropdownContent>{renderActions(entryActions(e))}</DropdownContent>
                    </Dropdown>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <p className="hidden px-3 py-2 text-xs text-muted-foreground md:block">
            {t("Tip: drag files onto a folder, the .. row or the path above to move them; right-click for more actions.")}
          </p>
        </div>
      )}

      {/* Right-click menu: a dropdown anchored at the pointer. */}
      <Dropdown open={!!menu} onOpenChange={(o) => !o && setMenu(null)}>
        <DropdownTrigger asChild>
          <span aria-hidden className="pointer-events-none fixed size-0" style={{ left: menu?.x ?? 0, top: menu?.y ?? 0 }} />
        </DropdownTrigger>
        {menu && (
          <DropdownContent align="start" sideOffset={2}>
            {menu.entry && (
              <div className="truncate px-2 py-1 text-xs text-muted-foreground">
                {targetsOf(menu.entry).length > 1 ? t("{count} items", { count: targetsOf(menu.entry).length }) : menu.entry.name}
              </div>
            )}
            {renderActions(menu.entry ? entryActions(menu.entry) : folderActions)}
          </DropdownContent>
        )}
      </Dropdown>

      <Dialog open={!!prompt} onOpenChange={(o) => !o && setPrompt(null)}>
        {prompt && (
          <DialogContent title={promptTitle(prompt)}>
            <form
              className="grid gap-4"
              onSubmit={(ev) => {
                ev.preventDefault();
                if (prompt.kind === "newfile") {
                  // Create it empty, then open it in the editor.
                  const path = join(dir, value);
                  api
                    .put(`${base}/content`, { path, content: "" })
                    .then(() => {
                      setPrompt(null);
                      files.refetch();
                      setEditing({ path, content: "", dirty: false });
                    })
                    .catch((err: Error) => toast.error(err.message));
                  return;
                }
                submitPrompt();
              }}
            >
              <Field
                label={
                  prompt.kind === "chmod"
                    ? t("Mode (octal)")
                    : prompt.kind === "compress"
                      ? t("Archive name")
                      : isPathPrompt
                        ? t("Destination folder")
                        : t("Name")
                }
                hint={
                  prompt.kind === "chmod"
                    ? t("e.g. 0644 for files, 0755 for folders and scripts")
                    : isPathPrompt
                      ? t("A folder of the website, such as public/img; empty for the website's root. It is created if it does not exist.")
                      : undefined
                }
              >
                <Input
                  autoFocus
                  required={!isPathPrompt}
                  list={isPathPrompt ? "fm-folders" : undefined}
                  className={prompt.kind === "chmod" || isPathPrompt ? "font-mono" : ""}
                  value={value}
                  onChange={(e) => setValue(e.target.value)}
                  pattern={prompt.kind === "chmod" ? "0?[0-7]{3}" : undefined}
                />
              </Field>
              {isPathPrompt && (
                <datalist id="fm-folders">
                  {[...(dir !== "." ? [parentOf(dir) === "." ? "" : parentOf(dir)] : []), ...subfolders].map((p) => (
                    <option key={p} value={p} />
                  ))}
                </datalist>
              )}
              <ErrorBox error={op.error} />
              <div className="flex justify-end gap-2">
                <Button type="button" variant="outline" onClick={() => setPrompt(null)}>{t("Cancel")}</Button>
                <Button type="submit" loading={op.isPending}>
                  {prompt.kind === "move" ? t("Move") : prompt.kind === "extract" ? t("Extract") : t("Save")}
                </Button>
              </div>
            </form>
          </DialogContent>
        )}
      </Dialog>

      <Confirm
        open={!!toDelete}
        onOpenChange={(o) => !o && setToDelete(null)}
        title={toDelete?.length === 1 ? t("Delete {name}?", { name: toDelete[0].name }) : t("Delete {count} items?", { count: toDelete?.length ?? 0 })}
        description={t("Folders are deleted with everything inside them. This cannot be undone.")}
        loading={op.isPending}
        onConfirm={() => op.mutate({ op: "delete", body: { paths: toDelete!.map((e) => e.path) } })}
      />
    </Card>
  );
}
