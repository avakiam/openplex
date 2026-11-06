"use client";
import { Archive, Check, Copy, Eye, HardDrive, KeyRound, Loader2, Plug, Server } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";

import { RunStatus } from "@/components/item-backups";
import {
  Badge,
  Button,
  Card,
  CardBody,
  CardHeader,
  Empty,
  ErrorBox,
  Field,
  Input,
  PageHeader,
  Spinner,
  Switch,
  Table,
  Td,
  Th,
} from "@/components/ui";
import { api } from "@/lib/api";
import { keys, useAction, useBackupRuns, useBackupSettings, useDatabases, useDomains, useUsers } from "@/lib/hooks";
import type { BackupSettings } from "@/lib/types";
import { ago, bytes, dateTime } from "@/lib/utils";

type Form = BackupSettings & { s3_secret: string };

function SettingsCard() {
  const { data, isLoading, error } = useBackupSettings();
  const [f, setF] = useState<Form | null>(null);
  useEffect(() => {
    if (data) setF({ ...data, s3_secret: "" });
  }, [data]);
  const set = <K extends keyof Form>(k: K, v: Form[K]) => setF((s) => (s ? { ...s, [k]: v } : s));
  const save = useAction(
    () =>
      api.put("/backups/settings", {
        target: f!.target,
        local_path: f!.local_path,
        s3_endpoint: f!.s3_endpoint,
        s3_bucket: f!.s3_bucket,
        s3_prefix: f!.s3_prefix,
        s3_region: f!.s3_region,
        s3_access_key: f!.s3_access_key,
        s3_secret: f!.s3_secret || undefined,
        schedule_enabled: f!.schedule_enabled,
        schedule_time: f!.schedule_time,
        keep_daily: Number(f!.keep_daily),
        keep_weekly: Number(f!.keep_weekly),
        keep_monthly: Number(f!.keep_monthly),
      }),
    { success: "Backup settings saved", invalidate: [keys.backupSettings] },
  );
  const test = useAction(() => api.post<{ location: string }>("/backups/test"), {
    onSuccess: (r) => toast.success(`Repository ready at ${r.location}`),
  });

  if (isLoading || !f) return <Card className="flex justify-center p-10">{error ? <ErrorBox error={error} /> : <Spinner />}</Card>;

  return (
    <Card>
      <CardHeader title="Storage & schedule" description={<>Encrypted, deduplicated, incremental backups (restic). Current repository: <code className="text-xs">{data?.location}</code></>} />
      <form
        className="grid gap-5 p-5"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate(undefined);
        }}
      >
        <div className="grid grid-cols-2 gap-2" role="radiogroup" aria-label="Destination">
          {([
            ["local", HardDrive, "This server", "A local directory or a mounted disk"],
            ["s3", Server, "S3-compatible", "AWS S3, Backblaze B2, Wasabi, MinIO…"],
          ] as const).map(([v, Icon, title, sub]) => (
            <button
              key={v}
              type="button"
              role="radio"
              aria-checked={f.target === v}
              onClick={() => set("target", v)}
              className={`flex items-start gap-3 rounded-md border p-3 text-left ${f.target === v ? "border-primary bg-accent text-accent-foreground" : "hover:bg-muted"}`}
            >
              <Icon className="mt-0.5 size-4 shrink-0" />
              <span>
                <span className="block text-sm font-medium">{title}</span>
                <span className="text-xs text-muted-foreground">{sub}</span>
              </span>
            </button>
          ))}
        </div>

        {f.target === "local" ? (
          <Field label="Directory" hint="Leave empty for /var/lib/openplex/backups/repo. Use a separate disk if you can — a backup on the same disk does not survive a disk failure.">
            <Input className="font-mono" placeholder="/mnt/backups/openplex" value={f.local_path} onChange={(e) => set("local_path", e.target.value)} />
          </Field>
        ) : (
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Endpoint" className="sm:col-span-2"><Input className="font-mono" required placeholder="https://s3.eu-central-1.amazonaws.com" value={f.s3_endpoint} onChange={(e) => set("s3_endpoint", e.target.value)} /></Field>
            <Field label="Bucket"><Input className="font-mono" required value={f.s3_bucket} onChange={(e) => set("s3_bucket", e.target.value)} /></Field>
            <Field label="Path prefix"><Input className="font-mono" value={f.s3_prefix} onChange={(e) => set("s3_prefix", e.target.value)} /></Field>
            <Field label="Access key"><Input className="font-mono" required autoComplete="off" value={f.s3_access_key} onChange={(e) => set("s3_access_key", e.target.value)} /></Field>
            <Field label="Secret key" hint={data?.s3_secret_set ? "Stored encrypted. Leave empty to keep it." : undefined}>
              <Input type="password" autoComplete="new-password" required={!data?.s3_secret_set} value={f.s3_secret} onChange={(e) => set("s3_secret", e.target.value)} />
            </Field>
            <Field label="Region (optional)"><Input className="font-mono" placeholder="eu-central-1" value={f.s3_region} onChange={(e) => set("s3_region", e.target.value)} /></Field>
          </div>
        )}

        <div className="grid gap-4 border-t pt-5 sm:grid-cols-[auto_1fr] sm:items-end">
          <label className="flex items-center gap-3 text-sm font-medium">
            <Switch checked={f.schedule_enabled} onCheckedChange={(v) => set("schedule_enabled", v)} /> Daily backup at
          </label>
          <Input type="time" required className="max-w-32" disabled={!f.schedule_enabled} value={f.schedule_time} onChange={(e) => set("schedule_time", e.target.value)} />
        </div>
        <div className="grid grid-cols-3 gap-3">
          <Field label="Keep daily"><Input type="number" min={0} max={1000} value={f.keep_daily} onChange={(e) => set("keep_daily", Number(e.target.value))} /></Field>
          <Field label="Keep weekly"><Input type="number" min={0} max={1000} value={f.keep_weekly} onChange={(e) => set("keep_weekly", Number(e.target.value))} /></Field>
          <Field label="Keep monthly"><Input type="number" min={0} max={1000} value={f.keep_monthly} onChange={(e) => set("keep_monthly", Number(e.target.value))} /></Field>
        </div>
        {data?.last_scheduled_at && <p className="text-xs text-muted-foreground">Last scheduled backup started {ago(data.last_scheduled_at)}.</p>}
        <ErrorBox error={save.error ?? test.error} />
        <div className="flex flex-wrap justify-end gap-2">
          <Button type="button" variant="outline" loading={test.isPending} onClick={() => test.mutate(undefined)}><Plug /> Test repository</Button>
          <Button type="submit" loading={save.isPending}>Save settings</Button>
        </div>
      </form>
    </Card>
  );
}

function RecoveryKeyCard() {
  const [key, setKey] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const reveal = useAction(() => api.get<{ key: string }>("/backups/recovery-key"), { onSuccess: (r) => setKey(r.key) });
  return (
    <Card>
      <CardHeader title="Recovery key" description="Backups are encrypted with this key. Store it outside this server (password manager) — without it, backups cannot be restored on a new machine." />
      <CardBody>
        {key ? (
          <div className="flex gap-2">
            <Input readOnly className="font-mono text-xs" value={key} onFocus={(e) => e.target.select()} />
            <Button
              size="icon"
              variant="outline"
              aria-label="Copy recovery key"
              onClick={() => navigator.clipboard.writeText(key).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1500); })}
            >
              {copied ? <Check /> : <Copy />}
            </Button>
          </div>
        ) : (
          <Button variant="outline" loading={reveal.isPending} onClick={() => reveal.mutate(undefined)}><Eye /> Reveal recovery key</Button>
        )}
      </CardBody>
    </Card>
  );
}

function RunsCard() {
  const runs = useBackupRuns();
  const domains = useDomains();
  const dbs = useDatabases();
  const users = useUsers();
  const start = useAction((scope: string) => api.post("/backups/run", { scope }), { success: "Backup started", invalidate: [keys.backupRuns] });
  const running = runs.data?.some((r) => r.status === "running");
  const scopeLabel = (s: string) => {
    if (s === "all") return "Everything";
    if (s === "panel") return "Panel configuration";
    const [kind, id] = s.split(":");
    if (kind === "site") return domains.data?.find((d) => String(d.id) === id)?.name ?? `website #${id}`;
    if (kind === "db") return dbs.data?.find((d) => String(d.id) === id)?.name ?? `database #${id}`;
    return s;
  };
  const who = (id: number | null) => (id ? users.data?.find((u) => u.id === id)?.username : "scheduler");
  return (
    <Card>
      <CardHeader
        title="Activity"
        description="Backups and restores, newest first."
        action={
          <Button size="sm" disabled={running} loading={start.isPending} onClick={() => start.mutate("all")}>
            {running ? <Loader2 className="animate-spin" /> : <Archive />} Back up everything now
          </Button>
        }
      />
      {start.error && <div className="px-5 pt-3"><ErrorBox error={start.error} /></div>}
      {runs.isLoading ? (
        <div className="flex justify-center py-10"><Spinner /></div>
      ) : !runs.data?.length ? (
        <Empty icon={Archive} title="No backups have run yet" />
      ) : (
        <Table>
          <thead>
            <tr><Th>Started</Th><Th>What</Th><Th>Status</Th><Th className="text-right">New data</Th><Th>By</Th></tr>
          </thead>
          <tbody className="tabular">
            {runs.data.map((r) => (
              <tr key={r.id}>
                <Td className="whitespace-nowrap">
                  <div>{dateTime(r.started_at)}</div>
                  <div className="text-xs text-muted-foreground">
                    {r.finished_at ? `took ${Math.max(1, Math.round((Date.parse(r.finished_at) - Date.parse(r.started_at)) / 1000))}s` : "in progress"}
                  </div>
                </Td>
                <Td>
                  <span className="capitalize">{r.kind}</span> · {scopeLabel(r.scope)}
                  {r.message && <div className={`max-w-md truncate text-xs ${r.status === "error" ? "text-destructive" : "text-muted-foreground"}`} title={r.message}>{r.message}</div>}
                </Td>
                <Td><RunStatus run={r} /></Td>
                <Td className="text-right">{r.kind === "backup" && r.status === "success" ? bytes(r.bytes_added) : "—"}</Td>
                <Td className="text-muted-foreground">{who(r.user_id)}{r.trigger === "scheduled" && <Badge className="ml-2">scheduled</Badge>}</Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </Card>
  );
}

export default function BackupsPage() {
  return (
    <>
      <PageHeader
        title="Backups"
        description="Websites, databases and panel configuration — each restorable on its own from its page."
        actions={<span className="flex items-center gap-1.5 text-sm text-muted-foreground"><KeyRound className="size-4" /> AES-256 encrypted</span>}
      />
      <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
        <div className="grid content-start gap-4">
          <SettingsCard />
          <RecoveryKeyCard />
        </div>
        <RunsCard />
      </div>
    </>
  );
}
