"use client";
import { CircleCheck, ExternalLink, Mail, ShieldCheck, TriangleAlert } from "lucide-react";
import Link from "next/link";
import { useEffect, useState } from "react";

import { Badge, Button, Card, CardBody, CardHeader, Empty, ErrorBox, Field, Input, PageHeader, Spinner, Switch, Table, Td, Th } from "@/components/ui";
import { api } from "@/lib/api";
import { keys, useAction, useMail } from "@/lib/hooks";
import { tServer, useT } from "@/lib/i18n";
import type { MailSettings, MailStatus } from "@/lib/types";
import { ago } from "@/lib/utils";

function SettingsCard({ st }: { st: MailStatus }) {
  const { t } = useT();
  const [f, setF] = useState<MailSettings>({ enabled: st.enabled, hostname: st.hostname, webmail: st.webmail });
  const key = `${st.enabled}|${st.hostname}|${st.webmail}`;
  useEffect(() => setF({ enabled: st.enabled, hostname: st.hostname, webmail: st.webmail }), [key]); // eslint-disable-line react-hooks/exhaustive-deps
  const save = useAction(() => api.put("/mail/settings", f), { success: t("Mail settings saved"), invalidate: [keys.mail] });
  return (
    <Card>
      <CardHeader title={t("Mail server")} description={t("Postfix, Dovecot and Rspamd (spam filter and DKIM signing) in one container.")} />
      <form
        className="grid gap-4 p-5"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate(undefined);
        }}
      >
        <label className="flex items-start gap-3">
          <Switch checked={f.enabled} onCheckedChange={(v) => setF({ ...f, enabled: v })} className="mt-0.5" aria-label={t("Mail server on")} />
          <span>
            <span className="block font-medium">{f.enabled ? t("Mail server on") : t("Mail server off")}</span>
            <span className="text-sm text-muted-foreground">{t("Mailboxes are kept while it is off, but no mail is received or sent.")}</span>
          </span>
        </label>
        <Field label={t("Mail server host name")} hint={t("Every mail domain's MX points here, and e-mail apps connect to it. It gets its own certificate.")}>
          <Input className="font-mono" placeholder="mail.example.com" value={f.hostname} onChange={(e) => setF({ ...f, hostname: e.target.value })} />
        </Field>
        <label className="flex items-start gap-3">
          <Switch checked={f.webmail} onCheckedChange={(v) => setF({ ...f, webmail: v })} className="mt-0.5" aria-label={t("Webmail")} />
          <span>
            <span className="block font-medium">{t("Webmail (Roundcube)")}</span>
            <span className="text-sm text-muted-foreground">
              {t("Read and send mail from the browser at the mail host and at webmail.<domain> for domains whose DNS is here.")}
            </span>
          </span>
        </label>
        <ErrorBox error={save.error} />
        <div className="flex justify-end">
          <Button type="submit" loading={save.isPending}>
            {t("Save settings")}
          </Button>
        </div>
      </form>
    </Card>
  );
}

function Check({ ok, children }: { ok: boolean; children: React.ReactNode }) {
  return (
    <li className="flex gap-2">
      {ok ? <CircleCheck className="mt-0.5 size-4 shrink-0 text-success" /> : <TriangleAlert className="mt-0.5 size-4 shrink-0 text-warning" />}
      <span>{children}</span>
    </li>
  );
}

function StatusCard({ st }: { st: MailStatus }) {
  const { t } = useT();
  const forwardOk = st.forward.length > 0 && st.server_ips.every((ip) => st.forward.includes(ip));
  return (
    <Card>
      <CardHeader
        title={t("Status")}
        action={
          st.enabled ? (
            <Badge tone={st.running ? (st.health === "unhealthy" ? "danger" : "success") : "danger"}>
              {st.running ? (st.health === "starting" ? t("Starting") : st.health === "unhealthy" ? t("Unhealthy") : t("Running")) : t("Not running")}
            </Badge>
          ) : (
            <Badge>{t("Off")}</Badge>
          )
        }
      />
      <CardBody className="grid gap-3 text-sm">
        {!st.available && <ErrorBox error={t("Docker is not available on this server.")} />}
        {st.last_error && <ErrorBox error={tServer(st.last_error)} />}
        {st.hostname && (
          <ul className="grid gap-2">
            <Check ok={forwardOk}>
              {forwardOk
                ? t("{host} points at this server.", { host: st.hostname })
                : t("{host} should point at this server ({ips}); now: {now}.", {
                    host: st.hostname,
                    ips: st.server_ips.join(", "),
                    now: st.forward.join(", ") || "—",
                  })}
            </Check>
            <Check ok={st.ptr_ok}>
              {st.ptr_ok
                ? t("Reverse DNS points back at {host}.", { host: st.hostname })
                : t("Set the reverse DNS (PTR) of {ips} to {host} at your server provider, or other servers may treat your mail as spam.", {
                    ips: st.server_ips.join(", "),
                    host: st.hostname,
                  })}
              {st.ptr.length > 0 && <span className="block font-mono text-xs text-muted-foreground">{st.ptr.join(" · ")}</span>}
            </Check>
            {st.webmail && st.enabled && (
              <Check ok={st.webmail_running}>
                {st.webmail_running ? t("Webmail running.") : t("The webmail is starting or not running.")}{" "}
                {st.webmail_url && (
                  <a className="inline-flex items-center gap-1 font-medium hover:underline" href={st.webmail_url} target="_blank" rel="noreferrer">
                    {st.webmail_url.replace(/^https:\/\/|\/$/g, "")} <ExternalLink className="size-3" />
                  </a>
                )}
              </Check>
            )}
            <Check ok={st.certificate === "letsencrypt"}>
              {st.certificate === "letsencrypt"
                ? t("Trusted certificate for {host}.", { host: st.hostname })
                : t("Temporary certificate until Let's Encrypt issues one for {host} (needs its DNS to point here).", { host: st.hostname })}
            </Check>
          </ul>
        )}
        {st.last_sync && <p className="text-xs text-muted-foreground">{t("Configuration last applied {when}.", { when: ago(st.last_sync) })}</p>}
      </CardBody>
    </Card>
  );
}

export default function MailPage() {
  const { t } = useT();
  const { data, isLoading, error } = useMail();
  return (
    <>
      <PageHeader title={t("Mail")} description={t("The mail server for every domain hosted here. Mailboxes are managed on each website's Mail tab.")} />
      {isLoading ? (
        <div className="flex justify-center py-10">
          <Spinner />
        </div>
      ) : error || !data ? (
        <ErrorBox error={error} />
      ) : (
        <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
          <div className="grid content-start gap-4">
            <SettingsCard st={data.status} />
            <StatusCard st={data.status} />
          </div>
          <Card>
            <CardHeader title={t("Mail domains")} />
            {data.domains.length === 0 ? (
              <Empty icon={Mail} title={t("No domain has mail yet")}>
                {t("Turn it on from a website's Mail tab.")}
              </Empty>
            ) : (
              <Table>
                <thead>
                  <tr>
                    <Th>{t("Domain")}</Th>
                    <Th className="text-right">{t("Mailboxes")}</Th>
                    <Th className="text-right">{t("Forwarding")}</Th>
                  </tr>
                </thead>
                <tbody className="tabular">
                  {data.domains.map((d) => (
                    <tr key={d.id}>
                      <Td>
                        <Link className="font-medium hover:underline" href={`/domains/view/?id=${d.domain_id}`}>
                          {d.name}
                        </Link>
                      </Td>
                      <Td className="text-right">{d.mailboxes}</Td>
                      <Td className="text-right">{d.aliases}</Td>
                    </tr>
                  ))}
                </tbody>
              </Table>
            )}
            <div className="flex gap-2 border-t p-4 text-xs text-muted-foreground">
              <ShieldCheck className="size-4 shrink-0" />{" "}
              {t("Each domain signs its mail with its own DKIM key; SPF and DMARC records are added to its DNS zone.")}
            </div>
          </Card>
        </div>
      )}
    </>
  );
}
