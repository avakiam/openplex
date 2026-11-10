"use client";
import { Database as DbIcon, Globe, Share2, Trash2 } from "lucide-react";
import { useState } from "react";

import { Badge, Button, Dialog, DialogContent, Empty, ErrorBox, Field, Select, Spinner, Switch, Table, Td, Th } from "@/components/ui";
import { api } from "@/lib/api";
import { keys, useAction, useDatabases, useDomains, useUserAccess } from "@/lib/hooks";
import { tk, useT } from "@/lib/i18n";
import type { AccessGrant, SiteArea, User } from "@/lib/types";

const areaLabels: Record<SiteArea, string> = {
  site: tk("Website settings"),
  files: tk("Files"),
  dns: "DNS",
  mail: tk("Mail"),
  backups: tk("Backups"),
};
const areaHints: Record<SiteArea, string> = {
  site: tk("Runtime, HTTPS, start/stop, logs, redirects, scheduled tasks"),
  files: tk("File manager (with Website settings: also the web terminal)"),
  dns: tk("The domain's DNS records"),
  mail: tk("Mailboxes, forwarding, webmail and mail backups"),
  backups: tk("Backups and restores of the website's files"),
};
const allAreas = Object.keys(areaLabels) as SiteArea[];

const presets: { id: string; label: string; areas: SiteArea[] }[] = [
  { id: "full", label: tk("Full access"), areas: allAreas },
  { id: "developer", label: tk("Web developer"), areas: ["site", "files", "backups"] },
  { id: "mail", label: tk("Mail only"), areas: ["mail"] },
  { id: "dns", label: tk("DNS only"), areas: ["dns"] },
];

function AddGrant({ user }: { user: User }) {
  const { t } = useT();
  const domains = useDomains();
  const dbs = useDatabases();
  const [target, setTarget] = useState("");
  const [areas, setAreas] = useState<SiteArea[]>(["mail"]);
  const [readOnly, setReadOnly] = useState(false);
  const sites = (domains.data ?? []).filter((d) => !d.access && d.kind !== "alias" && d.owner_id !== user.id);
  const databases = (dbs.data ?? []).filter((d) => !d.shared && d.owner_id !== user.id);
  const [kind, id] = target.split(":");
  const save = useAction(
    () =>
      api.put(`/users/${user.id}/access`, kind === "site" ? { kind, resource_id: Number(id), areas, read_only: readOnly } : { kind, resource_id: Number(id) }),
    { success: t("Access saved"), invalidate: [keys.userAccess(user.id)], onSuccess: () => setTarget("") },
  );
  const preset = presets.find((p) => p.areas.length === areas.length && p.areas.every((a) => areas.includes(a)))?.id ?? "custom";

  return (
    <form
      className="grid gap-4 rounded-md border p-4"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate(undefined);
      }}
    >
      <Field label={t("Share")}>
        <Select value={target} onChange={(e) => setTarget(e.target.value)}>
          <option value="">{t("Choose a website or database…")}</option>
          {sites.length > 0 && (
            <optgroup label={t("Websites")}>
              {sites.map((d) => (
                <option key={d.id} value={`site:${d.id}`}>
                  {d.name}
                </option>
              ))}
            </optgroup>
          )}
          {databases.length > 0 && (
            <optgroup label={t("Databases")}>
              {databases.map((d) => (
                <option key={d.id} value={`database:${d.id}`}>
                  {d.name} ({d.engine_label})
                </option>
              ))}
            </optgroup>
          )}
        </Select>
      </Field>
      {kind === "site" && (
        <>
          <Field label={t("Role")}>
            <Select
              value={preset}
              onChange={(e) => {
                const p = presets.find((x) => x.id === e.target.value);
                if (p) setAreas(p.areas);
              }}
            >
              {presets.map((p) => (
                <option key={p.id} value={p.id}>
                  {t(p.label)}
                </option>
              ))}
              <option value="custom" disabled>
                {t("Custom")}
              </option>
            </Select>
          </Field>
          <div className="grid gap-2">
            {allAreas.map((a) => (
              <label key={a} className="flex items-start gap-3 text-sm">
                <input
                  type="checkbox"
                  className="mt-1"
                  checked={areas.includes(a)}
                  onChange={(e) => setAreas(e.target.checked ? allAreas.filter((x) => x === a || areas.includes(x)) : areas.filter((x) => x !== a))}
                />
                <span>
                  <span className="block font-medium">{t(areaLabels[a])}</span>
                  <span className="text-muted-foreground">{t(areaHints[a])}</span>
                </span>
              </label>
            ))}
          </div>
          <label className="flex items-start gap-3">
            <Switch checked={readOnly} onCheckedChange={setReadOnly} className="mt-0.5" aria-label={t("Read-only")} />
            <span>
              <span className="block text-sm font-medium">{t("Read-only")}</span>
              <span className="text-sm text-muted-foreground">{t("Can look but not change anything (no terminal).")}</span>
            </span>
          </label>
        </>
      )}
      {kind === "database" && (
        <p className="text-sm text-muted-foreground">
          {t("The user can see the credentials, open the database and back it up or restore it, but not delete it.")}
        </p>
      )}
      <ErrorBox error={save.error} />
      <div className="flex justify-end">
        <Button type="submit" loading={save.isPending} disabled={!target || (kind === "site" && areas.length === 0)}>
          <Share2 /> {t("Share")}
        </Button>
      </div>
    </form>
  );
}

function GrantRow({ g, user }: { g: AccessGrant; user: User }) {
  const { t } = useT();
  const remove = useAction(() => api.del(`/users/${user.id}/access/${g.id}`), { success: t("Access removed"), invalidate: [keys.userAccess(user.id)] });
  return (
    <tr>
      <Td>
        <span className="flex items-center gap-2 font-medium">
          {g.kind === "site" ? <Globe className="size-4 text-muted-foreground" /> : <DbIcon className="size-4 text-muted-foreground" />}
          {g.resource_name}
        </span>
      </Td>
      <Td>
        {g.kind === "site" ? (
          <span className="flex flex-wrap gap-1">
            {g.areas.map((a) => (
              <Badge key={a}>{t(areaLabels[a])}</Badge>
            ))}
            {g.read_only && <Badge tone="warning">{t("Read-only")}</Badge>}
          </span>
        ) : (
          <Badge>{t("Database")}</Badge>
        )}
      </Td>
      <Td className="text-right">
        <Button size="icon" variant="ghost" aria-label={t("Remove access")} loading={remove.isPending} onClick={() => remove.mutate(undefined)}>
          <Trash2 />
        </Button>
      </Td>
    </tr>
  );
}

/** What is shared with a user, and sharing more. */
export function AccessDialog({ user, onClose }: { user: User | null; onClose: () => void }) {
  const { t } = useT();
  const { data, isLoading, error } = useUserAccess(user?.id ?? 0, !!user);
  return (
    <Dialog open={!!user} onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        className="max-w-2xl"
        title={t("Access for {username}", { username: user?.username ?? "" })}
        description={t("Websites and databases of other accounts this user may work on, besides their own.")}
      >
        {user && (
          <div className="grid gap-4">
            {isLoading ? (
              <Spinner />
            ) : error ? (
              <ErrorBox error={error} />
            ) : !data?.length ? (
              <Empty icon={Share2} title={t("Nothing shared yet")} />
            ) : (
              <Table>
                <thead>
                  <tr>
                    <Th>{t("Shared")}</Th>
                    <Th>{t("May manage")}</Th>
                    <Th className="w-12" />
                  </tr>
                </thead>
                <tbody>
                  {data.map((g) => (
                    <GrantRow key={g.id} g={g} user={user} />
                  ))}
                </tbody>
              </Table>
            )}
            <AddGrant key={user.id} user={user} />
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
