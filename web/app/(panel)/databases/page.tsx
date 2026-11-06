"use client";
import { Archive, Check, Copy, Database as DbIcon, Eye, EyeOff, KeyRound, Loader2, MoreHorizontal, Plus, SquareArrowOutUpRight, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";

import { ItemBackups } from "@/components/item-backups";
import {
  Badge,
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
  PageHeader,
  Select,
  Spinner,
  Table,
  Td,
  Th,
} from "@/components/ui";
import { api } from "@/lib/api";
import { canManageUsers, useAuth } from "@/lib/auth";
import { keys, useAction, useDatabaseSizes, useDatabases, useEngines, useUsers } from "@/lib/hooks";
import type { Database, DatabaseCredentials, Engine } from "@/lib/types";
import { bytes } from "@/lib/utils";

function prefixFor(username: string) {
  return username.toLowerCase().replace(/[.-]/g, "_").slice(0, 16);
}

/** Opens Adminer in a new tab already logged in, by POSTing its login form. */
function openAdminer(c: DatabaseCredentials) {
  const form = document.createElement("form");
  form.method = "POST";
  form.action = "/tools/adminer/";
  form.target = "_blank";
  const fields: Record<string, string> = {
    "auth[driver]": c.adminer.driver,
    "auth[server]": c.adminer.server,
    "auth[username]": c.username,
    "auth[password]": c.password,
    "auth[db]": c.database,
  };
  for (const [k, v] of Object.entries(fields)) {
    const i = document.createElement("input");
    i.type = "hidden";
    i.name = k;
    i.value = v;
    form.appendChild(i);
  }
  document.body.appendChild(form);
  form.submit();
  form.remove();
}

function CopyRow({ label, value, secret }: { label: string; value: string; secret?: boolean }) {
  const [shown, setShown] = useState(!secret);
  const [done, setDone] = useState(false);
  return (
    <Field label={label}>
      <div className="flex gap-2">
        <Input readOnly className="font-mono text-xs" type={shown ? "text" : "password"} value={value} onFocus={(e) => e.target.select()} />
        {secret && (
          <Button type="button" size="icon" variant="outline" aria-label={shown ? "Hide" : "Show"} onClick={() => setShown((s) => !s)}>
            {shown ? <EyeOff /> : <Eye />}
          </Button>
        )}
        <Button
          type="button"
          size="icon"
          variant="outline"
          aria-label={`Copy ${label}`}
          onClick={() =>
            navigator.clipboard.writeText(value).then(
              () => { setDone(true); setTimeout(() => setDone(false), 1500); },
              () => toast.error("Copy failed — select the text manually"),
            )
          }
        >
          {done ? <Check /> : <Copy />}
        </Button>
      </div>
    </Field>
  );
}

function CredentialsDialog({ db, onClose }: { db: Database | null; onClose: () => void }) {
  const [creds, setCreds] = useState<DatabaseCredentials | null>(null);
  const [loading, setLoading] = useState(false);
  const id = db?.id;
  useEffect(() => {
    if (!id) return;
    let live = true;
    setCreds(null);
    setLoading(true);
    api
      .get<DatabaseCredentials>(`/databases/${id}/credentials`)
      .then((c) => live && setCreds(c))
      .catch((e: Error) => toast.error(e.message))
      .finally(() => live && setLoading(false));
    return () => {
      live = false;
    };
  }, [id]);
  const rotate = useAction(() => api.post<{ password: string }>(`/databases/${db!.id}/rotate-password`), {
    success: "Password rotated — update your application's configuration",
    onSuccess: (r) => setCreds((c) => (c ? { ...c, password: r.password, url: c.url.replace(c.password, r.password) } : c)),
  });
  return (
    <Dialog open={!!db} onOpenChange={(o) => !o && onClose()}>
      <DialogContent title={`Connect to ${db?.name ?? ""}`} description="Use these settings from your website's code. The host is only reachable from your sites, not from the internet.">
        {loading || !creds ? (
          <div className="flex justify-center py-8"><Spinner /></div>
        ) : (
          <div className="grid gap-3">
            <div className="grid grid-cols-[1fr_6rem] gap-3">
              <CopyRow label="Host" value={creds.host} />
              <CopyRow label="Port" value={String(creds.port)} />
            </div>
            {db?.engine !== "redis" && <CopyRow label="Database" value={creds.database} />}
            {db?.engine !== "redis" && <CopyRow label="Username" value={creds.username} />}
            <CopyRow label="Password" value={creds.password} secret />
            <CopyRow label="Connection URL" value={creds.url} secret />
            <div className="mt-3 flex flex-wrap justify-between gap-2">
              <Button variant="outline" loading={rotate.isPending} onClick={() => rotate.mutate(undefined)}>
                <KeyRound /> Rotate password
              </Button>
              {db?.engine !== "redis" && (
                <Button onClick={() => openAdminer(creds)}>
                  <SquareArrowOutUpRight /> Open in Adminer
                </Button>
              )}
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

function CreateDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const { user } = useAuth();
  const engines = useEngines();
  const users = useUsers();
  const [engine, setEngine] = useState<Engine>("mariadb");
  const [name, setName] = useState("");
  const [ownerId, setOwnerId] = useState<number | "">("");
  const owner = ownerId === "" ? user : users.data?.find((u) => u.id === ownerId);
  const create = useAction(
    () => api.post<Database>("/databases", { engine, name, owner_id: ownerId === "" ? undefined : ownerId }),
    { success: "Database is being created", invalidate: [keys.databases], onSuccess: () => { onOpenChange(false); setName(""); } },
  );
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent title="New database" description="A user with a strong generated password is created and granted access to this database only.">
        <form className="grid gap-4" onSubmit={(e) => { e.preventDefault(); create.mutate(undefined); }}>
          <div className="grid grid-cols-3 gap-2" role="radiogroup" aria-label="Engine">
            {(engines.data ?? []).map((en) => (
              <button
                key={en.name}
                type="button"
                role="radio"
                aria-checked={engine === en.name}
                onClick={() => setEngine(en.name)}
                className={`rounded-md border px-3 py-2.5 text-left text-sm ${engine === en.name ? "border-primary bg-accent text-accent-foreground" : "hover:bg-muted"}`}
              >
                <span className="block font-medium">{en.label.split(" ")[0]}</span>
                <span className="text-xs text-muted-foreground">{en.label.split(" ").slice(1).join(" ")}</span>
              </button>
            ))}
          </div>
          {canManageUsers(user) && (
            <Field label="Owner">
              <Select value={ownerId} onChange={(e) => setOwnerId(e.target.value ? Number(e.target.value) : "")}>
                <option value="">Me ({user?.username})</option>
                {(users.data ?? []).filter((u) => u.id !== user?.id).map((u) => <option key={u.id} value={u.id}>{u.username} ({u.role})</option>)}
              </Select>
            </Field>
          )}
          <Field label={engine === "redis" ? "Instance name" : "Database name"} hint={<>Full name: <code>{prefixFor(owner?.username ?? "")}_{name || "…"}</code></>}>
            <div className="flex items-center rounded-md border border-input bg-card focus-within:outline-2 focus-within:outline-ring">
              <span className="pl-3 font-mono text-sm text-muted-foreground">{prefixFor(owner?.username ?? "")}_</span>
              <input
                required
                autoFocus
                pattern="[a-z0-9][a-z0-9_]{0,29}"
                maxLength={30}
                className="h-9 flex-1 bg-transparent pr-3 font-mono text-sm outline-none"
                value={name}
                onChange={(e) => setName(e.target.value.toLowerCase().replace(/[^a-z0-9_]/g, ""))}
              />
            </div>
          </Field>
          {engine === "redis" && <p className="text-xs text-muted-foreground">Each Redis instance runs in its own container (96 MB, LRU eviction, AOF persistence).</p>}
          <ErrorBox error={create.error} />
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
            <Button type="submit" loading={create.isPending}>Create</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

const engineTone = { mariadb: "info", postgres: "info", redis: "warning" } as const;

export default function DatabasesPage() {
  const { user } = useAuth();
  const { data, isLoading, error } = useDatabases();
  const sizes = useDatabaseSizes(!!data?.length);
  const users = useUsers();
  const [create, setCreate] = useState(false);
  const [connect, setConnect] = useState<Database | null>(null);
  const [del, setDel] = useState<Database | null>(null);
  const [backups, setBackups] = useState<Database | null>(null);
  const remove = useAction((id: number) => api.del(`/databases/${id}`), { success: "Database is being deleted", invalidate: [keys.databases], onSuccess: () => setDel(null) });
  const owner = (id: number) => users.data?.find((u) => u.id === id)?.username ?? `#${id}`;

  return (
    <>
      <PageHeader
        title="Databases"
        description="MariaDB, PostgreSQL and Redis, reachable only from your websites."
        actions={<Button onClick={() => setCreate(true)}><Plus /> New database</Button>}
      />
      <Card>
        {isLoading ? (
          <div className="flex justify-center py-14"><Spinner /></div>
        ) : error ? (
          <div className="p-4"><ErrorBox error={error} /></div>
        ) : !data?.length ? (
          <Empty icon={DbIcon} title="No databases yet">
            <Button className="mt-3" onClick={() => setCreate(true)}><Plus /> Create your first database</Button>
          </Empty>
        ) : (
          <Table>
            <thead>
              <tr>
                <Th>Name</Th><Th>Engine</Th><Th>Host</Th><Th className="text-right">Size</Th><Th>Status</Th>
                {canManageUsers(user) && <Th>Owner</Th>}
                <Th className="w-12" />
              </tr>
            </thead>
            <tbody>
              {data.map((d) => (
                <tr key={d.id} className="hover:bg-muted/40">
                  <Td>
                    <button className="font-mono font-medium hover:underline disabled:no-underline" disabled={d.status !== "running"} onClick={() => setConnect(d)}>
                      {d.name}
                    </button>
                  </Td>
                  <Td><Badge tone={engineTone[d.engine]}>{d.engine_label}</Badge></Td>
                  <Td className="font-mono text-xs text-muted-foreground">{d.host}:{d.port}</Td>
                  <Td className="tabular text-right text-muted-foreground">{sizes.data?.[d.id] !== undefined ? bytes(sizes.data[d.id]) : "—"}</Td>
                  <Td>
                    {d.status === "running" ? (
                      <Badge tone="success">Ready</Badge>
                    ) : d.status === "pending" ? (
                      <Badge tone="info"><Loader2 className="size-3 animate-spin" /> {d.status_message === "deleting" ? "Deleting" : "Creating"}</Badge>
                    ) : (
                      <Badge tone="danger" title={d.status_message}>Error</Badge>
                    )}
                    {d.status === "error" && <p className="mt-1 max-w-xs truncate text-xs text-destructive" title={d.status_message}>{d.status_message}</p>}
                  </Td>
                  {canManageUsers(user) && <Td className="text-muted-foreground">{owner(d.owner_id)}</Td>}
                  <Td>
                    <Dropdown>
                      <DropdownTrigger className="rounded p-1.5 hover:bg-muted" aria-label={`Actions for ${d.name}`}><MoreHorizontal className="size-4" /></DropdownTrigger>
                      <DropdownContent>
                        <DropdownItem disabled={d.status !== "running"} onSelect={() => setConnect(d)}><KeyRound /> Connection details</DropdownItem>
                        {d.engine !== "redis" && (
                          <DropdownItem
                            disabled={d.status !== "running"}
                            onSelect={() => api.get<DatabaseCredentials>(`/databases/${d.id}/credentials`).then(openAdminer, (e: Error) => toast.error(e.message))}
                          >
                            <SquareArrowOutUpRight /> Open in Adminer
                          </DropdownItem>
                        )}
                        <DropdownItem disabled={d.status !== "running"} onSelect={() => setBackups(d)}><Archive /> Backups</DropdownItem>
                        <DropdownSeparator />
                        <DropdownItem destructive disabled={d.status === "pending"} onSelect={() => setDel(d)}><Trash2 /> Delete</DropdownItem>
                      </DropdownContent>
                    </Dropdown>
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
      <CreateDialog open={create} onOpenChange={setCreate} />
      <CredentialsDialog db={connect} onClose={() => setConnect(null)} />
      <Dialog open={!!backups} onOpenChange={(o) => !o && setBackups(null)}>
        <DialogContent title={`Backups of ${backups?.name ?? ""}`} className="max-w-2xl">
          {backups && (
            <ItemBackups
              base={`/databases/${backups.id}`}
              label={backups.name}
              warning="The database is replaced with the backup: changes made since then are lost. Applications using it may see errors while it is being restored."
            />
          )}
        </DialogContent>
      </Dialog>
      <Confirm
        open={!!del}
        onOpenChange={(o) => !o && setDel(null)}
        title={`Delete ${del?.name}?`}
        description="The database, its user and all data are permanently removed. Back it up first if you need it."
        loading={remove.isPending}
        onConfirm={() => remove.mutate(del!.id)}
      />
    </>
  );
}
