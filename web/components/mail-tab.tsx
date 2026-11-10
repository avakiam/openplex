"use client";
import { ExternalLink, Forward, KeyRound, Mail, Plus, RefreshCw, Trash2, TriangleAlert } from "lucide-react";
import Link from "next/link";
import { useState } from "react";

import {
  Button,
  Card,
  CardBody,
  CardHeader,
  Confirm,
  Dialog,
  DialogContent,
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
} from "@/components/ui";
import { api } from "@/lib/api";
import { isAdmin, useAuth } from "@/lib/auth";
import { keys, useAction, useDomainMail } from "@/lib/hooks";
import { fmtNumber, useT } from "@/lib/i18n";
import type { Domain, MailAlias, Mailbox } from "@/lib/types";

const quotas = [256, 512, 1024, 2048, 5120, 10240];

const size = (mb: number) => (mb >= 1024 ? `${fmtNumber(mb / 1024, mb % 1024 ? 1 : 0)} GB` : `${fmtNumber(mb, mb < 10 ? 1 : 0)} MB`);

/** A 16-character password from an unambiguous alphabet. */
function generatePassword() {
  const abc = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  const buf = new Uint32Array(16);
  crypto.getRandomValues(buf);
  return Array.from(buf, (n) => abc[n % abc.length]).join("");
}

function PasswordField({ value, onChange, label }: { value: string; onChange: (v: string) => void; label: string }) {
  const { t } = useT();
  return (
    <Field label={label} hint={t("At least 10 characters. Copy it now: it cannot be shown again.")}>
      <div className="flex gap-2">
        <Input
          required
          minLength={10}
          maxLength={128}
          className="font-mono"
          autoComplete="new-password"
          value={value}
          onChange={(e) => onChange(e.target.value)}
        />
        <Button type="button" variant="outline" size="icon" aria-label={t("Generate a password")} onClick={() => onChange(generatePassword())}>
          <RefreshCw />
        </Button>
      </div>
    </Field>
  );
}

function MailboxDialog({ d, box, onClose }: { d: Domain; box: Mailbox | null; onClose: () => void }) {
  const { t } = useT();
  const [local, setLocal] = useState("");
  const [password, setPassword] = useState(box ? "" : generatePassword());
  const [quota, setQuota] = useState(box?.quota_mb ?? 1024);
  const save = useAction(
    () =>
      box
        ? api.patch(`/domains/${d.id}/mail/mailboxes/${box.id}`, { ...(password ? { password } : {}), quota_mb: quota })
        : api.post(`/domains/${d.id}/mail/mailboxes`, { local_part: local, password, quota_mb: quota }),
    { success: box ? t("Mailbox saved") : t("Mailbox created"), invalidate: [keys.domainMail(d.id)], onSuccess: onClose },
  );
  const sizes = quotas.includes(quota) ? quotas : [...quotas, quota].sort((a, b) => a - b);
  return (
    <form
      className="grid gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate(undefined);
      }}
    >
      {!box && (
        <Field label={t("Address")}>
          <div className="flex items-center gap-1">
            <Input required autoFocus className="font-mono" placeholder="info" value={local} onChange={(e) => setLocal(e.target.value)} />
            <span className="shrink-0 font-mono text-sm text-muted-foreground">@{d.name}</span>
          </div>
        </Field>
      )}
      {box ? (
        <Field label={t("New password (optional)")} hint={t("Leave empty to keep the current one.")}>
          <div className="flex gap-2">
            <Input
              minLength={10}
              maxLength={128}
              className="font-mono"
              autoComplete="new-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
            <Button type="button" variant="outline" size="icon" aria-label={t("Generate a password")} onClick={() => setPassword(generatePassword())}>
              <RefreshCw />
            </Button>
          </div>
        </Field>
      ) : (
        <PasswordField label={t("Password")} value={password} onChange={setPassword} />
      )}
      <Field label={t("Mailbox size")}>
        <Select value={quota} onChange={(e) => setQuota(Number(e.target.value))}>
          {sizes.map((q) => (
            <option key={q} value={q}>
              {size(q)}
            </option>
          ))}
        </Select>
      </Field>
      <ErrorBox error={save.error} />
      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" onClick={onClose}>
          {t("Cancel")}
        </Button>
        <Button type="submit" loading={save.isPending}>
          {box ? t("Save") : t("Create mailbox")}
        </Button>
      </div>
    </form>
  );
}

function AliasDialog({ d, alias, onClose }: { d: Domain; alias: MailAlias | null; onClose: () => void }) {
  const { t } = useT();
  const [local, setLocal] = useState(alias?.local_part ?? "");
  const [dest, setDest] = useState(alias?.destinations.join("\n") ?? "");
  const destinations = () => dest.split(/[\s,;]+/).filter(Boolean);
  const save = useAction(
    () =>
      alias
        ? api.patch(`/domains/${d.id}/mail/aliases/${alias.id}`, { destinations: destinations() })
        : api.post(`/domains/${d.id}/mail/aliases`, { local_part: local, destinations: destinations() }),
    { success: t("Forwarding saved"), invalidate: [keys.domainMail(d.id)], onSuccess: onClose },
  );
  return (
    <form
      className="grid gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate(undefined);
      }}
    >
      <Field label={t("Address")}>
        <div className="flex items-center gap-1">
          <Input required disabled={!!alias} className="font-mono" placeholder="sales" value={local} onChange={(e) => setLocal(e.target.value)} />
          <span className="shrink-0 font-mono text-sm text-muted-foreground">@{d.name}</span>
        </div>
      </Field>
      <Field label={t("Deliver to")} hint={t("One address per line. They can be mailboxes here or addresses anywhere else.")}>
        <Textarea
          required
          rows={4}
          className="font-mono"
          placeholder={`info@${d.name}\nsomeone@example.net`}
          value={dest}
          onChange={(e) => setDest(e.target.value)}
        />
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
  );
}

export function MailTab({ domain: d }: { domain: Domain }) {
  const { t } = useT();
  const { user } = useAuth();
  const { data, isLoading, error } = useDomainMail(d.id);
  const [box, setBox] = useState<Mailbox | "new" | null>(null);
  const [alias, setAlias] = useState<MailAlias | "new" | null>(null);
  const [delBox, setDelBox] = useState<Mailbox | null>(null);
  const [delAlias, setDelAlias] = useState<MailAlias | null>(null);
  const [disabling, setDisabling] = useState(false);
  const inv = { invalidate: [keys.domainMail(d.id)] };
  const enable = useAction(() => api.post(`/domains/${d.id}/mail`), { success: t("Mail enabled"), ...inv });
  const disable = useAction(() => api.del(`/domains/${d.id}/mail`), { success: t("Mail disabled"), ...inv, onSuccess: () => setDisabling(false) });
  const toggle = useAction((b: Mailbox) => api.patch(`/domains/${d.id}/mail/mailboxes/${b.id}`, { enabled: !b.enabled }), inv);
  const removeBox = useAction((b: Mailbox) => api.del(`/domains/${d.id}/mail/mailboxes/${b.id}`), {
    success: t("Mailbox deleted"),
    ...inv,
    onSuccess: () => setDelBox(null),
  });
  const openWebmail = useAction(
    async (b: Mailbox) => {
      // Opened before the request so pop-up blockers allow it.
      const tab = window.open("about:blank", "_blank");
      try {
        const { url } = await api.post<{ url: string }>(`/domains/${d.id}/mail/mailboxes/${b.id}/webmail`);
        if (tab) {
          tab.opener = null;
          tab.location.href = url;
        } else window.location.href = url;
      } catch (e) {
        tab?.close();
        throw e;
      }
    },
  );
  const removeAlias = useAction((a: MailAlias) => api.del(`/domains/${d.id}/mail/aliases/${a.id}`), {
    success: t("Forwarding deleted"),
    ...inv,
    onSuccess: () => setDelAlias(null),
  });

  if (isLoading)
    return (
      <div className="flex justify-center py-10">
        <Spinner />
      </div>
    );
  if (error || !data) return <ErrorBox error={error} />;

  const serverOff = !data.server_enabled && (
    <div className="flex gap-2 rounded-md border border-warning/40 bg-warning/8 p-3 text-sm">
      <TriangleAlert className="mt-0.5 size-4 shrink-0 text-warning" />
      <span>
        {t("The mail server is off, so these mailboxes do not receive mail yet.")}{" "}
        {isAdmin(user) ? (
          <Link className="underline" href="/mail/">
            {t("Turn it on")}
          </Link>
        ) : (
          t("Ask your provider to turn it on.")
        )}
      </span>
    </div>
  );

  if (!data.enabled)
    return (
      <Card>
        <Empty icon={Mail} title={t("Mail is off for {domain}", { domain: d.name })}>
          <p className="max-w-md">{t("Turning it on creates the domain's MX, SPF, DKIM and DMARC records, so you can add mailboxes and forwarding.")}</p>
          <Button className="mt-4" loading={enable.isPending} onClick={() => enable.mutate(undefined)}>
            <Mail /> {t("Turn on mail")}
          </Button>
          {serverOff && <div className="mt-4 text-left">{serverOff}</div>}
        </Empty>
      </Card>
    );

  return (
    <div className="grid gap-4">
      {serverOff}
      <Card>
        <CardHeader
          title={t("Mailboxes")}
          action={
            <Button size="sm" disabled={data.mailboxes.length >= data.max_mailboxes} onClick={() => setBox("new")}>
              <Plus /> {t("New mailbox")}
            </Button>
          }
        />
        {data.mailboxes.length === 0 ? (
          <Empty icon={Mail} title={t("No mailboxes yet")} />
        ) : (
          <Table>
            <thead>
              <tr>
                <Th>{t("Address")}</Th>
                <Th>{t("Used")}</Th>
                <Th>{t("Active")}</Th>
                <Th className="w-32" />
              </tr>
            </thead>
            <tbody>
              {data.mailboxes.map((b) => {
                const pct = Math.min(100, (b.used_mb / b.quota_mb) * 100);
                return (
                  <tr key={b.id}>
                    <Td className="font-mono text-sm">{b.address}</Td>
                    <Td className="min-w-40">
                      <div className="text-xs text-muted-foreground">{t("{used} of {total}", { used: size(b.used_mb), total: size(b.quota_mb) })}</div>
                      <div className="mt-1 h-1.5 rounded-full bg-muted">
                        <div className={`h-1.5 rounded-full ${pct > 90 ? "bg-destructive" : "bg-primary"}`} style={{ width: `${pct}%` }} />
                      </div>
                    </Td>
                    <Td>
                      <Switch checked={b.enabled} aria-label={t("Active")} onCheckedChange={() => toggle.mutate(b)} />
                    </Td>
                    <Td className="whitespace-nowrap text-right">
                      {data.webmail_url && (
                        <Button
                          size="icon"
                          variant="ghost"
                          aria-label={t("Open webmail")}
                          title={t("Open webmail")}
                          disabled={!b.enabled || openWebmail.isPending}
                          onClick={() => openWebmail.mutate(b)}
                        >
                          <ExternalLink />
                        </Button>
                      )}
                      <Button size="icon" variant="ghost" aria-label={t("Password and size")} onClick={() => setBox(b)}>
                        <KeyRound />
                      </Button>
                      <Button size="icon" variant="ghost" aria-label={t("Delete mailbox")} onClick={() => setDelBox(b)}>
                        <Trash2 />
                      </Button>
                    </Td>
                  </tr>
                );
              })}
            </tbody>
          </Table>
        )}
      </Card>

      <Card>
        <CardHeader
          title={t("Forwarding and aliases")}
          description={t("Addresses that deliver to other mailboxes, here or elsewhere.")}
          action={
            <Button size="sm" variant="outline" onClick={() => setAlias("new")}>
              <Plus /> {t("New forwarding")}
            </Button>
          }
        />
        {data.aliases.length === 0 ? (
          <Empty icon={Forward} title={t("No forwarding yet")} />
        ) : (
          <Table>
            <tbody>
              {data.aliases.map((a) => (
                <tr key={a.id}>
                  <Td className="font-mono text-sm">
                    {a.local_part}@{d.name}
                  </Td>
                  <Td className="font-mono text-sm text-muted-foreground">→ {a.destinations.join(", ")}</Td>
                  <Td className="w-24 whitespace-nowrap text-right">
                    <Button size="icon" variant="ghost" aria-label={t("Edit forwarding")} onClick={() => setAlias(a)}>
                      <Forward />
                    </Button>
                    <Button size="icon" variant="ghost" aria-label={t("Delete forwarding")} onClick={() => setDelAlias(a)}>
                      <Trash2 />
                    </Button>
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>

      <Card>
        <CardHeader title={t("E-mail app settings")} description={t("User name: the full address. Password: the mailbox password.")} />
        <CardBody>
          <dl className="grid gap-2 text-sm sm:grid-cols-[10rem_1fr]">
            <dt className="text-muted-foreground">{t("Incoming (IMAP)")}</dt>
            <dd className="font-mono">{data.hostname || "—"} · 993 · SSL/TLS</dd>
            <dt className="text-muted-foreground">{t("Incoming (POP3)")}</dt>
            <dd className="font-mono">{data.hostname || "—"} · 995 · SSL/TLS</dd>
            <dt className="text-muted-foreground">{t("Outgoing (SMTP)")}</dt>
            <dd className="font-mono">
              {data.hostname || "—"} · 465 · SSL/TLS {t("or")} 587 · STARTTLS
            </dd>
            {data.webmail_url && (
              <>
                <dt className="text-muted-foreground">{t("Webmail")}</dt>
                <dd className="font-mono">
                  <a className="hover:underline" href={data.webmail_url} target="_blank" rel="noreferrer">
                    {data.webmail_url}
                  </a>
                </dd>
              </>
            )}
          </dl>
        </CardBody>
      </Card>

      <div className="flex justify-end">
        <Button variant="ghost" className="text-destructive" onClick={() => setDisabling(true)}>
          {t("Turn off mail for this domain")}
        </Button>
      </div>

      <Dialog open={!!box} onOpenChange={(o) => !o && setBox(null)}>
        {box && (
          <DialogContent title={box === "new" ? t("New mailbox") : box.address}>
            <MailboxDialog d={d} box={box === "new" ? null : box} onClose={() => setBox(null)} />
          </DialogContent>
        )}
      </Dialog>
      <Dialog open={!!alias} onOpenChange={(o) => !o && setAlias(null)}>
        {alias && (
          <DialogContent title={alias === "new" ? t("New forwarding") : `${alias.local_part}@${d.name}`}>
            <AliasDialog d={d} alias={alias === "new" ? null : alias} onClose={() => setAlias(null)} />
          </DialogContent>
        )}
      </Dialog>
      <Confirm
        open={!!delBox}
        onOpenChange={(o) => !o && setDelBox(null)}
        title={t("Delete {address}?", { address: delBox?.address ?? "" })}
        description={t("The mailbox and all its messages are deleted. This cannot be undone.")}
        loading={removeBox.isPending}
        onConfirm={() => delBox && removeBox.mutate(delBox)}
      />
      <Confirm
        open={!!delAlias}
        onOpenChange={(o) => !o && setDelAlias(null)}
        title={t("Delete this forwarding?")}
        description={delAlias ? `${delAlias.local_part}@${d.name} → ${delAlias.destinations.join(", ")}` : ""}
        loading={removeAlias.isPending}
        onConfirm={() => delAlias && removeAlias.mutate(delAlias)}
      />
      <Confirm
        open={disabling}
        onOpenChange={setDisabling}
        title={t("Turn off mail for {domain}?", { domain: d.name })}
        description={t(
          "All mailboxes of this domain, their messages and the forwarding are deleted, and its mail DNS records are removed. This cannot be undone.",
        )}
        confirmLabel={t("Turn off and delete")}
        loading={disable.isPending}
        onConfirm={() => disable.mutate(undefined)}
      />
    </div>
  );
}
