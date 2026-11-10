"use client";
import {
  Archive,
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
  SquareTerminal,
  Trash2,
  X,
} from "lucide-react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import dynamic from "next/dynamic";
import { Suspense, useEffect, useState } from "react";

import { CreateDomainDialog } from "@/components/create-domain-dialog";
import { FileManager } from "@/components/file-manager";
import { ItemBackups } from "@/components/item-backups";
import { LogViewer } from "@/components/log-viewer";
import { StatusBadge, stateLabels } from "@/components/status";
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
import { isAdmin, useAuth } from "@/lib/auth";

const WebTerminal = dynamic(() => import("@/components/web-terminal").then((m) => m.WebTerminal), {
  ssr: false,
  loading: () => (
    <div className="flex h-80 items-center justify-center">
      <Spinner />
    </div>
  ),
});
import { keys, useAction, useDomain, useDomainStats, useDomains, useRedirects, useRuntimes } from "@/lib/hooks";
import { fmtNumber, tk, tServer, useT } from "@/lib/i18n";
import type { Domain } from "@/lib/types";
import { bytes, dateTime, pct } from "@/lib/utils";

const KIND_LABELS: Record<string, string> = {
  primary: tk("Website"),
  subdomain: tk("Subdomain"),
  alias: tk("Alias"),
};

function Overview({ d }: { d: Domain }) {
  const { t } = useT();
  const router = useRouter();
  const hasContainer = d.runtime !== "static";
  const stats = useDomainStats(d.id, hasContainer && d.status === "running");
  const [del, setDel] = useState(false);
  const [purge, setPurge] = useState(false);
  const inv = [keys.domain(d.id), keys.domains];
  const action = useAction((a: string) => api.post(`/domains/${d.id}/actions/${a}`), { success: t("Request sent"), invalidate: inv });
  const remove = useAction(() => api.del(`/domains/${d.id}?purge=${purge}`), {
    success: t("{name} deleted", { name: d.name }),
    invalidate: [keys.domains],
    onSuccess: () => router.replace("/domains/"),
  });
  const scheme = d.ssl_enabled ? "https" : "http";

  return (
    <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_18rem]">
      <Card>
        <CardHeader title={t("Website")} description={t("Routing, runtime and resources at a glance.")} />
        <CardBody className="grid gap-x-8 gap-y-4 text-sm sm:grid-cols-2">
          <Info label={t("Address")}>
            <a href={`${scheme}://${d.name}`} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-primary hover:underline">
              {scheme}://{d.name} <ExternalLink className="size-3" />
            </a>
          </Info>
          <Info label={t("Status")}>
            <StatusBadge status={d.status} />
            {d.status_message && <p className={`mt-1 text-xs ${d.status === "error" ? "text-destructive" : "text-muted-foreground"}`}>{d.status_message}</p>}
          </Info>
          <Info label={t("Runtime")}>{d.runtime === "static" ? t("Static files (served by the edge)") : `${d.runtime} ${d.runtime_version}`}</Info>
          <Info label={t("Document root")}>
            <code className="text-xs">{d.document_root}/</code>
          </Info>
          {d.start_command && (
            <Info label={t("Start command")}>
              <code className="text-xs">{d.start_command}</code> → {t("port {port}", { port: String(d.app_port) })}
            </Info>
          )}
          <Info label="HTTPS">{d.ssl_enabled ? (d.force_https ? t("Enabled, HTTP redirects to HTTPS") : t("Enabled, HTTP also served")) : t("Disabled")}</Info>
          <Info label={t("Limits")}>
            {fmtNumber(d.memory_mb)} MB RAM · {fmtNumber(d.cpus)} CPU
          </Info>
          <Info label={t("Created")}>{dateTime(d.created_at)}</Info>
        </CardBody>
      </Card>
      <div className="grid content-start gap-4">
        <Card>
          <CardHeader title={t("Actions")} />
          <CardBody className="grid gap-2">
            <Button variant="outline" loading={action.isPending && action.variables === "deploy"} onClick={() => action.mutate("deploy")}>
              <Rocket /> {t("Redeploy")}
            </Button>
            {d.status === "stopped" ? (
              <Button variant="outline" onClick={() => action.mutate("start")}>
                <Play /> {t("Start")}
              </Button>
            ) : (
              <Button variant="outline" onClick={() => action.mutate("stop")}>
                <Square /> {t("Stop (maintenance page)")}
              </Button>
            )}
            {hasContainer && (
              <Button variant="outline" onClick={() => action.mutate("restart")}>
                <RotateCw /> {t("Restart container")}
              </Button>
            )}
            <Button variant="outline" className="text-destructive" onClick={() => setDel(true)}>
              <Trash2 /> {t("Delete website")}
            </Button>
          </CardBody>
        </Card>
        {hasContainer && (
          <Card>
            <CardHeader title={t("Container")} />
            <CardBody className="text-sm">
              {stats.data ? (
                <dl className="tabular grid grid-cols-2 gap-y-2">
                  <dt className="text-muted-foreground">CPU</dt>
                  <dd className="text-right">{pct(stats.data.cpu_percent, 1)}</dd>
                  <dt className="text-muted-foreground">{t("Memory")}</dt>
                  <dd className="text-right">
                    {bytes(stats.data.mem_usage)} / {bytes(stats.data.mem_limit)}
                  </dd>
                  <dt className="text-muted-foreground">{t("Network ↓/↑")}</dt>
                  <dd className="text-right">
                    {bytes(stats.data.net_rx)} / {bytes(stats.data.net_tx)}
                  </dd>
                  <dt className="text-muted-foreground">{t("Processes")}</dt>
                  <dd className="text-right">{stats.data.pids}</dd>
                  <dt className="text-muted-foreground">{t("State")}</dt>
                  <dd className={stateLabels[stats.data.state] ? "text-right" : "text-right capitalize"}>
                    {stateLabels[stats.data.state] ? t(stateLabels[stats.data.state]) : stats.data.state}
                  </dd>
                </dl>
              ) : (
                <p className="text-muted-foreground">{d.status === "running" ? t("Sampling…") : t("Not running.")}</p>
              )}
            </CardBody>
          </Card>
        )}
      </div>
      <Confirm
        open={del}
        onOpenChange={setDel}
        title={t("Delete {name}?", { name: d.name })}
        description={t("The container, routing and aliases are removed. Subdomains of this domain are deleted too.")}
        loading={remove.isPending}
        onConfirm={() => remove.mutate(undefined)}
      >
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={purge} onChange={(e) => setPurge(e.target.checked)} />
          {t("Also delete all files of this website (cannot be undone)")}
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
  const { t } = useT();
  const runtimes = useRuntimes();
  const [f, setF] = useState(() => ({
    ...d,
    env: Object.entries(d.env ?? {}),
  }));
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
    {
      success: t("Settings saved — redeploying"),
      invalidate: [keys.domain(d.id), keys.domains],
    },
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
        <CardHeader title={t("Runtime")} description={t("Changing the runtime recreates the container.")} />
        <CardBody className="grid gap-4">
          <div className="grid grid-cols-2 gap-3">
            <Field label={t("Runtime")}>
              <Select
                value={f.runtime}
                onChange={(e) => {
                  const r = runtimes.data?.find((x) => x.name === e.target.value);
                  setF((s) => ({
                    ...s,
                    runtime: e.target.value,
                    runtime_version: r?.default ?? "",
                    app_port: r?.needs_command ? s.app_port || r.default_port : 0,
                  }));
                }}
              >
                {(runtimes.data ?? []).map((r) => (
                  <option key={r.name} value={r.name}>
                    {tServer(r.label)}
                  </option>
                ))}
              </Select>
            </Field>
            {rt?.versions && (
              <Field label={t("Version")}>
                <Select value={f.runtime_version} onChange={(e) => set("runtime_version", e.target.value)}>
                  {rt.versions.map((v) => (
                    <option key={v}>{v}</option>
                  ))}
                </Select>
              </Field>
            )}
          </div>
          {rt?.needs_command ? (
            <div className="grid grid-cols-[1fr_7rem] gap-3">
              <Field label={t("Start command")} hint={t("Runs in the site folder; listen on $PORT.")}>
                <Input className="font-mono" required value={f.start_command} onChange={(e) => set("start_command", e.target.value)} />
              </Field>
              <Field label={t("Port")}>
                <Input type="number" min={1024} max={65535} required value={f.app_port} onChange={(e) => set("app_port", Number(e.target.value))} />
              </Field>
            </div>
          ) : (
            <Field label={t("Document root")} hint={t("Folder (inside the site directory) that is served publicly.")}>
              <Input className="font-mono" value={f.document_root} onChange={(e) => set("document_root", e.target.value)} />
            </Field>
          )}
          <div className="grid grid-cols-2 gap-3">
            <Field label={t("Memory limit (MB)")}>
              <Input type="number" min={64} max={65536} value={f.memory_mb} onChange={(e) => set("memory_mb", Number(e.target.value))} />
            </Field>
            <Field label={t("CPU limit (cores)")}>
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
                <span className="block font-medium">{t("Automatic certificate")}</span>
                <span className="text-xs text-muted-foreground">{t("Requires the domain's DNS to point at this server.")}</span>
              </span>
              <Switch
                checked={f.ssl_enabled}
                onCheckedChange={(v) =>
                  setF((s) => ({
                    ...s,
                    ssl_enabled: v,
                    force_https: v && s.force_https,
                  }))
                }
              />
            </label>
            <label className="flex items-center justify-between gap-4 text-sm">
              <span className="font-medium">{t("Redirect HTTP to HTTPS")}</span>
              <Switch disabled={!f.ssl_enabled} checked={f.force_https} onCheckedChange={(v) => set("force_https", v)} />
            </label>
          </CardBody>
        </Card>
        <Card>
          <CardHeader
            title={t("Environment variables")}
            description={t("Injected into the container.")}
            action={
              <Button type="button" size="sm" variant="outline" onClick={() => set("env", [...f.env, ["", ""]])}>
                <Plus /> {t("Add")}
              </Button>
            }
          />
          <CardBody className="grid gap-2">
            {f.env.length === 0 && <p className="text-sm text-muted-foreground">{t("No variables defined.")}</p>}
            {f.env.map(([k, v], i) => (
              <div key={i} className="grid grid-cols-[1fr_1.4fr_auto] gap-2">
                <Input
                  className="font-mono"
                  placeholder="NAME"
                  value={k}
                  onChange={(e) =>
                    set(
                      "env",
                      f.env.map((p, j) => (j === i ? [e.target.value.toUpperCase(), p[1]] : p)),
                    )
                  }
                />
                <Input
                  className="font-mono"
                  placeholder={t("value")}
                  value={v}
                  onChange={(e) =>
                    set(
                      "env",
                      f.env.map((p, j) => (j === i ? [p[0], e.target.value] : p)),
                    )
                  }
                />
                <Button
                  type="button"
                  size="icon"
                  variant="ghost"
                  aria-label={t("Remove variable")}
                  onClick={() =>
                    set(
                      "env",
                      f.env.filter((_, j) => j !== i),
                    )
                  }
                >
                  <X />
                </Button>
              </div>
            ))}
          </CardBody>
        </Card>
        <ErrorBox error={save.error} />
        <div className="flex justify-end">
          <Button type="submit" loading={save.isPending}>
            {t("Save & redeploy")}
          </Button>
        </div>
      </div>
    </form>
  );
}

function RedirectsTab({ d }: { d: Domain }) {
  const { t } = useT();
  const list = useRedirects(d.id);
  const [src, setSrc] = useState("");
  const [dst, setDst] = useState("");
  const [code, setCode] = useState("301");
  const inv = { invalidate: [keys.redirects(d.id)] };
  const add = useAction(
    () =>
      api.post(`/domains/${d.id}/redirects`, {
        source_path: src,
        target_url: dst,
        status_code: Number(code),
      }),
    {
      success: t("Redirect added"),
      ...inv,
      onSuccess: () => {
        setSrc("");
        setDst("");
      },
    },
  );
  const del = useAction((id: number) => api.del(`/domains/${d.id}/redirects/${id}`), { success: t("Redirect removed"), ...inv });
  return (
    <Card>
      <CardHeader title={t("Redirects")} description={t("Applied at the edge before the request reaches your application.")} />
      <form
        className="grid gap-3 border-b p-4 sm:grid-cols-[1fr_1.5fr_8rem_auto] sm:items-end"
        onSubmit={(e) => {
          e.preventDefault();
          add.mutate(undefined);
        }}
      >
        <Field label={t("From path")}>
          <Input className="font-mono" placeholder="/old-page" required value={src} onChange={(e) => setSrc(e.target.value)} />
        </Field>
        <Field label={t("To URL")}>
          <Input className="font-mono" placeholder="https://example.com/new" required value={dst} onChange={(e) => setDst(e.target.value)} />
        </Field>
        <Field label={t("Type")}>
          <Select value={code} onChange={(e) => setCode(e.target.value)}>
            <option value="301">{t("301 permanent")}</option>
            <option value="302">{t("302 temporary")}</option>
            <option value="307">{t("307 temporary")}</option>
            <option value="308">{t("308 permanent")}</option>
          </Select>
        </Field>
        <Button type="submit" loading={add.isPending}>
          <Plus /> {t("Add")}
        </Button>
      </form>
      {add.error && (
        <div className="px-4 pt-3">
          <ErrorBox error={add.error} />
        </div>
      )}
      {(list.data ?? []).length === 0 ? (
        <Empty icon={Link2} title={t("No redirects")} />
      ) : (
        <Table>
          <thead>
            <tr>
              <Th>{t("From")}</Th>
              <Th>{t("To")}</Th>
              <Th>{t("Code")}</Th>
              <Th className="w-12" />
            </tr>
          </thead>
          <tbody>
            {list.data!.map((r) => (
              <tr key={r.id}>
                <Td className="font-mono text-xs">{r.source_path}</Td>
                <Td className="break-all font-mono text-xs">{r.target_url}</Td>
                <Td>
                  <Badge>{r.status_code}</Badge>
                </Td>
                <Td>
                  <Button size="icon" variant="ghost" aria-label={t("Delete redirect")} onClick={() => del.mutate(r.id)}>
                    <Trash2 />
                  </Button>
                </Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </Card>
  );
}

function AliasesTab({ d }: { d: Domain }) {
  const { t } = useT();
  const domains = useDomains();
  const [open, setOpen] = useState<"alias" | "subdomain" | null>(null);
  const [toDelete, setToDelete] = useState<Domain | null>(null);
  const children = (domains.data ?? []).filter((x) => x.parent_id === d.id);
  const del = useAction((id: number) => api.del(`/domains/${id}`), {
    success: t("Removed"),
    invalidate: [keys.domains],
    onSuccess: () => setToDelete(null),
  });
  return (
    <Card>
      <CardHeader
        title={t("Aliases & subdomains")}
        description={t("Aliases serve this website's content under another hostname; subdomains are separate websites.")}
        action={
          <div className="flex gap-2">
            <Button size="sm" variant="outline" onClick={() => setOpen("alias")}>
              <Plus /> {t("Alias")}
            </Button>
            {d.kind === "primary" && (
              <Button size="sm" variant="outline" onClick={() => setOpen("subdomain")}>
                <Plus /> {t("Subdomain")}
              </Button>
            )}
          </div>
        }
      />
      {children.length === 0 ? (
        <Empty icon={Link2} title={t("No aliases or subdomains")} />
      ) : (
        <Table>
          <thead>
            <tr>
              <Th>{t("Hostname")}</Th>
              <Th>{t("Type")}</Th>
              <Th>{t("Status")}</Th>
              <Th className="w-12" />
            </tr>
          </thead>
          <tbody>
            {children.map((c) => (
              <tr key={c.id}>
                <Td>
                  {c.kind === "subdomain" ? (
                    <Link className="font-medium hover:underline" href={`/domains/view/?id=${c.id}`}>
                      {c.name}
                    </Link>
                  ) : (
                    <span className="font-medium">{c.name}</span>
                  )}
                </Td>
                <Td>{t(KIND_LABELS[c.kind] ?? c.kind)}</Td>
                <Td>
                  <StatusBadge status={c.status} />
                </Td>
                <Td>
                  <Button size="icon" variant="ghost" aria-label={t("Delete {name}", { name: c.name })} onClick={() => setToDelete(c)}>
                    <Trash2 />
                  </Button>
                </Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      <CreateDomainDialog open={!!open} onOpenChange={(o) => !o && setOpen(null)} defaultKind={open ?? "alias"} defaultParent={d} />
      <Confirm
        open={!!toDelete}
        onOpenChange={(o) => !o && setToDelete(null)}
        title={t("Delete {name}?", { name: toDelete?.name ?? "" })}
        description={t("Routing for this hostname is removed immediately.")}
        loading={del.isPending}
        onConfirm={() => del.mutate(toDelete!.id)}
      />
    </Card>
  );
}

function DomainView() {
  const { t } = useT();
  const id = Number(useSearchParams().get("id") ?? 0);
  const { data: d, isLoading, error } = useDomain(id);
  const [tab, setTab] = useState("overview");
  const [logKind, setLogKind] = useState<"access" | "error">("access");
  const { user } = useAuth();

  if (isLoading)
    return (
      <div className="flex justify-center py-20">
        <Spinner />
      </div>
    );
  if (error || !d) return <ErrorBox error={error ?? new Error(t("Website not found"))} />;

  return (
    <>
      <Link href="/domains/" className="mb-3 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
        <ArrowLeft className="size-4" /> {t("Websites & Domains")}
      </Link>
      <PageHeader
        title={d.name}
        description={d.kind === "subdomain" ? t("Subdomain") : t("Website")}
        actions={<StatusBadge status={d.status} title={d.status_message} />}
      />
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList className="mb-4">
          <TabsTrigger value="overview">
            <Gauge /> {t("Overview")}
          </TabsTrigger>
          <TabsTrigger value="files">
            <FolderOpen /> {t("Files")}
          </TabsTrigger>
          <TabsTrigger value="settings">
            <Settings2 /> {t("Settings")}
          </TabsTrigger>
          <TabsTrigger value="redirects">
            <Link2 /> {t("Redirects")}
          </TabsTrigger>
          <TabsTrigger value="aliases">
            <Plus /> {t("Aliases")}
          </TabsTrigger>
          <TabsTrigger value="terminal">
            <SquareTerminal /> {t("Terminal")}
          </TabsTrigger>
          <TabsTrigger value="logs">
            <ScrollText /> {t("Logs")}
          </TabsTrigger>
          <TabsTrigger value="backups">
            <Archive /> {t("Backups")}
          </TabsTrigger>
        </TabsList>
        <TabsContent value="overview">
          <Overview d={d} />
        </TabsContent>
        <TabsContent value="files">
          <FileManager domainId={d.id} />
        </TabsContent>
        <TabsContent value="settings">
          <SettingsTab d={d} />
        </TabsContent>
        <TabsContent value="redirects">
          <RedirectsTab d={d} />
        </TabsContent>
        <TabsContent value="aliases">
          <AliasesTab d={d} />
        </TabsContent>
        <TabsContent value="terminal">
          <Card>
            <CardHeader
              title={t("Terminal")}
              description={
                d.runtime === "static"
                  ? t("A shell with git, curl, rsync, editors and database clients, with this website's files mounted.")
                  : t("A shell inside this website's {runtime} container — run composer, npm, artisan, migrations…", {
                      runtime: `${d.runtime} ${d.runtime_version}`,
                    })
              }
            />
            <CardBody>{tab === "terminal" && <WebTerminal domainId={d.id} canRoot={isAdmin(user)} />}</CardBody>
          </Card>
        </TabsContent>
        <TabsContent value="backups">
          <Card>
            <CardHeader
              title={t("Backups")}
              description={t("Restore points of this website's files. Databases are backed up and restored on the Databases page.")}
            />
            <CardBody>
              {tab === "backups" && (
                <ItemBackups
                  base={`/domains/${d.id}`}
                  label={d.name}
                  warning={t(
                    "All files of this website are replaced with the backup. Files created or changed since then are lost. The site is redeployed afterwards.",
                  )}
                />
              )}
            </CardBody>
          </Card>
        </TabsContent>
        <TabsContent value="logs">
          <Card>
            <CardHeader
              title={t("Live logs")}
              action={
                <div className="flex gap-1">
                  <Button size="sm" variant={logKind === "access" ? "default" : "outline"} onClick={() => setLogKind("access")}>
                    <FileText /> {t("Access")}
                  </Button>
                  <Button size="sm" variant={logKind === "error" ? "default" : "outline"} disabled={d.runtime === "static"} onClick={() => setLogKind("error")}>
                    <ScrollText /> {t("Application")}
                  </Button>
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
