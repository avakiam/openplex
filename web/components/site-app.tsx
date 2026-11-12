"use client";
import { ArrowUpCircle, Check, CircleCheck, Copy, CopyPlus, ExternalLink, LayoutTemplate, RefreshCw, RotateCcw, TriangleAlert, Upload } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { toast } from "sonner";

import { AppSecuritySection } from "@/components/app-security";
import { Button, Card, CardBody, CardHeader, Dialog, DialogContent, ErrorBox, Field, Input, Select, Spinner } from "@/components/ui";
import { api } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { keys, useAction, useApps, useDomain, useSiteApp } from "@/lib/hooks";
import { tk, tServer, useT } from "@/lib/i18n";
import type { AppInfo, AppUpdates, Domain, SiteApp } from "@/lib/types";
import { ago } from "@/lib/utils";

const languageNames: Record<string, string> = { en: "English", es: "Español", ca: "Català" };

/** A 20-character password from an unambiguous alphabet. */
function generatePassword() {
  const abc = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  const buf = new Uint32Array(20);
  crypto.getRandomValues(buf);
  return Array.from(buf, (n) => abc[n % abc.length]).join("");
}

/** Whether app runs on PHP version v ("8.3"). */
export function phpSupports(app: AppInfo, v: string) {
  const [a, b] = v.split(".").map(Number);
  const [x, y] = app.min_php.split(".").map(Number);
  return a > x || (a === x && b >= y);
}

/** preferred if app runs on it, else the lowest of versions (ascending) that it runs on. */
export function phpFor(app: AppInfo, versions: string[], preferred: string) {
  return phpSupports(app, preferred) ? preferred : (versions.find((v) => phpSupports(app, v)) ?? preferred);
}

export interface AppForm {
  app: string;
  title: string;
  admin_user: string;
  admin_email: string;
  admin_password: string;
  language: string;
}

/** State of the install form. An empty title means the website's name. */
export function useAppForm() {
  const { user } = useAuth();
  const { locale } = useT();
  const [form, setForm] = useState<AppForm>({
    app: "",
    title: "",
    admin_user: "",
    admin_email: "",
    admin_password: "",
    language: locale,
  });
  useEffect(() => {
    setForm((f) => ({
      ...f,
      admin_email: f.admin_email || user?.email || "",
      // "admin" is the first name attackers try.
      admin_user: f.admin_user || (user?.username && user.username !== "admin" ? user.username : ""),
      admin_password: f.admin_password || generatePassword(),
    }));
  }, [user?.email]);
  return [form, setForm] as const;
}

export function AppFields({ form, setForm, apps, siteName }: { form: AppForm; setForm: (f: AppForm) => void; apps: AppInfo[]; siteName: string }) {
  const { t } = useT();
  const [copied, setCopied] = useState(false);
  const app = apps.find((a) => a.id === form.app);
  if (!app) return null;
  const set = (k: keyof AppForm) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setForm({ ...form, [k]: e.target.value });
  return (
    <div className="grid gap-3 rounded-md border p-3">
      <p className="text-xs text-muted-foreground">{t(app.description)}</p>
      <Field label={t("Site title")}>
        <Input maxLength={100} placeholder={siteName || "example.com"} value={form.title} onChange={set("title")} />
      </Field>
      <div className="grid grid-cols-2 gap-3">
        <Field label={t("Administrator username")}>
          <Input required minLength={3} maxLength={60} pattern="[A-Za-z0-9][A-Za-z0-9._\-]+" autoComplete="off" value={form.admin_user} onChange={set("admin_user")} />
        </Field>
        <Field label={t("Language")}>
          <Select value={app.languages.includes(form.language) ? form.language : "en"} onChange={set("language")} disabled={app.languages.length < 2}>
            {app.languages.map((l) => (
              <option key={l} value={l}>
                {languageNames[l] ?? l}
              </option>
            ))}
          </Select>
        </Field>
      </div>
      <Field label={t("Administrator e-mail")}>
        <Input required type="email" maxLength={254} value={form.admin_email} onChange={set("admin_email")} />
      </Field>
      <Field label={t("Administrator password")} hint={t("At least 12 characters. Copy it now: OpenPlex does not keep it.")}>
        <div className="flex gap-2">
          <Input
            required
            minLength={12}
            maxLength={128}
            className="font-mono"
            autoComplete="new-password"
            value={form.admin_password}
            onChange={set("admin_password")}
          />
          <Button
            type="button"
            variant="outline"
            size="icon"
            aria-label={t("Copy")}
            onClick={() =>
              navigator.clipboard.writeText(form.admin_password).then(
                () => {
                  setCopied(true);
                  setTimeout(() => setCopied(false), 1500);
                },
                () => toast.error(t("Copy failed — select the text manually")),
              )
            }
          >
            {copied ? <Check /> : <Copy />}
          </Button>
          <Button
            type="button"
            variant="outline"
            size="icon"
            aria-label={t("Generate a password")}
            onClick={() => setForm({ ...form, admin_password: generatePassword() })}
          >
            <RefreshCw />
          </Button>
        </div>
      </Field>
    </div>
  );
}

/** The application card on a website's overview. */
export function SiteAppCard({ d, canInstall }: { d: Domain; canInstall: boolean }) {
  const { t } = useT();
  const { data: app, isLoading } = useSiteApp(d.id);
  const [open, setOpen] = useState(false);
  if (isLoading || d.kind === "alias") return null;
  if (!app && !canInstall) return null;

  return (
    <Card>
      <CardHeader title={t("Application")} />
      <CardBody className="grid gap-3 text-sm">
        {!app && (
          <>
            <p className="text-muted-foreground">{t("Install WordPress, Joomla or Drupal with its own database in one step.")}</p>
            <Button variant="outline" onClick={() => setOpen(true)}>
              <LayoutTemplate /> {t("Install an application")}
            </Button>
          </>
        )}
        {app?.status === "installing" && (
          <div className="flex items-start gap-2">
            <Spinner className="mt-0.5 size-4" />
            <p>
              {app.staging_of_name
                ? t("Copying the files and the database of {site}… This can take several minutes.", { site: app.staging_of_name })
                : t("Installing {app}… This usually takes one or two minutes.", { app: app.name })}
            </p>
          </div>
        )}
        {app?.status === "installed" && <Installed app={app} canManage={canInstall} />}
        {app?.status === "error" && (
          <>
            <div className="flex items-start gap-2 text-destructive">
              <TriangleAlert className="mt-0.5 size-4 shrink-0" />
              <div>
                <p className="font-medium">
                  {app.staging_of_name ? t("The staging copy could not be made.") : t("{app} could not be installed.", { app: app.name })}
                </p>
                <p className="break-words text-xs">{tServer(app.status_message)}</p>
              </div>
            </div>
            {canInstall && !app.staging_of_name && (
              <Button variant="outline" onClick={() => setOpen(true)}>
                <RotateCcw /> {t("Try again")}
              </Button>
            )}
          </>
        )}
      </CardBody>
      {open && <InstallAppDialog d={d} initial={app?.app} onClose={() => setOpen(false)} />}
    </Card>
  );
}

function Installed({ app, canManage }: { app: SiteApp; canManage: boolean }) {
  const { t } = useT();
  return (
    <>
      <p>
        <span className="font-medium">{app.name}</span> {app.version}
      </p>
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
        <dt className="text-muted-foreground">{t("Administrator")}</dt>
        <dd className="truncate">{app.admin_user}</dd>
        {app.database_name && (
          <>
            <dt className="text-muted-foreground">{t("Database")}</dt>
            <dd className="truncate font-mono">{app.database_name}</dd>
          </>
        )}
      </dl>
      <Button variant="outline" asChild>
        <a href={app.admin_url} target="_blank" rel="noreferrer">
          <ExternalLink /> {t("Open the administration")}
        </a>
      </Button>
      <AppUpdatesSection app={app} canManage={canManage} />
      <AppSecuritySection app={app} canManage={canManage} />
      <StagingSection app={app} canManage={canManage} />
    </>
  );
}

/** Staging: copies of this website, or (on a copy) pushing it to the live site. */
function StagingSection({ app, canManage }: { app: SiteApp; canManage: boolean }) {
  const { t } = useT();
  const [creating, setCreating] = useState(false);
  const [pushing, setPushing] = useState(false);
  const live = app.staging_of_name;
  if (!live && !canManage && app.staging_copies.length === 0) return null;
  return (
    <div className="grid gap-2 border-t pt-3">
      <p className="font-medium">{t("Staging")}</p>
      {app.task === "copying" && (
        <p className="flex items-start gap-2">
          <Spinner className="mt-0.5 size-4 shrink-0" /> {t("Making a staging copy… This can take several minutes.")}
        </p>
      )}
      {app.task === "pushing" && (
        <p className="flex items-start gap-2">
          <Spinner className="mt-0.5 size-4 shrink-0" />{" "}
          {live ? t("Pushing this copy to {site}…", { site: live }) : t("A staging copy is being pushed to this website…")}
        </p>
      )}
      {live ? (
        <>
          <p className="text-xs text-muted-foreground">
            {t("This website is a staging copy of {site}. Try changes here, then push them to the live website.", { site: live })}{" "}
            {app.staging_of != null && (
              <Link className="text-primary hover:underline" href={`/domains/view/?id=${app.staging_of}`}>
                {t("Open {domain}", { domain: live })}
              </Link>
            )}
          </p>
          {canManage && !app.task && (
            <Button variant="outline" onClick={() => setPushing(true)}>
              <Upload /> {t("Push to {site}", { site: live })}
            </Button>
          )}
        </>
      ) : (
        <>
          {app.staging_copies.length > 0 ? (
            <ul className="grid gap-1 text-xs">
              {app.staging_copies.map((c) => (
                <li key={c.id}>
                  <Link className="text-primary hover:underline" href={`/domains/view/?id=${c.id}`}>
                    {c.name}
                  </Link>
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-xs text-muted-foreground">{t("A staging copy is a separate copy of this website on a subdomain, to try updates and changes before they reach visitors.")}</p>
          )}
          {canManage && !app.task && (
            <Button variant="outline" onClick={() => setCreating(true)}>
              <CopyPlus /> {t("Create a staging copy")}
            </Button>
          )}
        </>
      )}
      {creating && <CreateStagingDialog app={app} onClose={() => setCreating(false)} />}
      {pushing && live && <PushStagingDialog app={app} live={live} onClose={() => setPushing(false)} />}
    </div>
  );
}

function CreateStagingDialog({ app, onClose }: { app: SiteApp; onClose: () => void }) {
  const { t } = useT();
  const router = useRouter();
  const { data: site } = useDomain(app.domain_id);
  const [label, setLabel] = useState("staging");
  const create = useAction(() => api.post<Domain>(`/domains/${app.domain_id}/app/staging`, { label }), {
    success: t("Making the staging copy"),
    invalidate: [keys.siteApp(app.domain_id), keys.domains, keys.databases],
    onSuccess: (d) => {
      onClose();
      router.push(`/domains/view/?id=${d.id}`);
    },
  });
  const name = site ? `${label || "staging"}.${site.name}` : "";
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        title={t("Create a staging copy")}
        description={t("The files and the database are copied to a new website on a subdomain, with its own database. The live website is not changed.")}
      >
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            create.mutate(undefined);
          }}
        >
          <Field label={t("Name")} hint={name}>
            <Input required maxLength={32} pattern="[a-z0-9]([a-z0-9\-]*[a-z0-9])?" value={label} onChange={(e) => setLabel(e.target.value.toLowerCase())} />
          </Field>
          <ul className="grid list-disc gap-1 pl-5 text-xs text-muted-foreground">
            <li>{t("The name must point to this server (as the live website does) for the copy to be reachable and get its HTTPS certificate.")}</li>
            <li>{t("The copy asks search engines not to index it (WordPress and Joomla).")}</li>
            <li>{t("It counts as a website and a database of the account.")}</li>
          </ul>
          <ErrorBox error={create.error} />
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={onClose}>
              {t("Cancel")}
            </Button>
            <Button type="submit" loading={create.isPending}>
              {t("Create the copy")}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function PushStagingDialog({ app, live, onClose }: { app: SiteApp; live: string; onClose: () => void }) {
  const { t } = useT();
  const [files, setFiles] = useState(true);
  const [database, setDatabase] = useState(true);
  const push = useAction(() => api.post(`/domains/${app.domain_id}/app/push`, { files, database }), {
    success: t("Pushing to {site}", { site: live }),
    invalidate: [keys.siteApp(app.domain_id), ...(app.staging_of != null ? [keys.siteApp(app.staging_of)] : [])],
    onSuccess: onClose,
  });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        title={t("Push to {site}", { site: live })}
        description={t("The live website is replaced with this copy. A backup of the live website is taken first; if it fails, nothing is changed.")}
      >
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            push.mutate(undefined);
          }}
        >
          <div className="grid gap-2 text-sm">
            <label className="flex items-start gap-2">
              <input type="checkbox" className="mt-1" checked={files} onChange={(e) => setFiles(e.target.checked)} />
              <span>
                {t("Files")}
                <span className="block text-xs text-muted-foreground">{t("Code, plugins, themes and uploads. The live configuration file is kept.")}</span>
              </span>
            </label>
            <label className="flex items-start gap-2">
              <input type="checkbox" className="mt-1" checked={database} onChange={(e) => setDatabase(e.target.checked)} />
              <span>
                {t("Database")}
                <span className="block text-xs text-muted-foreground">{t("Content and settings. What changed on the live website since the copy was made (comments, orders, users…) is lost.")}</span>
              </span>
            </label>
          </div>
          <ErrorBox error={push.error} />
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={onClose}>
              {t("Cancel")}
            </Button>
            <Button type="submit" variant="destructive" loading={push.isPending} disabled={!files && !database}>
              {t("Push")}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

const kindLabels: Record<string, string> = {
  plugin: tk("Plugin"),
  theme: tk("Theme"),
  module: tk("Module"),
  component: tk("Component"),
  package: tk("Package"),
  library: tk("Library"),
  template: tk("Template"),
  language: tk("Language"),
};

/** Updates of an installed application: last check, what is available, update. */
function AppUpdatesSection({ app, canManage }: { app: SiteApp; canManage: boolean }) {
  const { t } = useT();
  const [all, setAll] = useState(false);
  const [updating, setUpdating] = useState(false);
  const check = useAction(() => api.post(`/domains/${app.domain_id}/app/check`), { invalidate: [keys.siteApp(app.domain_id)] });
  const u = app.updates;
  const items = u?.items ?? [];
  const shown = all ? items : items.slice(0, 6);
  const manual = !!u?.manual_extensions;
  const updatable = !!u?.core || (items.length > 0 && !manual);

  return (
    <div className="grid gap-2 border-t pt-3">
      <div className="flex items-center justify-between gap-2">
        <p className="font-medium">{t("Updates")}</p>
        {canManage && !app.task && (
          <Button variant="ghost" size="sm" loading={check.isPending} onClick={() => check.mutate(undefined)}>
            <RefreshCw /> {t("Check now")}
          </Button>
        )}
      </div>
      {app.task === "checking" && (
        <p className="flex items-center gap-2 text-muted-foreground">
          <Spinner className="size-4" /> {t("Checking for updates…")}
        </p>
      )}
      {app.task === "updating" && (
        <p className="flex items-start gap-2">
          <Spinner className="mt-0.5 size-4 shrink-0" /> {t("Backing up the website and its database, then updating… This can take several minutes.")}
        </p>
      )}
      {app.task_error && !app.task && (
        <div className="flex items-start gap-2 text-destructive">
          <TriangleAlert className="mt-0.5 size-4 shrink-0" />
          <p className="break-words text-xs">{tServer(app.task_error)}</p>
        </div>
      )}
      <ErrorBox error={check.error} />
      {!u && !app.task && <p className="text-muted-foreground">{t("Not checked for updates yet.")}</p>}
      {u && !app.task && (
        <>
          {!u.core && items.length === 0 && (
            <p className="flex items-center gap-2 text-emerald-600 dark:text-emerald-400">
              <CircleCheck className="size-4" /> {t("Everything is up to date.")}
            </p>
          )}
          {u.core && (
            <p className="flex items-center gap-2">
              <ArrowUpCircle className="size-4 text-primary" /> {t("{app} {version} is available.", { app: app.name, version: u.core })}
            </p>
          )}
          {items.length > 0 && (
            <ul className="grid gap-1 text-xs">
              {shown.map((it) => (
                <li key={`${it.kind}:${it.name}`} className="flex justify-between gap-3">
                  <span className="truncate">
                    {it.title} <span className="text-muted-foreground">· {kindLabels[it.kind] ? t(kindLabels[it.kind]) : it.kind}</span>
                  </span>
                  <span className="shrink-0 font-mono text-muted-foreground">
                    {it.version && `${it.version} → `}
                    {it.latest}
                  </span>
                </li>
              ))}
              {items.length > shown.length && (
                <li>
                  <button type="button" className="text-primary hover:underline" onClick={() => setAll(true)}>
                    {t("Show all ({count})", { count: items.length })}
                  </button>
                </li>
              )}
            </ul>
          )}
          {manual && items.length > 0 && (
            <p className="text-xs text-muted-foreground">{t("{app} extensions are updated from its administration (System → Update → Extensions).", { app: app.name })}</p>
          )}
          {canManage && updatable && (
            <Button onClick={() => setUpdating(true)}>
              <ArrowUpCircle /> {t("Update")}
            </Button>
          )}
          <p className="text-xs text-muted-foreground">
            {app.checked_at && t("Checked {when}.", { when: ago(app.checked_at) })} {app.last_update_at && t("Last updated {when}.", { when: ago(app.last_update_at) })}
          </p>
        </>
      )}
      {updating && u && <UpdateAppDialog app={app} updates={u} onClose={() => setUpdating(false)} />}
    </div>
  );
}

function UpdateAppDialog({ app, updates, onClose }: { app: SiteApp; updates: AppUpdates; onClose: () => void }) {
  const { t } = useT();
  const extensions = updates.manual_extensions ? 0 : updates.items.length;
  const [core, setCore] = useState(!!updates.core);
  const [ext, setExt] = useState(extensions > 0);
  const update = useAction(() => api.post(`/domains/${app.domain_id}/app/update`, { core, extensions: ext }), {
    success: t("Updating {app}", { app: app.name }),
    invalidate: [keys.siteApp(app.domain_id)],
    onSuccess: onClose,
  });
  const extLabel =
    app.app === "wordpress" ? t("Plugins and themes ({count})", { count: extensions }) : t("Modules ({count})", { count: extensions });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        title={t("Update {app}", { app: app.name })}
        description={t("A backup of the website's files and its database is taken first; if it fails, nothing is updated.")}
      >
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            update.mutate(undefined);
          }}
        >
          <div className="grid gap-2 text-sm">
            {updates.core && (
              <label className="flex items-center gap-2">
                <input type="checkbox" checked={core} onChange={(e) => setCore(e.target.checked)} />
                {t("{app} {from} → {to}", { app: app.name, from: updates.version, to: updates.core })}
              </label>
            )}
            {extensions > 0 && (
              <label className="flex items-center gap-2">
                <input type="checkbox" checked={ext} onChange={(e) => setExt(e.target.checked)} />
                {extLabel}
              </label>
            )}
          </div>
          <p className="text-xs text-muted-foreground">
            {t("While it runs, the website may show a maintenance page for a moment. If the website does not work afterwards, restore the backup from its Backups tab.")}
          </p>
          <ErrorBox error={update.error} />
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={onClose}>
              {t("Cancel")}
            </Button>
            <Button type="submit" loading={update.isPending} disabled={!core && !ext}>
              {t("Update")}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function InstallAppDialog({ d, initial, onClose }: { d: Domain; initial?: string; onClose: () => void }) {
  const { t } = useT();
  const apps = useApps();
  const [form, setForm] = useAppForm();
  useEffect(() => {
    if (!form.app && apps.data?.length) setForm({ ...form, app: initial ?? apps.data[0].id });
  }, [apps.data, form, initial, setForm]);
  const app = apps.data?.find((a) => a.id === form.app);
  const tooOld = !!app && d.runtime === "php" && !phpSupports(app, d.runtime_version);
  const notPHP = d.runtime !== "php";
  const install = useAction(() => api.post<SiteApp>(`/domains/${d.id}/app`, { ...form, title: form.title.trim() || d.name }), {
    success: t("Installing {app}", { app: app?.name ?? "" }),
    invalidate: [keys.siteApp(d.id), keys.databases],
    onSuccess: onClose,
  });

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        title={t("Install an application")}
        description={t("The latest version is downloaded from the project's website and installed in the document root, with a new MariaDB database.")}
      >
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            install.mutate(undefined);
          }}
        >
          <Field label={t("Application")}>
            <Select value={form.app} onChange={(e) => setForm({ ...form, app: e.target.value })}>
              {(apps.data ?? []).map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </Select>
          </Field>
          {notPHP ? (
            <p className="text-sm text-destructive">{t("Applications need a PHP website: change the runtime to PHP in Settings first.")}</p>
          ) : tooOld ? (
            <p className="text-sm text-destructive">
              {t("{app} needs PHP {version} or later: change the PHP version in Settings first.", { app: app?.name ?? "", version: app?.min_php ?? "" })}
            </p>
          ) : (
            <AppFields form={form} setForm={setForm} apps={apps.data ?? []} siteName={d.name} />
          )}
          <p className="text-xs text-muted-foreground">{t("The document root must be empty: the installer refuses to overwrite files.")}</p>
          <ErrorBox error={install.error} />
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={onClose}>
              {t("Cancel")}
            </Button>
            <Button type="submit" loading={install.isPending} disabled={!app || notPHP || !!tooOld}>
              {t("Install")}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
