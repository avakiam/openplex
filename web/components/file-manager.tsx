"use client";
import {
  Archive,
  ChevronRight,
  Download,
  File as FileIcon,
  FileArchive,
  Folder,
  FolderPlus,
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
const isArchive = (n: string) => /\.(zip|tar\.gz|tgz|tar)$/i.test(n);

type Prompt =
  | { kind: "mkdir" }
  | { kind: "rename"; entry: FileEntry }
  | { kind: "chmod"; entry: FileEntry }
  | { kind: "compress"; entries: FileEntry[] };

export function FileManager({ domainId }: { domainId: number }) {
  const [dir, setDir] = useState(".");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [prompt, setPrompt] = useState<Prompt | null>(null);
  const [value, setValue] = useState("");
  const [toDelete, setToDelete] = useState<FileEntry[] | null>(null);
  const [editing, setEditing] = useState<{ path: string; content: string; dirty: boolean } | null>(null);
  const [uploading, setUploading] = useState(false);
  const uploadRef = useRef<HTMLInputElement>(null);
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

  const open = (e: FileEntry) => {
    if (e.is_dir) {
      setDir(e.path);
      setSelected(new Set());
      return;
    }
    api
      .get<{ content: string }>(`${base}/content?path=${encodeURIComponent(e.path)}`)
      .then((r) => setEditing({ path: e.path, content: r.content, dirty: false }))
      .catch((err: Error) => toast.error(err.message));
  };

  const save = useAction(() => api.put(`${base}/content`, { path: editing!.path, content: editing!.content }), {
    success: "Saved",
    ...inv,
    onSuccess: () => setEditing((e) => (e ? { ...e, dirty: false } : e)),
  });

  async function upload(list: FileList | null) {
    if (!list?.length) return;
    const fd = new FormData();
    Array.from(list).forEach((f) => fd.append("files", f, f.name));
    setUploading(true);
    try {
      await request(`POST`, `${base}/upload?path=${encodeURIComponent(dir)}`, fd);
      toast.success(`Uploaded ${list.length} file${list.length > 1 ? "s" : ""}`);
      files.refetch();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Upload failed");
    } finally {
      setUploading(false);
      if (uploadRef.current) uploadRef.current.value = "";
    }
  }

  const crumbs = dir === "." ? [] : dir.split("/");
  const sel = entries.filter((e) => selected.has(e.path));
  const toggle = (p: string) =>
    setSelected((s) => {
      const n = new Set(s);
      if (n.has(p)) n.delete(p);
      else n.add(p);
      return n;
    });

  const submitPrompt = () => {
    if (!prompt) return;
    if (prompt.kind === "mkdir") op.mutate({ op: "mkdir", body: { path: join(dir, value) } });
    if (prompt.kind === "rename") op.mutate({ op: "rename", body: { from: prompt.entry.path, to: join(dir, value) } });
    if (prompt.kind === "chmod") op.mutate({ op: "chmod", body: { path: prompt.entry.path, mode: value } });
    if (prompt.kind === "compress")
      op.mutate({ op: "compress", body: { paths: prompt.entries.map((e) => e.path), dest: join(dir, value) } });
  };

  if (editing) {
    return (
      <Card className="flex h-[70vh] flex-col overflow-hidden">
        <div className="flex items-center gap-2 border-b px-3 py-2">
          <FileIcon className="size-4 text-muted-foreground" />
          <span className="truncate font-mono text-sm">{editing.path}</span>
          {editing.dirty && <span className="size-2 rounded-full bg-warning" title="Unsaved changes" />}
          <div className="ml-auto flex gap-2">
            <Button size="sm" loading={save.isPending} disabled={!editing.dirty} onClick={() => save.mutate(undefined)}>
              <Save /> Save
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => {
                if (!editing.dirty || confirm("Discard unsaved changes?")) setEditing(null);
              }}
            >
              <X /> Close
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

  return (
    <Card
      onDragOver={(e) => e.preventDefault()}
      onDrop={(e) => {
        e.preventDefault();
        upload(e.dataTransfer.files);
      }}
    >
      <div className="flex flex-wrap items-center gap-2 border-b px-3 py-2">
        <nav className="flex min-w-0 flex-1 items-center gap-1 text-sm">
          <button className="rounded p-1 hover:bg-muted" onClick={() => setDir(".")} aria-label="Site root">
            <Home className="size-4" />
          </button>
          {crumbs.map((c, i) => (
            <span key={i} className="flex items-center gap-1">
              <ChevronRight className="size-3.5 text-muted-foreground" />
              <button className="rounded px-1 hover:bg-muted" onClick={() => setDir(crumbs.slice(0, i + 1).join("/"))}>
                {c}
              </button>
            </span>
          ))}
        </nav>
        {sel.length > 0 && (
          <>
            <Button size="sm" variant="outline" onClick={() => { setValue("archive.tar.gz"); setPrompt({ kind: "compress", entries: sel }); }}>
              <Archive /> Compress ({sel.length})
            </Button>
            <Button size="sm" variant="outline" className="text-destructive" onClick={() => setToDelete(sel)}>
              <Trash2 /> Delete ({sel.length})
            </Button>
          </>
        )}
        <Button size="sm" variant="outline" onClick={() => { setValue(""); setPrompt({ kind: "mkdir" }); }}>
          <FolderPlus /> New folder
        </Button>
        <Button size="sm" variant="outline" onClick={() => { setValue("index.html"); setPrompt({ kind: "rename", entry: { path: "", name: "" } as FileEntry }); }}>
          <Pencil /> New file
        </Button>
        <Button size="sm" loading={uploading} onClick={() => uploadRef.current?.click()}>
          <Upload /> Upload
        </Button>
        <input ref={uploadRef} type="file" multiple hidden onChange={(e) => upload(e.target.files)} />
        <Button size="icon" variant="ghost" onClick={() => files.refetch()} aria-label="Refresh">
          <RefreshCw className={cn(files.isFetching && "animate-spin")} />
        </Button>
      </div>

      {files.isLoading ? (
        <div className="flex justify-center py-14"><Spinner /></div>
      ) : files.error ? (
        <div className="p-4"><ErrorBox error={files.error} /></div>
      ) : entries.length === 0 ? (
        <Empty icon={Folder} title="This folder is empty">Drop files here or use Upload.</Empty>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left text-xs uppercase tracking-wide text-muted-foreground">
                <th className="w-10 border-b px-3 py-2">
                  <input
                    type="checkbox"
                    aria-label="Select all"
                    checked={sel.length === entries.length}
                    onChange={(e) => setSelected(e.target.checked ? new Set(entries.map((x) => x.path)) : new Set())}
                  />
                </th>
                <th className="border-b px-2 py-2 font-medium">Name</th>
                <th className="border-b px-2 py-2 text-right font-medium">Size</th>
                <th className="hidden border-b px-2 py-2 font-medium sm:table-cell">Permissions</th>
                <th className="hidden border-b px-2 py-2 font-medium md:table-cell">Modified</th>
                <th className="w-10 border-b px-2 py-2" />
              </tr>
            </thead>
            <tbody>
              {entries.map((e) => (
                <tr key={e.path} className={cn("hover:bg-muted/40", selected.has(e.path) && "bg-accent/50")}>
                  <td className="border-b px-3 py-2">
                    <input type="checkbox" aria-label={`Select ${e.name}`} checked={selected.has(e.path)} onChange={() => toggle(e.path)} />
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
                      <DropdownTrigger className="rounded p-1.5 hover:bg-muted" aria-label={`Actions for ${e.name}`}>
                        <MoreHorizontal className="size-4" />
                      </DropdownTrigger>
                      <DropdownContent>
                        {!e.is_dir && (
                          <DropdownItem onSelect={() => open(e)}>
                            <Pencil /> Edit
                          </DropdownItem>
                        )}
                        {!e.is_dir && (
                          <DropdownItem onSelect={() => download(`${base}/download?path=${encodeURIComponent(e.path)}`, e.name).catch((err: Error) => toast.error(err.message))}>
                            <Download /> Download
                          </DropdownItem>
                        )}
                        {isArchive(e.name) && (
                          <DropdownItem onSelect={() => op.mutate({ op: "extract", body: { path: e.path } })}>
                            <FileArchive /> Extract here
                          </DropdownItem>
                        )}
                        <DropdownItem onSelect={() => { setValue(e.name); setPrompt({ kind: "rename", entry: e }); }}>
                          <Pencil /> Rename
                        </DropdownItem>
                        <DropdownItem onSelect={() => { setValue(e.mode); setPrompt({ kind: "chmod", entry: e }); }}>
                          <Shield /> Permissions
                        </DropdownItem>
                        <DropdownSeparator />
                        <DropdownItem destructive onSelect={() => setToDelete([e])}>
                          <Trash2 /> Delete
                        </DropdownItem>
                      </DropdownContent>
                    </Dropdown>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <Dialog open={!!prompt} onOpenChange={(o) => !o && setPrompt(null)}>
        {prompt && (
          <DialogContent
            title={
              prompt.kind === "mkdir"
                ? "New folder"
                : prompt.kind === "chmod"
                  ? `Permissions of ${prompt.entry.name}`
                  : prompt.kind === "compress"
                    ? "Compress to archive"
                    : prompt.entry.path
                      ? `Rename ${prompt.entry.name}`
                      : "New file"
            }
          >
            <form
              className="grid gap-4"
              onSubmit={(ev) => {
                ev.preventDefault();
                if (prompt.kind === "rename" && !prompt.entry.path) {
                  // "New file": create empty, then open it in the editor.
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
                label={prompt.kind === "chmod" ? "Mode (octal)" : prompt.kind === "compress" ? "Archive name" : "Name"}
                hint={prompt.kind === "chmod" ? "e.g. 0644 for files, 0755 for folders and scripts" : undefined}
              >
                <Input autoFocus required className={prompt.kind === "chmod" ? "font-mono" : ""} value={value} onChange={(e) => setValue(e.target.value)} pattern={prompt.kind === "chmod" ? "0?[0-7]{3}" : undefined} />
              </Field>
              <ErrorBox error={op.error} />
              <div className="flex justify-end gap-2">
                <Button type="button" variant="outline" onClick={() => setPrompt(null)}>Cancel</Button>
                <Button type="submit" loading={op.isPending}>Save</Button>
              </div>
            </form>
          </DialogContent>
        )}
      </Dialog>

      <Confirm
        open={!!toDelete}
        onOpenChange={(o) => !o && setToDelete(null)}
        title={`Delete ${toDelete?.length === 1 ? toDelete[0].name : `${toDelete?.length} items`}?`}
        description="Folders are deleted with everything inside them. This cannot be undone."
        loading={op.isPending}
        onConfirm={() => op.mutate({ op: "delete", body: { paths: toDelete!.map((e) => e.path) } })}
      />
    </Card>
  );
}
