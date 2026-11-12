"use client";
import { Archive, Check, Copy, Eye, HardDrive, Info, KeyRound, Loader2, Plug, Server, ShieldCheck, TriangleAlert } from "lucide-react";
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
import { useT } from "@/lib/i18n";
import { keys, useAction, useBackupRuns, useBackupSettings, useDatabases, useDomains, useUsers } from "@/lib/hooks";
import type { BackupSettings } from "@/lib/types";
import { ago, bytes, dateTime } from "@/lib/utils";

type Form = BackupSettings & { s3_secret: string };

/** Says whether the saved repository would survive losing the data's disk or the server. */
function OffsiteNotice({ o }: { o: BackupSettings["offsite"] }) {
  const { t } = useT();
  switch (o.level) {
    case "same_disk":
    case "same_server":
      return (
        <div className="flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">
          <TriangleAlert className="mt-0.5 size-4 shrink-0" />
          <p>
            {o.level === "same_disk"
              ? t("The backups are on the same disk as the data they protect ({detail}): if that disk fails, the websites and their backups are lost together.", { detail: o.detail })
              : t("The S3 storage ({detail}) runs on this server: if the server is lost, the backups go with it.", { detail: o.detail })}{" "}
            {t("Keep them on S3-compatible storage elsewhere, a network share, or at least another disk.")}
          </p>
        </div>
      );
    case "other_disk":
      return (
        <div className="flex items-start gap-2 rounded-md border bg-muted/50 p-3 text-sm text-muted-foreground">
          <Info className="mt-0.5 size-4 shrink-0" />
          <p>
            {t(
              "The backups are on another disk of this server ({detail}): they survive a failure of the data disk, but not losing the whole server. A copy elsewhere (S3-compatible storage, another machine) covers that too.",
              { detail: o.detail },
            )}
          </p>
        </div>
      );
    case "remote":
      return (
        <p className="flex items-center gap-2 text-sm text-muted-foreground">
          <ShieldCheck className="size-4 shrink-0 text-success" /> {t("The backups are stored outside this server ({detail}).", { detail: o.detail })}
        </p>
      );
  }
  return null;
}

function SettingsCard() {
  const { t } = useT();
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
    { success: t("Backup settings saved"), invalidate: [keys.backupSettings] },
  );
  const test = useAction(() => api.post<{ location: string }>("/backups/test"), {
    onSuccess: (r) => toast.success(t("Repository ready at {location}", { location: r.location })),
  });

  if (isLoading || !f) return <Card className="flex justify-center p-10">{error ? <ErrorBox error={error} /> : <Spinner />}</Card>;

  return (
    <Card>
      <CardHeader title={t("Storage & schedule")} description={<>{t("Encrypted, deduplicated, incremental backups (restic). Current repository:")} <code className="text-xs">{data?.location}</code></>} />
      <form
        className="grid gap-5 p-5"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate(undefined);
        }}
      >
        {data && <OffsiteNotice o={data.offsite} />}
        <div className="grid grid-cols-2 gap-2" role="radiogroup" aria-label={t("Destination")}>
          {([
            ["local", HardDrive, t("This server"), t("A local directory or a mounted disk")],
            ["s3", Server, t("S3-compatible"), "AWS S3, Backblaze B2, Wasabi, MinIO…"],
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
          <Field
            label={t("Directory")}
            hint={t("Leave empty for {path}. Use a separate disk if you can — a backup on the same disk does not survive a disk failure.", { path: "/var/lib/openplex/backups/repo" })}
          >
            <Input className="font-mono" placeholder="/mnt/backups/openplex" value={f.local_path} onChange={(e) => set("local_path", e.target.value)} />
          </Field>
        ) : (
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label={t("Endpoint")} className="sm:col-span-2"><Input className="font-mono" required placeholder="https://s3.eu-central-1.amazonaws.com" value={f.s3_endpoint} onChange={(e) => set("s3_endpoint", e.target.value)} /></Field>
            <Field label={t("Bucket")}><Input className="font-mono" required value={f.s3_bucket} onChange={(e) => set("s3_bucket", e.target.value)} /></Field>
            <Field label={t("Path prefix")}><Input className="font-mono" value={f.s3_prefix} onChange={(e) => set("s3_prefix", e.target.value)} /></Field>
            <Field label={t("Access key")}><Input className="font-mono" required autoComplete="off" value={f.s3_access_key} onChange={(e) => set("s3_access_key", e.target.value)} /></Field>
            <Field label={t("Secret key")} hint={data?.s3_secret_set ? t("Stored encrypted. Leave empty to keep it.") : undefined}>
              <Input type="password" autoComplete="new-password" required={!data?.s3_secret_set} value={f.s3_secret} onChange={(e) => set("s3_secret", e.target.value)} />
            </Field>
            <Field label={t("Region (optional)")}><Input className="font-mono" placeholder="eu-central-1" value={f.s3_region} onChange={(e) => set("s3_region", e.target.value)} /></Field>
          </div>
        )}

        <div className="grid gap-4 border-t pt-5 sm:grid-cols-[auto_1fr] sm:items-end">
          <label className="flex items-center gap-3 text-sm font-medium">
            <Switch checked={f.schedule_enabled} onCheckedChange={(v) => set("schedule_enabled", v)} /> {t("Daily backup at")}
          </label>
          <Input type="time" required className="max-w-32" disabled={!f.schedule_enabled} value={f.schedule_time} onChange={(e) => set("schedule_time", e.target.value)} />
        </div>
        <div className="grid grid-cols-3 gap-3">
          <Field label={t("Keep daily")}><Input type="number" min={0} max={1000} value={f.keep_daily} onChange={(e) => set("keep_daily", Number(e.target.value))} /></Field>
          <Field label={t("Keep weekly")}><Input type="number" min={0} max={1000} value={f.keep_weekly} onChange={(e) => set("keep_weekly", Number(e.target.value))} /></Field>
          <Field label={t("Keep monthly")}><Input type="number" min={0} max={1000} value={f.keep_monthly} onChange={(e) => set("keep_monthly", Number(e.target.value))} /></Field>
        </div>
        {data?.last_scheduled_at && <p className="text-xs text-muted-foreground">{t("Last scheduled backup started {when}.", { when: ago(data.last_scheduled_at) })}</p>}
        <ErrorBox error={save.error ?? test.error} />
        <div className="flex flex-wrap justify-end gap-2">
          <Button type="button" variant="outline" loading={test.isPending} onClick={() => test.mutate(undefined)}><Plug /> {t("Test repository")}</Button>
          <Button type="submit" loading={save.isPending}>{t("Save settings")}</Button>
        </div>
      </form>
    </Card>
  );
}

function RecoveryKeyCard() {
  const { t } = useT();
  const [key, setKey] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const reveal = useAction(() => api.get<{ key: string }>("/backups/recovery-key"), { onSuccess: (r) => setKey(r.key) });
  return (
    <Card>
      <CardHeader title={t("Recovery key")} description={t("Backups are encrypted with this key. Store it outside this server (password manager) — without it, backups cannot be restored on a new machine.")} />
      <CardBody>
        {key ? (
          <div className="flex gap-2">
            <Input readOnly className="font-mono text-xs" value={key} onFocus={(e) => e.target.select()} />
            <Button
              size="icon"
              variant="outline"
              aria-label={t("Copy recovery key")}
              onClick={() => navigator.clipboard.writeText(key).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1500); })}
            >
              {copied ? <Check /> : <Copy />}
            </Button>
          </div>
        ) : (
          <Button variant="outline" loading={reveal.isPending} onClick={() => reveal.mutate(undefined)}><Eye /> {t("Reveal recovery key")}</Button>
        )}
        <p className="mt-4 text-sm text-muted-foreground">
          {t("If this server is lost: install OpenPlex on a new one, stop it, and run this command with the repository and this key. Websites, databases and mail are then restored when it starts.")}
        </p>
        <pre className="mt-2 overflow-x-auto rounded-md bg-muted p-3 font-mono text-xs">
          {"systemctl stop openplexd\nopenplexd restore-panel --repo <repository>\nsystemctl start openplexd"}
        </pre>
      </CardBody>
    </Card>
  );
}

function RunsCard() {
  const { t } = useT();
  const runs = useBackupRuns();
  const domains = useDomains();
  const dbs = useDatabases();
  const users = useUsers();
  const start = useAction((scope: string) => api.post("/backups/run", { scope }), { success: t("Backup started"), invalidate: [keys.backupRuns] });
  const running = runs.data?.some((r) => r.status === "running");
  const scopeLabel = (s: string) => {
    if (s === "all") return t("Everything");
    if (s === "panel") return t("Panel configuration");
    const [kind, id] = s.split(":");
    if (kind === "site") return domains.data?.find((d) => String(d.id) === id)?.name ?? t("website #{id}", { id });
    if (kind === "app")
      return t("{site} and its application's database", { site: domains.data?.find((d) => String(d.id) === id)?.name ?? t("website #{id}", { id }) });
    if (kind === "db") return dbs.data?.find((d) => String(d.id) === id)?.name ?? t("database #{id}", { id });
    return s;
  };
  const who = (id: number | null) => (id ? users.data?.find((u) => u.id === id)?.username : t("scheduler"));
  return (
    <Card>
      <CardHeader
        title={t("Activity")}
        description={t("Backups and restores, newest first.")}
        action={
          <Button size="sm" disabled={running} loading={start.isPending} onClick={() => start.mutate("all")}>
            {running ? <Loader2 className="animate-spin" /> : <Archive />} {t("Back up everything now")}
          </Button>
        }
      />
      {start.error && <div className="px-5 pt-3"><ErrorBox error={start.error} /></div>}
      {runs.isLoading ? (
        <div className="flex justify-center py-10"><Spinner /></div>
      ) : !runs.data?.length ? (
        <Empty icon={Archive} title={t("No backups have run yet")} />
      ) : (
        <Table>
          <thead>
            <tr><Th>{t("Started")}</Th><Th>{t("What")}</Th><Th>{t("Status")}</Th><Th className="text-right">{t("New data")}</Th><Th>{t("By")}</Th></tr>
          </thead>
          <tbody className="tabular">
            {runs.data.map((r) => (
              <tr key={r.id}>
                <Td className="whitespace-nowrap">
                  <div>{dateTime(r.started_at)}</div>
                  <div className="text-xs text-muted-foreground">
                    {r.finished_at ? t("took {seconds} s", { seconds: Math.max(1, Math.round((Date.parse(r.finished_at) - Date.parse(r.started_at)) / 1000)) }) : t("in progress")}
                  </div>
                </Td>
                <Td>
                  <span>{r.kind === "restore" ? t("Restoration") : t("Backup")}</span> · {scopeLabel(r.scope)}
                  {r.message && <div className={`max-w-md truncate text-xs ${r.status === "error" ? "text-destructive" : "text-muted-foreground"}`} title={r.message}>{r.message}</div>}
                </Td>
                <Td><RunStatus run={r} /></Td>
                <Td className="text-right">{r.kind === "backup" && r.status === "success" ? bytes(r.bytes_added) : "—"}</Td>
                <Td className="text-muted-foreground">{who(r.user_id)}{r.trigger === "scheduled" && <Badge className="ml-2">{t("scheduled")}</Badge>}
                  {r.trigger === "app_update" && <Badge className="ml-2">{t("before an update")}</Badge>}
                  {r.trigger === "staging_push" && <Badge className="ml-2">{t("before a staging push")}</Badge>}</Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </Card>
  );
}

export default function BackupsPage() {
  const { t } = useT();
  return (
    <>
      <PageHeader
        title={t("Backups")}
        description={t("Websites, databases and panel configuration — each restorable on its own from its page.")}
        actions={<span className="flex items-center gap-1.5 text-sm text-muted-foreground"><KeyRound className="size-4" /> {t("AES-256 encrypted")}</span>}
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
