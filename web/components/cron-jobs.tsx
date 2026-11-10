"use client";
import {
  CalendarClock,
  CheckCircle2,
  ChevronRight,
  CircleDashed,
  History,
  Loader2,
  MoreHorizontal,
  Pencil,
  Play,
  Plus,
  SkipForward,
  TimerOff,
  Trash2,
  XCircle,
} from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";

import { CRON_PRESETS, describeSchedule } from "@/lib/cron";
import { useCreateCron, useCronJobs, useCronPreview, useCronRuns, useDeleteCron, useRunCron, useUpdateCron } from "@/lib/hooks";
import { fmtNumber, tk, useT } from "@/lib/i18n";
import type { CronJob, CronRun, CronStatus, Domain } from "@/lib/types";
import { ago, dateTime, duration } from "@/lib/utils";

import {
  Badge,
  Button,
  Card,
  CardHeader,
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
  Select,
  Spinner,
  Switch,
  Table,
  Td,
  Textarea,
  Th,
} from "./ui";

// Status always ships with icon + label, never colour alone.
const statusMap: Record<
  CronStatus,
  { tone: "success" | "warning" | "danger" | "neutral" | "info"; label: string; icon: React.ComponentType<{ className?: string }> }
> = {
  running: { tone: "info", label: tk("Running"), icon: Loader2 },
  success: { tone: "success", label: tk("Succeeded"), icon: CheckCircle2 },
  failed: { tone: "danger", label: tk("Failed"), icon: XCircle },
  timeout: { tone: "warning", label: tk("Timed out"), icon: TimerOff },
  skipped: { tone: "neutral", label: tk("Skipped"), icon: SkipForward },
};

const TIMEOUTS: { sec: number; label: string }[] = [
  { sec: 60, label: tk("1 minute") },
  { sec: 300, label: tk("5 minutes") },
  { sec: 900, label: tk("15 minutes") },
  { sec: 3600, label: tk("1 hour") },
  { sec: 10800, label: tk("3 hours") },
  { sec: 21600, label: tk("6 hours") },
];

const EXAMPLES: { label: string; name: string; schedule: string; command: string }[] = [
  { label: tk("Laravel scheduler, every minute"), name: tk("Laravel scheduler"), schedule: "* * * * *", command: "php artisan schedule:run" },
  { label: tk("WordPress cron, every 5 minutes"), name: tk("WordPress cron"), schedule: "*/5 * * * *", command: "php wp-cron.php" },
  { label: tk("A backup script, nightly"), name: tk("Nightly backup"), schedule: "0 3 * * *", command: "bash scripts/backup.sh" },
];

export function CronStatusBadge({ status }: { status: CronStatus }) {
  const { t } = useT();
  const s = statusMap[status] ?? { tone: "neutral" as const, label: status, icon: CircleDashed };
  return (
    <Badge tone={s.tone}>
      <s.icon className={`size-3 ${status === "running" ? "animate-spin" : ""}`} />
      {t(s.label)}
    </Badge>
  );
}

function secs(n: number) {
  return n < 60 ? `${fmtNumber(n, n < 10 ? 1 : 0)} s` : duration(n);
}

function useDebounced<T>(value: T, ms: number) {
  const [v, setV] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setV(value), ms);
    return () => clearTimeout(id);
  }, [value, ms]);
  return v;
}

type Draft = { name: string; schedule: string; command: string; timeout_sec: number; enabled: boolean };
type Editing = { job: CronJob | null; draft: Draft };

const blank: Draft = { name: "", schedule: "* * * * *", command: "", timeout_sec: 3600, enabled: true };

/** Scheduled tasks of one website: list, create/edit, run now, history. */
export function CronJobs({ domain }: { domain: Domain }) {
  const { t } = useT();
  const { data, isLoading, error } = useCronJobs(domain.id);
  const [editing, setEditing] = useState<Editing | null>(null);
  const [history, setHistory] = useState<CronJob | null>(null);
  const [toDelete, setToDelete] = useState<CronJob | null>(null);
  const update = useUpdateCron(domain.id);
  const run = useRunCron(domain.id);
  const del = useDeleteCron(domain.id, () => setToDelete(null));

  const jobs = data?.jobs ?? [];
  const max = data?.max_jobs ?? 20;
  const full = jobs.length >= max;
  const openNew = (draft: Draft = blank) => setEditing({ job: null, draft });

  return (
    <Card>
      <CardHeader
        title={t("Scheduled tasks")}
        description={
          <>
            {t("Times are in the server's time zone ({timezone}).", { timezone: data?.timezone ?? "…" })}
            {full && <span className="block">{t("This website has reached the limit of {count} tasks.", { count: max })}</span>}
          </>
        }
        action={
          <Button
            size="sm"
            disabled={!data || full}
            title={full ? t("This website has reached the limit of {count} tasks.", { count: max }) : undefined}
            onClick={() => openNew()}
          >
            <Plus /> {t("New task")}
          </Button>
        }
      />
      {isLoading ? (
        <div className="flex justify-center py-10">
          <Spinner />
        </div>
      ) : error ? (
        <div className="p-4">
          <ErrorBox error={error} />
        </div>
      ) : jobs.length === 0 ? (
        <Empty icon={CalendarClock} title={t("No scheduled tasks")}>
          <p className="mx-auto max-w-lg">
            {t("Scheduled tasks run a command on this website at fixed times — framework schedulers, cache clean-ups, imports or backups.")}
          </p>
          <p className="mt-3">{t("Start from an example:")}</p>
          <ul className="mt-2 grid gap-1.5">
            {EXAMPLES.map((ex) => (
              <li key={ex.command}>
                <button
                  type="button"
                  className="inline-flex items-center gap-2 rounded px-2 py-1 hover:bg-muted hover:text-foreground"
                  onClick={() => openNew({ ...blank, name: t(ex.name), schedule: ex.schedule, command: ex.command })}
                >
                  <ChevronRight className="size-3.5" />
                  {t(ex.label)}: <code className="font-mono text-xs">{ex.command}</code>
                </button>
              </li>
            ))}
          </ul>
        </Empty>
      ) : (
        <Table>
          <thead>
            <tr>
              <Th>{t("Task")}</Th>
              <Th>{t("Schedule")}</Th>
              <Th>{t("Enabled")}</Th>
              <Th>{t("Next run")}</Th>
              <Th>{t("Last run")}</Th>
              <Th className="w-40" />
            </tr>
          </thead>
          <tbody>
            {jobs.map((j) => {
              const label = j.name || j.command;
              const starting = run.isPending && run.variables === j.id;
              return (
                <tr key={j.id} className={j.enabled ? undefined : "text-muted-foreground"}>
                  <Td className="max-w-xs">
                    <div className={`truncate font-medium ${j.name ? "" : "font-mono text-xs"}`} title={label}>
                      {label}
                    </div>
                    {j.name && (
                      <div className="truncate font-mono text-xs text-muted-foreground" title={j.command}>
                        {j.command}
                      </div>
                    )}
                  </Td>
                  <Td>
                    <div>{describeSchedule(j.schedule)}</div>
                    <code className="font-mono text-xs text-muted-foreground">{j.schedule}</code>
                  </Td>
                  <Td>
                    <Switch
                      checked={j.enabled}
                      disabled={update.isPending && update.variables?.jid === j.id}
                      aria-label={t("Enable {name}", { name: label })}
                      onCheckedChange={(v) => update.mutate({ jid: j.id, enabled: v })}
                    />
                  </Td>
                  <Td className="whitespace-nowrap">{j.enabled && j.next_run_at ? <span title={dateTime(j.next_run_at)}>{ago(j.next_run_at)}</span> : "—"}</Td>
                  <Td className="whitespace-nowrap">
                    {j.running ? (
                      <CronStatusBadge status="running" />
                    ) : j.last_status ? (
                      <div className="flex flex-col items-start gap-1">
                        <CronStatusBadge status={j.last_status} />
                        {j.last_run_at && (
                          <span className="text-xs text-muted-foreground" title={dateTime(j.last_run_at)}>
                            {ago(j.last_run_at)}
                          </span>
                        )}
                      </div>
                    ) : (
                      <span className="text-muted-foreground">{ago(null)}</span>
                    )}
                  </Td>
                  <Td className="text-right">
                    <div className="flex items-center justify-end gap-1">
                      <Button size="sm" variant="outline" loading={starting || j.running} onClick={() => run.mutate(j.id)}>
                        {!(starting || j.running) && <Play />} {t("Run now")}
                      </Button>
                      <Dropdown>
                        <DropdownTrigger className="rounded p-1.5 hover:bg-muted" aria-label={t("Actions for {name}", { name: label })}>
                          <MoreHorizontal className="size-4" />
                        </DropdownTrigger>
                        <DropdownContent>
                          <DropdownItem onSelect={() => setHistory(j)}>
                            <History /> {t("History")}
                          </DropdownItem>
                          <DropdownItem
                            onSelect={() =>
                              setEditing({
                                job: j,
                                draft: { name: j.name, schedule: j.schedule, command: j.command, timeout_sec: j.timeout_sec, enabled: j.enabled },
                              })
                            }
                          >
                            <Pencil /> {t("Edit")}
                          </DropdownItem>
                          <DropdownSeparator />
                          <DropdownItem destructive onSelect={() => setToDelete(j)}>
                            <Trash2 /> {t("Delete")}
                          </DropdownItem>
                        </DropdownContent>
                      </Dropdown>
                    </div>
                  </Td>
                </tr>
              );
            })}
          </tbody>
        </Table>
      )}

      <Dialog open={!!editing} onOpenChange={(o) => !o && setEditing(null)}>
        {editing && (
          <DialogContent title={editing.job ? t("Edit task") : t("New task")} className="max-w-xl">
            <CronForm domain={domain} editing={editing} onDone={() => setEditing(null)} />
          </DialogContent>
        )}
      </Dialog>

      <Dialog open={!!history} onOpenChange={(o) => !o && setHistory(null)}>
        {history && (
          <DialogContent
            title={t("History of {name}", { name: history.name || history.command })}
            description={t("The last 50 runs, newest first.")}
            className="max-w-3xl"
          >
            <CronHistory domainId={domain.id} job={history} />
          </DialogContent>
        )}
      </Dialog>

      <Confirm
        open={!!toDelete}
        onOpenChange={(o) => !o && setToDelete(null)}
        title={t("Delete {name}?", { name: toDelete ? toDelete.name || toDelete.command : "" })}
        description={t("The task and its run history are removed. A run in progress is not stopped.")}
        loading={del.isPending}
        onConfirm={() => del.mutate(toDelete!.id)}
      />
    </Card>
  );
}

function CronForm({ domain, editing, onDone }: { domain: Domain; editing: Editing; onDone: () => void }) {
  const { t } = useT();
  const [d, setD] = useState<Draft>(editing.draft);
  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => setD((p) => ({ ...p, [k]: v }));
  const debounced = useDebounced(d.schedule.trim(), 300);
  const preview = useCronPreview(debounced);
  const create = useCreateCron(domain.id, onDone);
  const update = useUpdateCron(domain.id, () => {
    toast.success(t("Task saved"));
    onDone();
  });
  const mutation = editing.job ? update : create;
  const preset = CRON_PRESETS.find((p) => p.expr === d.schedule.trim())?.expr ?? "custom";
  const timeouts = TIMEOUTS.some((o) => o.sec === d.timeout_sec) ? TIMEOUTS : [...TIMEOUTS, { sec: d.timeout_sec, label: "" }].sort((a, b) => a.sec - b.sec);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const body = { name: d.name.trim(), schedule: d.schedule.trim(), command: d.command, timeout_sec: d.timeout_sec, enabled: d.enabled };
    if (editing.job) update.mutate({ jid: editing.job.id, ...body });
    else create.mutate(body);
  };

  return (
    <form className="grid gap-4" onSubmit={submit}>
      <Field label={t("Name")} hint={t("Optional. Shown in the list instead of the command.")}>
        <Input maxLength={100} value={d.name} onChange={(e) => set("name", e.target.value)} />
      </Field>

      <div className="grid gap-1.5">
        <Field label={t("Schedule")}>
          <Select value={preset} onChange={(e) => e.target.value !== "custom" && set("schedule", e.target.value)}>
            {CRON_PRESETS.map((p) => (
              <option key={p.expr} value={p.expr}>
                {t(p.label)}
              </option>
            ))}
            <option value="custom">{t("Custom")}</option>
          </Select>
        </Field>
        <Input
          className="font-mono"
          aria-label={t("Cron expression")}
          placeholder="*/5 * * * *"
          required
          spellCheck={false}
          value={d.schedule}
          onChange={(e) => set("schedule", e.target.value)}
        />
        <p className="text-xs text-muted-foreground">{t("Minute, hour, day of month, month, day of week — or @hourly, @daily, @weekly, @monthly, @yearly.")}</p>
        <div className="min-h-14 rounded-md bg-muted/60 px-3 py-2 text-xs" aria-live="polite">
          {!debounced ? null : preview.error ? (
            <span className="text-destructive">{preview.error.message}</span>
          ) : preview.data ? (
            <>
              <div className="font-medium">{describeSchedule(debounced)}</div>
              <div className="mt-1 text-muted-foreground">
                {t("Next runs (your local time):")} {preview.data.next.slice(0, 3).map(dateTime).join(" · ")}
              </div>
            </>
          ) : (
            <Spinner className="size-4" />
          )}
        </div>
      </div>

      <Field
        label={t("Command")}
        hint={
          <>
            {t("Runs as the website's user in {dir}", { dir: `/srv/sites/${domain.name}` })}
            {domain.runtime === "static" && (
              <span className="block">{t("Static websites run tasks in a tools container with bash, git and curl; PHP is not available there.")}</span>
            )}
          </>
        }
      >
        <Textarea
          className="min-h-24 font-mono text-xs"
          required
          maxLength={4000}
          spellCheck={false}
          placeholder="php artisan schedule:run"
          value={d.command}
          onChange={(e) => set("command", e.target.value)}
        />
      </Field>

      <div className="grid gap-4 sm:grid-cols-2 sm:items-end">
        <Field label={t("Time limit")} hint={t("The task is stopped when it runs longer.")}>
          <Select value={d.timeout_sec} onChange={(e) => set("timeout_sec", Number(e.target.value))}>
            {timeouts.map((o) => (
              <option key={o.sec} value={o.sec}>
                {o.label ? t(o.label) : secs(o.sec)}
              </option>
            ))}
          </Select>
        </Field>
        <label className="flex h-9 items-center gap-2 self-start text-sm sm:mt-6">
          <Switch checked={d.enabled} onCheckedChange={(v) => set("enabled", v)} />
          {d.enabled ? t("Enabled") : t("Disabled")}
        </label>
      </div>

      <ErrorBox error={mutation.error} />
      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" onClick={onDone}>
          {t("Cancel")}
        </Button>
        <Button type="submit" loading={mutation.isPending}>
          {editing.job ? t("Save") : t("Create task")}
        </Button>
      </div>
    </form>
  );
}

const TRIGGERS: Record<CronRun["trigger"], string> = { schedule: tk("Scheduled"), manual: tk("Manual") };

function CronHistory({ domainId, job }: { domainId: number; job: CronJob }) {
  const { t } = useT();
  const { data, isLoading, error } = useCronRuns(domainId, job.id);
  if (isLoading)
    return (
      <div className="flex justify-center py-10">
        <Spinner />
      </div>
    );
  if (error) return <ErrorBox error={error} />;
  if (!data?.length) return <Empty icon={History} title={t("No runs yet")} />;
  return (
    <div className="grid gap-2">
      {data.map((r, i) => (
        <details key={r.id} open={i === 0} className="group rounded-md border">
          <summary className="flex cursor-pointer list-none flex-wrap items-center gap-x-4 gap-y-1 px-3 py-2 text-sm">
            <ChevronRight className="size-4 shrink-0 text-muted-foreground transition-transform group-open:rotate-90" />
            <CronStatusBadge status={r.status} />
            <span className="tabular">{dateTime(r.started_at)}</span>
            <span className="text-muted-foreground">{t(TRIGGERS[r.trigger] ?? r.trigger)}</span>
            <span className="text-muted-foreground">
              {t("Duration")}: {r.finished_at ? secs(Math.max(0, (Date.parse(r.finished_at) - Date.parse(r.started_at)) / 1000)) : "—"}
            </span>
            <span className="text-muted-foreground">
              {t("Exit code")}: <span className="font-mono">{r.exit_code ?? "—"}</span>
            </span>
          </summary>
          <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-all border-t bg-muted/40 px-3 py-2 font-mono text-xs">
            {r.output || t("No output")}
          </pre>
        </details>
      ))}
    </div>
  );
}
