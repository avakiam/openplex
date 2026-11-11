"use client";
import { BadgeCheck, ExternalLink, KeyRound, Puzzle, Settings2, ShieldAlert, ShoppingBag, Trash2, TriangleAlert, Upload } from "lucide-react";
import Link from "next/link";
import { useRef, useState } from "react";

import {
  Badge,
  Button,
  Card,
  CardBody,
  Confirm,
  Dialog,
  DialogContent,
  Empty,
  ErrorBox,
  Field,
  Input,
  PageHeader,
  Select,
  Spinner,
  Switch,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@/components/ui";
import { api } from "@/lib/api";
import { keys, useAction, useExtensions, useMarketplace } from "@/lib/hooks";
import { tk, useT } from "@/lib/i18n";
import { type CatalogEntry, type Extension, type ExtensionStatus, extText, type PendingExtension } from "@/lib/types";

const permissionLabels: Record<string, string> = {
  "domains:read": tk("See websites and domains"),
  "dns:read": tk("See DNS records"),
  "dns:write": tk("Change DNS records"),
  "mail:read": tk("See mail domains and mailboxes"),
  "databases:read": tk("See databases (not their passwords)"),
  "users:read": tk("See user accounts"),
  "system:read": tk("See server information and usage"),
};

const statusTone: Record<ExtensionStatus, "success" | "warning" | "danger" | "neutral"> = {
  running: "success",
  starting: "warning",
  error: "danger",
  disabled: "neutral",
};
const statusLabels: Record<ExtensionStatus, string> = {
  running: tk("Running"),
  starting: tk("Starting"),
  error: tk("Error"),
  disabled: tk("Disabled"),
};

function Permissions({ list }: { list?: string[] | null }) {
  const { t } = useT();
  if (!list?.length) return <p className="text-sm text-muted-foreground">{t("It doesn't use the panel's data.")}</p>;
  return (
    <ul className="grid gap-1 text-sm">
      {list.map((p) => (
        <li key={p} className="flex items-center gap-2">
          <span className="size-1.5 rounded-full bg-primary" /> {permissionLabels[p] ? t(permissionLabels[p]) : p}
        </li>
      ))}
    </ul>
  );
}

function Verified({ signer }: { signer: string }) {
  const { t } = useT();
  return signer ? (
    <Badge tone="success">
      <BadgeCheck className="size-3" /> {t("Verified by OpenPlex")}
    </Badge>
  ) : (
    <Badge tone="warning">
      <ShieldAlert className="size-3" /> {t("Unverified publisher")}
    </Badge>
  );
}

/** Review of a received package: who made it, what it may do; then install. */
function ReviewDialog({ p, onClose }: { p: PendingExtension | null; onClose: () => void }) {
  const { t, locale } = useT();
  const install = useAction(() => api.post<Extension>("/extensions/install", { token: p?.token }), {
    success: t("Extension installed"),
    invalidate: [keys.extensions, keys.extensionsUI],
    onSuccess: onClose,
  });
  const cancel = () => {
    if (p) api.del(`/extensions/pending/${p.token}`).catch(() => undefined);
    onClose();
  };
  const m = p?.manifest;
  return (
    <Dialog open={!!p} onOpenChange={(o) => !o && cancel()}>
      <DialogContent
        className="max-w-lg"
        title={m ? `${extText(m.name, locale)} ${m.version}` : ""}
        description={m ? t("By {publisher}", { publisher: m.publisher.name }) : ""}
      >
        {p && m && (
          <div className="grid gap-4">
            <div className="flex flex-wrap gap-2">
              <Verified signer={p.signer} />
              {p.installed_version && <Badge>{t("Replaces version {version}", { version: p.installed_version })}</Badge>}
            </div>
            {m.description && <p className="text-sm text-muted-foreground">{extText(m.description, locale)}</p>}
            {!p.signer && (
              <p className="flex gap-2 rounded-md border border-warning/40 bg-warning/10 p-3 text-sm">
                <TriangleAlert className="mt-0.5 size-4 shrink-0 text-warning" />
                {t("OpenPlex has not checked this package. Install it only if you trust whoever gave it to you: it will run on this server.")}
              </p>
            )}
            <div className="grid gap-2">
              <p className="text-sm font-medium">{t("It will be able to:")}</p>
              <Permissions list={m.permissions} />
            </div>
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={cancel}>
                {t("Cancel")}
              </Button>
              <Button loading={install.isPending} onClick={() => install.mutate(undefined)}>
                {p.installed_version ? t("Update") : t("Install")}
              </Button>
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

function UploadButton({ onReceived }: { onReceived: (p: PendingExtension) => void }) {
  const { t } = useT();
  const input = useRef<HTMLInputElement>(null);
  const upload = useAction(
    (f: File) => {
      const fd = new FormData();
      fd.append("package", f);
      return api.post<PendingExtension>("/extensions/upload", fd);
    },
    { onSuccess: onReceived },
  );
  return (
    <>
      <input
        ref={input}
        type="file"
        accept=".opx"
        className="hidden"
        onChange={(e) => {
          const f = e.target.files?.[0];
          if (f) upload.mutate(f);
          e.target.value = "";
        }}
      />
      <Button variant="outline" loading={upload.isPending} onClick={() => input.current?.click()}>
        <Upload /> {t("Upload extension")}
      </Button>
    </>
  );
}

function SettingsDialog({ ext, onClose }: { ext: Extension | null; onClose: () => void }) {
  const { t, locale } = useT();
  const [values, setValues] = useState<Record<string, string>>({});
  const [key, setKey] = useState("");
  if (ext && key !== ext.id + ext.updated_at) {
    setKey(ext.id + ext.updated_at);
    setValues({ ...ext.settings });
  }
  const save = useAction(
    () => {
      const out: Record<string, string> = {};
      for (const s of ext?.manifest.settings ?? []) {
        if (s.type === "secret" && !values[s.key]) continue; // keep the stored secret
        out[s.key] = values[s.key] ?? "";
      }
      return api.put(`/extensions/${ext?.id}/settings`, { values: out });
    },
    { success: t("Settings saved"), invalidate: [keys.extensions], onSuccess: onClose },
  );
  const set = (k: string, v: string) => setValues((s) => ({ ...s, [k]: v }));
  return (
    <Dialog open={!!ext} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg" title={ext ? t("{name} settings", { name: extText(ext.manifest.name, locale) }) : ""}>
        {ext && (
          <form
            className="grid gap-4"
            onSubmit={(e) => {
              e.preventDefault();
              save.mutate(undefined);
            }}
          >
            {(ext.manifest.settings ?? []).map((s) => {
              const hint = s.help ? extText(s.help, locale) : undefined;
              const label = extText(s.label, locale);
              if (s.type === "boolean")
                return (
                  <label key={s.key} className="flex items-start gap-3">
                    <Switch checked={values[s.key] === "true"} onCheckedChange={(v) => set(s.key, String(v))} className="mt-0.5" aria-label={label} />
                    <span>
                      <span className="block text-sm font-medium">{label}</span>
                      {hint && <span className="text-sm text-muted-foreground">{hint}</span>}
                    </span>
                  </label>
                );
              return (
                <Field
                  key={s.key}
                  label={label + (s.required ? " *" : "")}
                  hint={s.type === "secret" && ext.secrets_set?.includes(s.key) ? t("Leave empty to keep the saved one.") : hint}
                >
                  {s.type === "select" ? (
                    <Select value={values[s.key] ?? ""} onChange={(e) => set(s.key, e.target.value)}>
                      <option value="">—</option>
                      {(s.options ?? []).map((o) => (
                        <option key={o.value} value={o.value}>
                          {extText(o.label, locale)}
                        </option>
                      ))}
                    </Select>
                  ) : (
                    <Input
                      type={s.type === "secret" ? "password" : "text"}
                      autoComplete={s.type === "secret" ? "new-password" : "off"}
                      placeholder={s.type === "secret" && ext.secrets_set?.includes(s.key) ? "••••••••" : ""}
                      value={values[s.key] ?? ""}
                      onChange={(e) => set(s.key, e.target.value)}
                    />
                  )}
                </Field>
              );
            })}
            <ErrorBox error={save.error} />
            <div className="flex justify-end">
              <Button type="submit" loading={save.isPending}>
                {t("Save settings")}
              </Button>
            </div>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}

function LicenseDialog({ ext, onClose }: { ext: Extension | null; onClose: () => void }) {
  const { t } = useT();
  const [value, setValue] = useState("");
  const save = useAction(() => api.put(`/extensions/${ext?.id}/license`, { license_key: value }), {
    success: t("Licence key saved"),
    invalidate: [keys.extensions],
    onSuccess: () => {
      setValue("");
      onClose();
    },
  });
  return (
    <Dialog open={!!ext} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-md" title={t("Licence key")} description={t("The key you received when buying this extension.")}>
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate(undefined);
          }}
        >
          <Input className="font-mono" value={value} onChange={(e) => setValue(e.target.value)} placeholder={ext?.has_license ? "••••••••" : ""} />
          <div className="flex justify-end">
            <Button type="submit" loading={save.isPending}>
              {t("Save")}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function InstalledCard({ e, onSettings, onLicense }: { e: Extension; onSettings: () => void; onLicense: () => void }) {
  const { t, locale } = useT();
  const [confirm, setConfirm] = useState(false);
  const toggle = useAction((on: boolean) => api.patch(`/extensions/${e.id}`, { enabled: on }), { invalidate: [keys.extensions, keys.extensionsUI] });
  const remove = useAction(() => api.del(`/extensions/${e.id}`), {
    success: t("Extension removed"),
    invalidate: [keys.extensions, keys.extensionsUI],
    onSuccess: () => setConfirm(false),
  });
  const m = e.manifest;
  return (
    <Card>
      <CardBody className="grid gap-3">
        <div className="flex flex-wrap items-start gap-3">
          <Puzzle className="mt-0.5 size-5 text-muted-foreground" />
          <div className="min-w-0 flex-1">
            <p className="font-medium">
              {extText(m.name, locale)} <span className="text-sm font-normal text-muted-foreground">{e.version}</span>
            </p>
            <p className="text-sm text-muted-foreground">{t("By {publisher}", { publisher: m.publisher.name })}</p>
          </div>
          <Switch checked={e.enabled} disabled={toggle.isPending} onCheckedChange={(v) => toggle.mutate(v)} aria-label={t("Enabled")} />
        </div>
        {m.description && <p className="text-sm text-muted-foreground">{extText(m.description, locale)}</p>}
        <div className="flex flex-wrap gap-2">
          <Badge tone={statusTone[e.status]}>{t(statusLabels[e.status])}</Badge>
          <Verified signer={e.signer} />
        </div>
        {e.status === "error" && e.status_message && <p className="text-sm text-destructive">{e.status_message}</p>}
        <div className="flex flex-wrap gap-2">
          {e.enabled &&
            (m.pages ?? []).map((p) => (
              <Button key={p.id} size="sm" variant="outline" asChild>
                <Link href={`/extensions/app/?id=${encodeURIComponent(e.id)}&page=${encodeURIComponent(p.id)}`}>
                  <ExternalLink /> {extText(p.title, locale)}
                </Link>
              </Button>
            ))}
          {(m.settings?.length ?? 0) > 0 && (
            <Button size="sm" variant="outline" onClick={onSettings}>
              <Settings2 /> {t("Settings")}
            </Button>
          )}
          {e.source === "marketplace" && (
            <Button size="sm" variant="outline" onClick={onLicense}>
              <KeyRound /> {t("Licence key")}
            </Button>
          )}
          <Button size="sm" variant="ghost" className="ml-auto text-destructive" onClick={() => setConfirm(true)}>
            <Trash2 /> {t("Remove")}
          </Button>
        </div>
      </CardBody>
      <Confirm
        open={confirm}
        onOpenChange={setConfirm}
        title={t("Remove {name}?", { name: extText(m.name, locale) })}
        description={t("Its container, settings and stored data are deleted.")}
        confirmLabel={t("Remove")}
        loading={remove.isPending}
        onConfirm={() => remove.mutate(undefined)}
      />
    </Card>
  );
}

function Installed({ onReceived }: { onReceived: (p: PendingExtension) => void }) {
  const { t } = useT();
  const { data, isLoading, error } = useExtensions();
  const [settings, setSettings] = useState<Extension | null>(null);
  const [license, setLicense] = useState<Extension | null>(null);
  if (isLoading) return <Spinner />;
  if (error) return <ErrorBox error={error} />;
  return (
    <>
      {!data?.length ? (
        <Empty icon={Puzzle} title={t("No extensions installed")}>
          <div className="grid justify-items-center gap-3">
            {t("Install one from the marketplace or upload a package (.opx).")}
            <UploadButton onReceived={onReceived} />
          </div>
        </Empty>
      ) : (
        <div className="grid gap-4 md:grid-cols-2">
          {data.map((e) => (
            <InstalledCard key={e.id} e={e} onSettings={() => setSettings(e)} onLicense={() => setLicense(e)} />
          ))}
        </div>
      )}
      <SettingsDialog ext={settings} onClose={() => setSettings(null)} />
      <LicenseDialog ext={license} onClose={() => setLicense(null)} />
    </>
  );
}

function Marketplace({ onReceived }: { onReceived: (p: PendingExtension) => void }) {
  const { t, locale } = useT();
  const { data, isLoading, error } = useMarketplace(true);
  const installed = useExtensions().data ?? [];
  const [buying, setBuying] = useState<CatalogEntry | null>(null);
  const [license, setLicense] = useState("");
  const download = useAction((a: { id: string; license_key: string }) => api.post<PendingExtension>("/extensions/marketplace/download", a), {
    onSuccess: (p) => {
      setBuying(null);
      setLicense("");
      onReceived(p);
    },
  });
  if (isLoading) return <Spinner />;
  if (error)
    return (
      <Empty icon={ShoppingBag} title={t("The marketplace is unavailable")}>
        {t("Try again later, or upload an extension package.")}
      </Empty>
    );
  if (!data?.length) return <Empty icon={ShoppingBag} title={t("No extensions available yet")} />;
  return (
    <>
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        {data.map((c) => {
          const have = installed.find((e) => e.id === c.id);
          return (
            <Card key={c.id}>
              <CardBody className="grid h-full content-start gap-3">
                <div className="flex items-start gap-3">
                  {/* eslint-disable-next-line @next/next/no-img-element */}
                  {c.icon ? <img src={c.icon} alt="" className="size-10 rounded" /> : <Puzzle className="size-10 text-muted-foreground" />}
                  <div className="min-w-0 flex-1">
                    <p className="font-medium">{extText(c.name, locale)}</p>
                    <p className="text-sm text-muted-foreground">{t("By {publisher}", { publisher: c.publisher.name })}</p>
                  </div>
                  <Badge tone={c.price ? "info" : "success"}>{c.price ? `${c.price.amount} ${c.price.currency}` : t("Free")}</Badge>
                </div>
                <p className="text-sm text-muted-foreground">{extText(c.description, locale)}</p>
                <div className="mt-auto flex flex-wrap items-center gap-2">
                  {c.homepage && (
                    <a className="inline-flex items-center gap-1 text-sm hover:underline" href={c.homepage} target="_blank" rel="noreferrer">
                      {t("More information")} <ExternalLink className="size-3" />
                    </a>
                  )}
                  <span className="ml-auto" />
                  {have?.version === c.version ? (
                    <Badge>{t("Installed")}</Badge>
                  ) : (
                    <Button
                      size="sm"
                      loading={download.isPending && download.variables?.id === c.id}
                      onClick={() => (c.price ? setBuying(c) : download.mutate({ id: c.id, license_key: "" }))}
                    >
                      {have ? t("Update to {version}", { version: c.version }) : t("Install")}
                    </Button>
                  )}
                </div>
              </CardBody>
            </Card>
          );
        })}
      </div>
      <Dialog open={!!buying} onOpenChange={(o) => !o && setBuying(null)}>
        <DialogContent
          className="max-w-md"
          title={buying ? extText(buying.name, locale) : ""}
          description={t("This extension is paid: enter the licence key you received when buying it.")}
        >
          {buying && (
            <form
              className="grid gap-4"
              onSubmit={(e) => {
                e.preventDefault();
                download.mutate({ id: buying.id, license_key: license });
              }}
            >
              <Field label={t("Licence key")}>
                <Input className="font-mono" value={license} onChange={(e) => setLicense(e.target.value)} required />
              </Field>
              {buying.buy_url && (
                <a className="inline-flex items-center gap-1 text-sm hover:underline" href={buying.buy_url} target="_blank" rel="noreferrer">
                  {t("Buy a licence")} <ExternalLink className="size-3" />
                </a>
              )}
              <div className="flex justify-end">
                <Button type="submit" loading={download.isPending}>
                  {t("Continue")}
                </Button>
              </div>
            </form>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}

export default function ExtensionsPage() {
  const { t } = useT();
  const [pending, setPending] = useState<PendingExtension | null>(null);
  return (
    <>
      <PageHeader
        title={t("Extensions")}
        description={t("Add features to OpenPlex: official extensions from the marketplace, or your own packages.")}
        actions={<UploadButton onReceived={setPending} />}
      />
      <Tabs defaultValue="installed">
        <TabsList className="mb-4">
          <TabsTrigger value="installed">
            <Puzzle /> {t("Installed")}
          </TabsTrigger>
          <TabsTrigger value="marketplace">
            <ShoppingBag /> {t("Marketplace")}
          </TabsTrigger>
        </TabsList>
        <TabsContent value="installed">
          <Installed onReceived={setPending} />
        </TabsContent>
        <TabsContent value="marketplace">
          <Marketplace onReceived={setPending} />
        </TabsContent>
      </Tabs>
      <ReviewDialog p={pending} onClose={() => setPending(null)} />
    </>
  );
}
