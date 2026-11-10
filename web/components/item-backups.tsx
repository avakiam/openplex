"use client";
import { Archive, CheckCircle2, History, Loader2, RotateCcw, XCircle } from "lucide-react";
import { useState } from "react";

import { api } from "@/lib/api";
import { keys, useAction, useItemBackups } from "@/lib/hooks";
import { useT } from "@/lib/i18n";
import type { BackupRun, Snapshot } from "@/lib/types";
import { ago, bytes, dateTime } from "@/lib/utils";

import { Badge, Button, Confirm, Empty, ErrorBox, Spinner, Table, Td, Th } from "./ui";

export function RunStatus({ run }: { run: BackupRun }) {
  const { t } = useT();
  if (run.status === "running")
    return (
      <Badge tone="info">
        <Loader2 className="size-3 animate-spin" /> {run.kind === "restore" ? t("Restoring") : t("Backing up")}
      </Badge>
    );
  if (run.status === "success")
    return (
      <Badge tone="success">
        <CheckCircle2 className="size-3" /> {run.kind === "restore" ? t("Restored") : t("Done")}
      </Badge>
    );
  return (
    <Badge tone="danger" title={run.message}>
      <XCircle className="size-3" /> {t("Failed")}
    </Badge>
  );
}

/** Snapshots of one website/database with "back up now" and one-click restore. */
export function ItemBackups({ base, label, warning }: { base: string; label: string; warning: string }) {
  const { t } = useT();
  const { data, isLoading, error } = useItemBackups(base);
  const [restore, setRestore] = useState<Snapshot | null>(null);
  const inv = { invalidate: [keys.itemBackups(base)] };
  const now = useAction(() => api.post(`${base}/backups`), { success: t("Backup started"), ...inv });
  const doRestore = useAction((id: string) => api.post(`${base}/backups/${id}/restore`), {
    success: t("Restore started"),
    ...inv,
    onSuccess: () => setRestore(null),
  });
  const running = data?.runs.find((r) => r.status === "running");
  const last = data?.runs[0];

  return (
    <div className="grid gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <Button loading={now.isPending} disabled={!!running} onClick={() => now.mutate(undefined)}>
          <Archive /> {t("Back up now")}
        </Button>
        {last && (
          <span className="flex items-center gap-2 text-sm text-muted-foreground">
            <RunStatus run={last} />
            {last.kind === "restore" ? t("Restore {when}", { when: ago(last.started_at) }) : t("Backup {when}", { when: ago(last.started_at) })}
            {last.status === "error" && <span className="max-w-md truncate text-destructive" title={last.message}>— {last.message}</span>}
          </span>
        )}
      </div>
      <ErrorBox error={now.error ?? doRestore.error} />
      {isLoading ? (
        <div className="flex justify-center py-10"><Spinner /></div>
      ) : error ? (
        <ErrorBox error={error} />
      ) : !data?.snapshots.length ? (
        <Empty icon={History} title={t("No backups yet")}>{t("Backups run nightly, or start one now.")}</Empty>
      ) : (
        <Table>
          <thead>
            <tr>
              <Th>{t("Restore point")}</Th>
              <Th className="text-right">{t("Size")}</Th>
              <Th className="text-right">{t("New data")}</Th>
              <Th className="w-32" />
            </tr>
          </thead>
          <tbody className="tabular">
            {data.snapshots.map((s, i) => (
              <tr key={s.id}>
                <Td>
                  <div className="font-medium">{dateTime(s.time)}</div>
                  <div className="text-xs text-muted-foreground">
                    {ago(s.time)} · <span className="font-mono">{s.short_id}</span>
                    {i === 0 && <Badge className="ml-2">{t("latest")}</Badge>}
                  </div>
                </Td>
                <Td className="text-right">{s.summary ? bytes(s.summary.total_bytes_processed) : "—"}</Td>
                <Td className="text-right text-muted-foreground">{s.summary ? bytes(s.summary.data_added) : "—"}</Td>
                <Td className="text-right">
                  <Button size="sm" variant="outline" disabled={!!running} onClick={() => setRestore(s)}>
                    <RotateCcw /> {t("Restore")}
                  </Button>
                </Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      <Confirm
        open={!!restore}
        onOpenChange={(o) => !o && setRestore(null)}
        title={t("Restore {name} to {time}?", { name: label, time: restore ? dateTime(restore.time) : "" })}
        description={warning}
        confirmLabel={t("Restore")}
        loading={doRestore.isPending}
        onConfirm={() => doRestore.mutate(restore!.id)}
      />
    </div>
  );
}
