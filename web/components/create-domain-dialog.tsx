"use client";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { toast } from "sonner";

import { Button, Dialog, DialogContent, ErrorBox, Field, Input, Select, Switch } from "@/components/ui";
import { api } from "@/lib/api";
import { canManageUsers, useAuth } from "@/lib/auth";
import { keys, useAction, useApps, useDomains, useRuntimes, useUsers } from "@/lib/hooks";
import { tServer, useT } from "@/lib/i18n";

import { AppFields, phpFor, phpSupports, useAppForm } from "./site-app";
import { roleLabels } from "./status";
import type { Domain, DomainKind, SiteApp } from "@/lib/types";

export function CreateDomainDialog({
  open,
  onOpenChange,
  defaultKind = "primary",
  defaultParent,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  defaultKind?: DomainKind;
  defaultParent?: Domain;
}) {
  const { t } = useT();
  const router = useRouter();
  const { user } = useAuth();
  const runtimes = useRuntimes();
  const domains = useDomains();
  const users = useUsers();
  const [kind, setKind] = useState<DomainKind>(defaultKind);
  const [name, setName] = useState("");
  const [parentId, setParentId] = useState<number | "">(defaultParent?.id ?? "");
  const [ownerId, setOwnerId] = useState<number | "">("");
  const [runtime, setRuntime] = useState("static");
  const [version, setVersion] = useState("");
  const [command, setCommand] = useState("");
  const [port, setPort] = useState("");
  const [ssl, setSsl] = useState(true);
  const apps = useApps();
  const [appForm, setAppForm] = useAppForm();
  const app = kind === "alias" ? undefined : apps.data?.find((a) => a.id === appForm.app);

  useEffect(() => {
    if (open) {
      setKind(defaultKind);
      setParentId(defaultParent?.id ?? "");
      setName(defaultKind === "subdomain" && defaultParent ? `.${defaultParent.name}` : "");
    }
  }, [open, defaultKind, defaultParent]);

  const rt = runtimes.data?.find((r) => r.name === runtime);
  useEffect(() => {
    setVersion(rt?.default ?? "");
    setPort(rt?.needs_command ? String(rt.default_port) : "");
  }, [rt]);
  // An application needs PHP, in a version it supports.
  useEffect(() => {
    if (!app) return;
    if (runtime !== "php") setRuntime("php");
    else if (rt?.versions) setVersion((v) => phpFor(app, rt.versions ?? [], v || rt.default));
  }, [app, runtime, rt]);

  const parents = (domains.data ?? []).filter((d) => (kind === "subdomain" ? d.kind === "primary" : d.kind !== "alias"));

  const create = useAction(
    async () => {
      const d = await api.post<Domain>("/domains", {
        name: name.trim(),
        kind,
        parent_id: kind === "primary" ? undefined : Number(parentId),
        owner_id: kind === "primary" && ownerId !== "" ? Number(ownerId) : undefined,
        ...(kind === "alias"
          ? {}
          : {
              runtime,
              runtime_version: version || undefined,
              start_command: rt?.needs_command ? command : undefined,
              app_port: rt?.needs_command ? Number(port) : undefined,
              ssl_enabled: ssl,
              force_https: ssl,
            }),
      });
      if (app) {
        // The website exists either way: a refused install is reported on its page.
        await api.post<SiteApp>(`/domains/${d.id}/app`, { ...appForm, title: appForm.title.trim() || d.name }).catch((e: Error) => toast.error(e.message));
      }
      return d;
    },
    {
      success: app ? t("Website created — installing {app}", { app: app.name }) : t("Website created — deploying now"),
      invalidate: [keys.domains, keys.databases],
      onSuccess: (d) => {
        onOpenChange(false);
        router.push(`/domains/view/?id=${d.kind === "alias" ? d.parent_id : d.id}`);
      },
    },
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent title={t("Add a website or domain")} description={t("OpenPlex provisions the container, routing and HTTPS certificate automatically.")}>
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            create.mutate(undefined);
          }}
        >
          <Field label={t("Type")}>
            <Select value={kind} onChange={(e) => setKind(e.target.value as DomainKind)}>
              <option value="primary">{t("Website (new domain)")}</option>
              <option value="subdomain">{t("Subdomain of an existing domain")}</option>
              <option value="alias">{t("Alias (extra hostname for an existing website)")}</option>
            </Select>
          </Field>
          {kind !== "primary" && (
            <Field label={kind === "subdomain" ? t("Parent domain") : t("Serve the same content as")}>
              <Select required value={parentId} onChange={(e) => setParentId(e.target.value ? Number(e.target.value) : "")}>
                <option value="">{t("Choose…")}</option>
                {parents.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </Select>
            </Field>
          )}
          <Field
            label={t("Hostname")}
            hint={
              kind === "alias"
                ? t("e.g. {example}", { example: "www.example.com" })
                : t("e.g. {example} — point its DNS A/AAAA record at this server", { example: "example.com" })
            }
          >
            <Input required placeholder="example.com" value={name} onChange={(e) => setName(e.target.value.toLowerCase())} autoFocus />
          </Field>
          {kind === "primary" && canManageUsers(user) && (
            <Field label={t("Owner")}>
              <Select value={ownerId} onChange={(e) => setOwnerId(e.target.value ? Number(e.target.value) : "")}>
                <option value="">{t("Me ({name})", { name: user?.username ?? "" })}</option>
                {(users.data ?? [])
                  .filter((u) => u.id !== user?.id)
                  .map((u) => (
                    <option key={u.id} value={u.id}>
                      {u.username} ({t(roleLabels[u.role])})
                    </option>
                  ))}
              </Select>
            </Field>
          )}
          {kind !== "alias" && (
            <>
              <Field label={t("Application")}>
                <Select value={appForm.app} onChange={(e) => setAppForm({ ...appForm, app: e.target.value })}>
                  <option value="">{t("None — empty website")}</option>
                  {(apps.data ?? []).map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.name}
                    </option>
                  ))}
                </Select>
              </Field>
              <AppFields form={appForm} setForm={setAppForm} apps={apps.data ?? []} siteName={name.trim()} />
              <div className="grid grid-cols-2 gap-3">
                <Field label={t("Runtime")}>
                  <Select value={runtime} disabled={!!app} onChange={(e) => setRuntime(e.target.value)}>
                    {(runtimes.data ?? []).map((r) => (
                      <option key={r.name} value={r.name}>
                        {tServer(r.label)}
                      </option>
                    ))}
                  </Select>
                </Field>
                {rt?.versions && (
                  <Field label={t("Version")}>
                    <Select value={version} onChange={(e) => setVersion(e.target.value)}>
                      {rt.versions
                        .filter((v) => !app || phpSupports(app, v))
                        .map((v) => (
                          <option key={v}>{v}</option>
                        ))}
                    </Select>
                  </Field>
                )}
              </div>
              {rt?.needs_command && (
                <div className="grid grid-cols-[1fr_7rem] gap-3">
                  <Field label={t("Start command")} hint={t("Runs in the site folder; listen on $PORT.")}>
                    <Input
                      required
                      className="font-mono"
                      placeholder={runtime === "node" ? "npm start" : runtime === "python" ? "python app.py" : "go run ."}
                      value={command}
                      onChange={(e) => setCommand(e.target.value)}
                    />
                  </Field>
                  <Field label={t("Port")}>
                    <Input required type="number" min={1024} max={65535} value={port} onChange={(e) => setPort(e.target.value)} />
                  </Field>
                </div>
              )}
              <label className="flex items-center justify-between gap-4 rounded-md border px-3 py-2.5">
                <span>
                  <span className="block text-sm font-medium">{t("HTTPS with automatic certificate")}</span>
                  <span className="block text-xs text-muted-foreground">{t("Let's Encrypt / ZeroSSL, HTTP redirected to HTTPS.")}</span>
                </span>
                <Switch checked={ssl} onCheckedChange={setSsl} />
              </label>
            </>
          )}
          <ErrorBox error={create.error} />
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              {t("Cancel")}
            </Button>
            <Button type="submit" loading={create.isPending}>
              {t("Create")}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
