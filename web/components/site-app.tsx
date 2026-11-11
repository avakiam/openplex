"use client";
import { Check, Copy, ExternalLink, LayoutTemplate, RefreshCw, RotateCcw, TriangleAlert } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";

import { Button, Card, CardBody, CardHeader, Dialog, DialogContent, ErrorBox, Field, Input, Select, Spinner } from "@/components/ui";
import { api } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { keys, useAction, useApps, useSiteApp } from "@/lib/hooks";
import { tServer, useT } from "@/lib/i18n";
import type { AppInfo, Domain, SiteApp } from "@/lib/types";

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
    admin_user: "admin",
    admin_email: "",
    admin_password: "",
    language: locale,
  });
  useEffect(() => {
    setForm((f) => ({
      ...f,
      admin_email: f.admin_email || user?.email || "",
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
            <p>{t("Installing {app}… This usually takes one or two minutes.", { app: app.name })}</p>
          </div>
        )}
        {app?.status === "installed" && <Installed app={app} />}
        {app?.status === "error" && (
          <>
            <div className="flex items-start gap-2 text-destructive">
              <TriangleAlert className="mt-0.5 size-4 shrink-0" />
              <div>
                <p className="font-medium">{t("{app} could not be installed.", { app: app.name })}</p>
                <p className="break-words text-xs">{tServer(app.status_message)}</p>
              </div>
            </div>
            {canInstall && (
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

function Installed({ app }: { app: SiteApp }) {
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
    </>
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
