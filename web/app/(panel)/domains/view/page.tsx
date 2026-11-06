"use client";
import {
  ArrowLeft,
  ExternalLink,
  FileText,
  FolderOpen,
  Gauge,
  Link2,
  Play,
  Plus,
  RotateCw,
  Rocket,
  ScrollText,
  Settings2,
  Square,
  Trash2,
  X,
} from "lucide-react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useEffect, useState } from "react";

import { CreateDomainDialog } from "@/components/create-domain-dialog";
import { FileManager } from "@/components/file-manager";
import { LogViewer } from "@/components/log-viewer";
import { StatusBadge } from "@/components/status";
import {
  Badge,
  Button,
  Card,
  CardBody,
  CardHeader,
  Confirm,
  Empty,
  ErrorBox,
  Field,
  Input,
  PageHeader,
  Select,
  Spinner,
  Switch,
  Table,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Td,
  Th,
} from "@/components/ui";
import { api } from "@/lib/api";
import { keys, useAction, useDomain, useDomainStats, useDomains, useRedirects, useRuntimes } from "@/lib/hooks";
import type { Domain } from "@/lib/types";
import { bytes, dateTime, pct } from "@/lib/utils";

function Overview({ d }: { d: Domain }) {
  const router = useRouter();
  const hasContainer = d.runtime !== "static";
  const stats = useDomainStats(d.id, hasContainer && d.status === "running");
  const [del, setDel] = useState(false);
  const [purge, setPurge] = useState(false);
  const inv = [keys.domain(d.id), keys.domains];
  const action = useAction((a: string) => api.post(`/domains/${d.id}/actions/${a}`), { success: "Request sent", invalidate: inv });
  const remove = useAction(() => api.del(`/domains/${d.id}?purge=${purge}`), {
    success: `${d.name} deleted`,
    invalidate: [keys.domains],
    onSuccess: () => router.replace("/domains/"),
  });
  const scheme = d.ssl_enabled ? "https" : "http";

  return (
    <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_18rem]">
      <Card>
        <CardHeader title="Website" description="Routing, runtime and resources at a glance." />
        <CardBody className="grid gap-x-8 gap-y-4 text-sm sm:grid-cols-2">
          <Info label="Address">
            <a href={`${scheme}://${d.name}`} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-primary hover:underline">
              {scheme}://{d.name} <ExternalLink className="size-3" />
            </a>
          </Info>
          <Info label="Status">
            <StatusBadge status={d.status} />
            {d.status_message && <p className="mt-1 text-xs text-destructive">{d.status_message}</p>}
          </Info>
          <Info label="Runtime">{d.runtime === "static" ? "Static files (served by the edge)" : `${d.runtime} ${d.runtime_version}`}</Info>
          <Info label="Document root"><code className="text-xs">{d.document_root}/</code></Info>
          {d.start_command && <Info label="Start command"><code className="text-xs">{d.start_command}</code> → port {d.app_port}</Info>}
          <Info label="HTTPS">{d.ssl_enabled ? (d.force_https ? "Enabled, HTTP redirects to HTTPS" : "Enabled, HTTP also served") : "Disabled"}</Info>
          <Info label="Limits">{d.memory_mb} MB RAM · {d.cpus} CPU</Info>
          <Info label="Created">{dateTime(d.created_at)}</Info>
        </CardBody>
      </Card>
      <div className="grid content-start gap-4">
        <Card>
          <CardHeader title="Actions" />
          <CardBody className="grid gap-2">
            <Button variant="outline" loading={action.isPending && action.variables === "deploy"} onClick={() => action.mutate("deploy")}>
              <Rocket /> Redeploy
            </Button>
            {d.status === "stopped" ? (
              <Button variant="outline" onClick={() => action.mutate("start")}><Play /> Start</Button>
            ) : (
              <Button variant="outline" onClick={() => action.mutate("stop")}><Square /> Stop (maintenance page)</Button>
            )}
            {hasContainer && (
              <Button variant="outline" onClick={() => action.mutate("restart")}><RotateCw /> Restart container</Button>
            )}
            <Button variant="outline" className="text-destructive" onClick={() => setDel(true)}><Trash2 /> Delete website</Button>
          </CardBody>
        </Card>
        {hasContainer && (
          <Card>
            <CardHeader title="Container" />
            <CardBody className="text-sm">
              {stats.data ? (
                <dl className="tabular grid grid-cols-2 gap-y-2">
                  <dt className="text-muted-foreground">CPU</dt><dd className="text-right">{pct(stats.data.cpu_percent, 1)}</dd>
                  <dt className="text-muted-foreground">Memory</dt><dd className="text-right">{bytes(stats.data.mem_usage)} / {bytes(stats.data.mem_limit)}</dd>
                  <dt className="text-muted-foreground">Network ↓/↑</dt><dd className="text-right">{bytes(stats.data.net_rx)} / {bytes(stats.data.net_tx)}</dd>
                  <dt className="text-muted-foreground">Processes</dt><dd className="text-right">{stats.data.pids}</dd>
                  <dt className="text-muted-foreground">State</dt><dd className="text-right capitalize">{stats.data.state}</dd>
                </dl>
              ) : (
                <p className="text-muted-foreground">{d.status === "running" ? "Sampling…" : "Not running."}</p>
              )}
            </CardBody>
          </Card>
        )}
      </div>
      <Confirm
        open={del}
        onOpenChange={setDel}
        title={`Delete ${d.name}?`}
        description="The container, routing and aliases are removed. Subdomains of this domain are deleted too."
        loading={remove.isPending}
        onConfirm={() => remove.mutate(undefined)}
      >
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={purge} onChange={(e) => setPurge(e.target.checked)} />
          Also delete all files of this website (cannot be undone)
        </label>
      </Confirm>
    </div>
  );
}

function Info({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="mb-1 text-xs font-medium uppercase tracking-wide text-muted-foreground">{label}</div>
      <div>{children}</div>
    </div>
  );
}

function SettingsTab({ d }: { d: Domain }) {
  const runtimes = useRuntimes();
  const [f, setF] = useState(() => ({ ...d, env: Object.entries(d.env ?? {}) }));
  useEffect(() => setF({ ...d, env: Object.entries(d.env ?? {}) }), [d]);
  const rt = runtimes.data?.find((r) => r.name === f.runtime);
  const set = <K extends keyof typeof f>(k: K, v: (typeof f)[K]) => setF((s) => ({ ...s, [k]: v }));
  const save = useAction(
    () =>
      api.patch<Domain>(`/domains/${d.id}`, {
        runtime: f.runtime,
        runtime_version: f.runtime_version || undefined,
        document_root: f.document_root,
        start_command: rt?.needs_command ? f.start_command : undefined,
        app_port: rt?.needs_command ? Number(f.app_port) : undefined,
        ssl_enabled: f.ssl_enabled,
        force_https: f.force_https,
        memory_mb: Number(f.memory_mb),
        cpus: Number(f.cpus),
        env: Object.fromEntries(f.env.filter(([k]) => k.trim()).map(([k, v]) => [k.trim(), v])),
      }),
    { success: "Settings saved — redeploying", invalidate: [keys.domain(d.id), keys.domains] },
  );

  return (
    <form
      className="grid gap-4 lg:grid-cols-2"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate(undefined);
      }}
    >
      <Card>
        <CardHeader title="Runtime" description="Changing the runtime recreates the container." />
        <CardBody className="grid gap-4">
          <div className="grid grid-cols-2 gap-3">
            <Field label="Runtime">
              <Select
                value={f.runtime}
                onChange={(e) => {
                  const r = runtimes.data?.find((x) => x.name === e.target.value);
                  setF((s) => ({ ...s, runtime: e.target.value, runtime_version: r?.default ?? "", app_port: r?.needs_command ? s.app_port || r.default_port : 0 }));
                }}
              >
                {(runtimes.data ?? []).map((r) => <option key={r.name} value={r.name}>{r.label}</option>)}
              </Select>
            </Field>
            {rt?.versions && (
              <Field label="Version">
                <Select value={f.runtime_version} onChange={(e) => set("runtime_version", e.target.value)}>
                  {rt.versions.map((v) => <option key={v}>{v}</option>)}
                </Select>
              </Field>
            )}
          </div>
          {rt?.needs_command ? (
            <div className="grid grid-cols-[1fr_7rem] gap-3">
              <Field label="Start command" hint="Runs in the site folder; listen on $PORT.">
                <Input className="font-mono" required value={f.start_command} onChange={(e) => set("start_command", e.target.value)} />
              </Field>
              <Field label="Port">
                <Input type="number" min={1024} max={65535} required value={f.app_port} onChange={(e) => set("app_port", Number(e.target.value))} />
              </Field>
            </div>
          ) : (
            <Field label="Document root" hint="Folder (inside the site directory) that is served publicly.">
              <Input className="font-mono" value={f.document_root} onChange={(e) => set("document_root", e.target.value)} />
            </Field>
          )}
          <div className="grid grid-cols-2 gap-3">
            <Field label="Memory limit (MB)">
              <Input type="number" min={64} max={65536} value={f.memory_mb} onChange={(e) => set("memory_mb", Number(e.target.value))} />
            </Field>
            <Field label="CPU limit (cores)">
              <Input type="number" min={0.1} max={64} step={0.1} value={f.cpus} onChange={(e) => set("cpus", Number(e.target.value))} />
            </Field>
          </div>
        </CardBody>
      </Card>
      <div className="grid content-start gap-4">
        <Card>
          <CardHeader title="HTTPS" />
          <CardBody className="grid gap-3">
            <label className="flex items-center justify-between gap-4 text-sm">
              <span>
                <span className="block font-medium">Automatic certificate</span>
                <span className="text-xs text-muted-foreground">Requires the domain&apos;s DNS to point at this server.</span>
              </span>
              <Switch checked={f.ssl_enabled} onCheckedChange={(v) => setF((s) => ({ ...s, ssl_enabled: v, force_https: v && s.force_https }))} />
            </label>
            <label className="flex items-center justify-between gap-4 text-sm">
              <span className="font-medium">Redirect HTTP to HTTPS</span>
              <Switch disabled={!f.ssl_enabled} checked={f.force_https} onCheckedChange={(v) => set("force_https", v)} />
            </label>
          </CardBody>
        </Card>
        <Card>
          <CardHeader
            title="Environment variables"
            description="Injected into the container."
            action={<Button type="button" size="sm" variant="outline" onClick={() => set("env", [...f.env, ["", ""]])}><Plus /> Add</Button>}
          />
          <CardBody className="grid gap-2">
            {f.env.length === 0 && <p className="text-sm text-muted-foreground">No variables defined.</p>}
            {f.env.map(([k, v], i) => (
              <div key={i} className="grid grid-cols-[1fr_1.4fr_auto] gap-2">
                <Input className="font-mono" placeholder="NAME" value={k} onChange={(e) => set("env", f.env.map((p, j) => (j === i ? [e.target.value.toUpperCase(), p[1]] : p)))} />
                <Input className="font-mono" placeholder="value" value={v} onChange={(e) => set("env", f.env.map((p, j) => (j === i ? [p[0], e.target.value] : p)))} />
                <Button type="button" size="icon" variant="ghost" aria-label="Remove variable" onClick={() => set("env", f.env.filter((_, j) => j !== i))}><X /></Button>
              </div>
            ))}
          </CardBody>
        </Card>
        <ErrorBox error={save.error} />
        <div className="flex justify-end">
          <Button type="submit" loading={save.isPending}>Save & redeploy</Button>
        </div>
      </div>
    </form>
  );
}

function RedirectsTab({ d }: { d: Domain }) {
  const list = useRedirects(d.id);
  const [src, setSrc] = useState("");
  const [dst, setDst] = useState("");
  const [code, setCode] = useState("301");
  const inv = { invalidate: [keys.redirects(d.id)] };
  const add = useAction(() => api.post(`/domains/${d.id}/redirects`, { source_path: src, target_url: dst, status_code: Number(code) }), {
    success: "Redirect added",
    ...inv,
    onSuccess: () => { setSrc(""); setDst(""); },
  });
  const del = useAction((id: number) => api.del(`/domains/${d.id}/redirects/${id}`), { success: "Redirect removed", ...inv });
  return (
    <Card>
      <CardHeader title="Redirects" description="Applied at the edge before the request reaches your application." />
      <form className="grid gap-3 border-b p-4 sm:grid-cols-[1fr_1.5fr_8rem_auto] sm:items-end" onSubmit={(e) => { e.preventDefault(); add.mutate(undefined); }}>
        <Field label="From path"><Input className="font-mono" placeholder="/old-page" required value={src} onChange={(e) => setSrc(e.target.value)} /></Field>
        <Field label="To URL"><Input className="font-mono" placeholder="https://example.com/new" required value={dst} onChange={(e) => setDst(e.target.value)} /></Field>
        <Field label="Type">
          <Select value={code} onChange={(e) => setCode(e.target.value)}>
            <option value="301">301 permanent</option><option value="302">302 temporary</option>
            <option value="307">307 temporary</option><option value="308">308 permanent</option>
          </Select>
        </Field>
        <Button type="submit" loading={add.isPending}><Plus /> Add</Button>
      </form>
      {add.error && <div className="px-4 pt-3"><ErrorBox error={add.error} /></div>}
      {(list.data ?? []).length === 0 ? (
        <Empty icon={Link2} title="No redirects" />
      ) : (
        <Table>
          <thead><tr><Th>From</Th><Th>To</Th><Th>Code</Th><Th className="w-12" /></tr></thead>
          <tbody>
            {list.data!.map((r) => (
              <tr key={r.id}>
                <Td className="font-mono text-xs">{r.source_path}</Td>
                <Td className="break-all font-mono text-xs">{r.target_url}</Td>
                <Td><Badge>{r.status_code}</Badge></Td>
                <Td><Button size="icon" variant="ghost" aria-label="Delete redirect" onClick={() => del.mutate(r.id)}><Trash2 /></Button></Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </Card>
  );
}

function AliasesTab({ d }: { d: Domain }) {
  const domains = useDomains();
  const [open, setOpen] = useState<"alias" | "subdomain" | null>(null);
  const [toDelete, setToDelete] = useState<Domain | null>(null);
  const children = (domains.data ?? []).filter((x) => x.parent_id === d.id);
  const del = useAction((id: number) => api.del(`/domains/${id}`), { success: "Removed", invalidate: [keys.domains], onSuccess: () => setToDelete(null) });
  return (
    <Card>
      <CardHeader
        title="Aliases & subdomains"
        description="Aliases serve this website's content under another hostname; subdomains are separate websites."
        action={
          <div className="flex gap-2">
            <Button size="sm" variant="outline" onClick={() => setOpen("alias")}><Plus /> Alias</Button>
            {d.kind === "primary" && <Button size="sm" variant="outline" onClick={() => setOpen("subdomain")}><Plus /> Subdomain</Button>}
          </div>
        }
      />
      {children.length === 0 ? (
        <Empty icon={Link2} title="No aliases or subdomains" />
      ) : (
        <Table>
          <thead><tr><Th>Hostname</Th><Th>Type</Th><Th>Status</Th><Th className="w-12" /></tr></thead>
          <tbody>
            {children.map((c) => (
              <tr key={c.id}>
                <Td>{c.kind === "subdomain" ? <Link className="font-medium hover:underline" href={`/domains/view/?id=${c.id}`}>{c.name}</Link> : <span className="font-medium">{c.name}</span>}</Td>
                <Td className="capitalize">{c.kind}</Td>
                <Td><StatusBadge status={c.status} /></Td>
                <Td><Button size="icon" variant="ghost" aria-label={`Delete ${c.name}`} onClick={() => setToDelete(c)}><Trash2 /></Button></Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      <CreateDomainDialog open={!!open} onOpenChange={(o) => !o && setOpen(null)} defaultKind={open ?? "alias"} defaultParent={d} />
      <Confirm open={!!toDelete} onOpenChange={(o) => !o && setToDelete(null)} title={`Delete ${toDelete?.name}?`} description="Routing for this hostname is removed immediately." loading={del.isPending} onConfirm={() => del.mutate(toDelete!.id)} />
    </Card>
  );
}

function DomainView() {
  const id = Number(useSearchParams().get("id") ?? 0);
  const { data: d, isLoading, error } = useDomain(id);
  const [tab, setTab] = useState("overview");
  const [logKind, setLogKind] = useState<"access" | "error">("access");

  if (isLoading) return <div className="flex justify-center py-20"><Spinner /></div>;
  if (error || !d) return <ErrorBox error={error ?? new Error("Website not found")} />;

  return (
    <>
      <Link href="/domains/" className="mb-3 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
        <ArrowLeft className="size-4" /> Websites & Domains
      </Link>
      <PageHeader title={d.name} description={d.kind === "subdomain" ? "Subdomain" : "Website"} actions={<StatusBadge status={d.status} title={d.status_message} />} />
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList className="mb-4">
          <TabsTrigger value="overview"><Gauge /> Overview</TabsTrigger>
          <TabsTrigger value="files"><FolderOpen /> Files</TabsTrigger>
          <TabsTrigger value="settings"><Settings2 /> Settings</TabsTrigger>
          <TabsTrigger value="redirects"><Link2 /> Redirects</TabsTrigger>
          <TabsTrigger value="aliases"><Plus /> Aliases</TabsTrigger>
          <TabsTrigger value="logs"><ScrollText /> Logs</TabsTrigger>
        </TabsList>
        <TabsContent value="overview"><Overview d={d} /></TabsContent>
        <TabsContent value="files"><FileManager domainId={d.id} /></TabsContent>
        <TabsContent value="settings"><SettingsTab d={d} /></TabsContent>
        <TabsContent value="redirects"><RedirectsTab d={d} /></TabsContent>
        <TabsContent value="aliases"><AliasesTab d={d} /></TabsContent>
        <TabsContent value="logs">
          <Card>
            <CardHeader
              title="Live logs"
              action={
                <div className="flex gap-1">
                  <Button size="sm" variant={logKind === "access" ? "default" : "outline"} onClick={() => setLogKind("access")}><FileText /> Access</Button>
                  <Button size="sm" variant={logKind === "error" ? "default" : "outline"} disabled={d.runtime === "static"} onClick={() => setLogKind("error")}><ScrollText /> Application</Button>
                </div>
              }
            />
            <CardBody>{tab === "logs" && <LogViewer domainId={d.id} kind={logKind} />}</CardBody>
          </Card>
        </TabsContent>
      </Tabs>
    </>
  );
}

export default function DomainPage() {
  return (
    <Suspense fallback={<Spinner />}>
      <DomainView />
    </Suspense>
  );
}
