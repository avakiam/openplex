"use client";
import { useState } from "react";

import { Button, Dialog, DialogContent, ErrorBox, Field, Input } from "@/components/ui";
import { api } from "@/lib/api";
import { keys, useAction } from "@/lib/hooks";
import { useT } from "@/lib/i18n";
import type { Database } from "@/lib/types";
import { bytes } from "@/lib/utils";

const MB = 1 << 20;

/** Size of a database, against its limit when it has one. */
export function SizeCell({ db, size }: { db: Database; size?: number }) {
  const { t } = useT();
  if (size === undefined) return <>—</>;
  if (!db.limit_mb) return <>{bytes(size)}</>;
  const pct = Math.min(100, (size / (db.limit_mb * MB)) * 100);
  const tone = db.size_locked || pct >= 100 ? "bg-destructive" : pct >= 90 ? "bg-warning" : "bg-primary";
  return (
    <div className="ml-auto grid w-32 gap-1" title={t("{pct}% of {limit}", { pct: String(Math.round(pct)), limit: bytes(db.limit_mb * MB) })}>
      <span>
        {bytes(size)} <span className="text-xs">/ {bytes(db.limit_mb * MB)}</span>
      </span>
      <div className="h-1 overflow-hidden rounded-full bg-muted" role="presentation">
        <div className={`h-full rounded-full ${tone}`} style={{ width: `${pct}%` }} />
      </div>
    </div>
  );
}

/** Sets the size limit of one database (administrators, the account's reseller). */
export function LimitDialog({ db, onClose }: { db: Database | null; onClose: () => void }) {
  const { t } = useT();
  // Remounted per database (key), so this starts from its own limit.
  const [mb, setMb] = useState(() => String(db?.max_size_mb ?? 0));
  const save = useAction((n: number) => api.put<Database>(`/databases/${db!.id}/limit`, { max_size_mb: n }), {
    success: t("Size limit saved"),
    invalidate: [keys.databases],
    onSuccess: onClose,
  });
  const account = db && db.max_size_mb === 0 ? db.limit_mb : 0;
  return (
    <Dialog open={!!db} onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        title={t("Size limit of {name}", { name: db?.name ?? "" })}
        description={t(
          "Checked every five minutes. Over the limit, the database becomes read-only for its applications until data is deleted or the limit is raised.",
        )}
      >
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate(Number(mb) || 0);
          }}
        >
          <Field
            label={t("Limit (MB)")}
            hint={
              account
                ? t("0 = the account's limit ({limit} for each database).", { limit: bytes(account * MB) })
                : t("0 = the account's limit (none at the moment).")
            }
          >
            <Input type="number" min={0} value={mb} onChange={(e) => setMb(e.target.value)} />
          </Field>
          <ErrorBox error={save.error} />
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={onClose}>
              {t("Cancel")}
            </Button>
            <Button type="submit" loading={save.isPending}>
              {t("Save")}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
