"use client";
import { Send } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";

import { Badge, Button, Card, CardBody, CardHeader, ErrorBox, Field, Input, PageHeader, Select, Spinner, Switch, Table, Td, Textarea, Th } from "@/components/ui";
import { api } from "@/lib/api";
import { keys, useAction, useAlerts } from "@/lib/hooks";
import { tk, tServer, useT } from "@/lib/i18n";
import type { AlertEntry, AlertSettings, AlertsStatus } from "@/lib/types";
import { dateTime } from "@/lib/utils";

const kindLabels: Record<string, { label: string; hint: string }> = {
  backup_failed: { label: tk("A backup fails"), hint: tk("Each failed backup, once.") },
  site_down: { label: tk("A website is down"), hint: tk("After two minutes in error; again when it is back up.") },
  disk_full: { label: tk("The disk is almost full"), hint: tk("Above 90%; again when it drops below 85%.") },
  database_full: { label: tk("A database goes over its size limit"), hint: tk("When it becomes read-only; again when it is back under its limit.") },
  cert_expiring: { label: tk("A certificate is not renewing"), hint: tk("When a certificate is close to expiring without having been renewed.") },
  update_available: { label: tk("A new OpenPlex version is available"), hint: tk("Once per version.") },
  update_failed: { label: tk("An OpenPlex update fails"), hint: tk("When an update is undone or fails.") },
};

const stateTone = { firing: "danger", resolved: "success", sent: "neutral" } as const;
const stateLabels = { firing: tk("Ongoing"), resolved: tk("Resolved"), sent: tk("Sent") };

type Form = AlertSettings & { smtp_password?: string };

export default function AlertsPage() {
  const { t } = useT();
  const { data, error, isLoading } = useAlerts();
  const [f, setF] = useState<Form | null>(null);
  const [recipients, setRecipients] = useState("");
  useEffect(() => {
    if (data) {
      setF({ ...data.settings, smtp_password: undefined });
      setRecipients(data.settings.recipients.join("\n"));
    }
  }, [data]);

  const body = () => (f ? { ...f, recipients: recipients.split(/[\s,;]+/).filter(Boolean) } : {});
  const save = useAction(() => api.put<AlertsStatus>("/system/alerts", body()), { success: t("Saved"), invalidate: [keys.alerts] });
  const test = useAction(() => api.post<{ sent_to: string[] }>("/system/alerts/test", body()), {
    onSuccess: (r) => toast.success(t("Test e-mail sent to {to}. Check the inbox (and the spam folder).", { to: r.sent_to.join(", ") })),
  });

  if (isLoading || !f) {
    return error ? (
      <ErrorBox error={error} />
    ) : (
      <div className="flex justify-center py-20">
        <Spinner />
      </div>
    );
  }
  if (!data) return null;
  const set = <K extends keyof Form>(k: K, v: Form[K]) => setF((s) => (s ? { ...s, [k]: v } : s));

  return (
    <>
      <PageHeader title={t("Alerts")} description={t("E-mails to the administrators when something on the server needs attention.")} />
      <form
        className="grid gap-4 lg:grid-cols-2"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate(undefined);
        }}
      >
        <Card>
          <CardHeader
            title={t("E-mail alerts")}
            description={t("Checked every minute. A problem is e-mailed once, reminded every day while it lasts, and reported again when it is over.")}
            action={<Switch checked={f.enabled} onCheckedChange={(v) => set("enabled", v)} aria-label={t("Send alerts")} />}
          />
          <CardBody className="grid gap-4">
            <Field
              label={t("Recipients")}
              hint={
                <>
                  {t("One address per line, up to 10.")}{" "}
                  {data.admin_emails.length > 0 && (
                    <button type="button" className="text-primary hover:underline" onClick={() => setRecipients(data.admin_emails.join("\n"))}>
                      {t("Use the administrators' addresses")}
                    </button>
                  )}
                </>
              }
            >
              <Textarea rows={3} className="font-mono text-sm" value={recipients} onChange={(e) => setRecipients(e.target.value)} placeholder="admin@example.com" />
            </Field>
            <Field label={t("Language of the e-mails")}>
              <Select value={f.language} onChange={(e) => set("language", e.target.value)}>
                <option value="en">English</option>
                <option value="es">Español</option>
                <option value="ca">Català</option>
              </Select>
            </Field>
            <div className="grid gap-2">
              <p className="text-sm font-medium">{t("Send an e-mail when")}</p>
              {data.kinds.map((k) => (
                <label key={k} className="flex items-start gap-2 text-sm">
                  <input
                    type="checkbox"
                    className="mt-1"
                    checked={f.events.includes(k)}
                    onChange={(e) => set("events", e.target.checked ? [...f.events, k] : f.events.filter((x) => x !== k))}
                  />
                  <span>
                    {t(kindLabels[k]?.label ?? k)}
                    <span className="block text-xs text-muted-foreground">{t(kindLabels[k]?.hint ?? "")}</span>
                  </span>
                </label>
              ))}
            </div>
          </CardBody>
        </Card>

        <Card>
          <CardHeader title={t("Sending")} description={t("How the panel sends the e-mails.")} />
          <CardBody className="grid gap-4">
            <Field label={t("Send through")}>
              <Select value={f.transport} onChange={(e) => set("transport", e.target.value as Form["transport"])}>
                <option value="local">{t("This server's mail service")}</option>
                <option value="smtp">{t("An SMTP server (e.g. your provider's)")}</option>
              </Select>
            </Field>
            {f.transport === "local" ? (
              <p className={`text-sm ${data.mail_running ? "text-muted-foreground" : "text-destructive"}`}>
                {data.mail_running
                  ? t("The mail service is running. For the best delivery, use a sender address of a mail domain hosted here, whose SPF record allows this server.")
                  : t("The mail service of this server is off: turn it on under Mail, or send through an SMTP server.")}
              </p>
            ) : (
              <>
                <div className="grid grid-cols-[1fr_6rem] gap-3">
                  <Field label={t("SMTP server")}>
                    <Input required value={f.smtp_host} onChange={(e) => set("smtp_host", e.target.value)} placeholder="smtp.example.com" />
                  </Field>
                  <Field label={t("Port")}>
                    <Input required type="number" min={1} max={65535} value={f.smtp_port} onChange={(e) => set("smtp_port", Number(e.target.value))} />
                  </Field>
                </div>
                <Field label={t("Security")}>
                  <Select value={f.smtp_security} onChange={(e) => set("smtp_security", e.target.value as Form["smtp_security"])}>
                    <option value="starttls">{t("STARTTLS (usually port 587)")}</option>
                    <option value="tls">{t("TLS (usually port 465)")}</option>
                    <option value="none">{t("None (only for a relay on a trusted network)")}</option>
                  </Select>
                </Field>
                <div className="grid gap-3 sm:grid-cols-2">
                  <Field label={t("User")}>
                    <Input autoComplete="off" value={f.smtp_user} onChange={(e) => set("smtp_user", e.target.value)} />
                  </Field>
                  <Field label={t("Password")} hint={data.smtp_password_set ? t("Leave empty to keep the saved one.") : undefined}>
                    <Input
                      type="password"
                      autoComplete="new-password"
                      value={f.smtp_password ?? ""}
                      placeholder={data.smtp_password_set ? "••••••••" : ""}
                      onChange={(e) => set("smtp_password", e.target.value || undefined)}
                    />
                  </Field>
                </div>
              </>
            )}
            <Field label={t("Sender address")} hint={t("Empty: {address}", { address: data.default_from })}>
              <Input type="email" value={f.from_address} onChange={(e) => set("from_address", e.target.value)} placeholder={data.default_from} />
            </Field>
          </CardBody>
        </Card>

        <div className="grid gap-3 lg:col-span-2">
          <ErrorBox error={save.error ?? test.error} />
          <div className="flex flex-wrap justify-end gap-2">
            <Button type="button" variant="outline" loading={test.isPending} onClick={() => test.mutate(undefined)}>
              <Send /> {t("Send a test e-mail")}
            </Button>
            <Button type="submit" loading={save.isPending}>
              {t("Save")}
            </Button>
          </div>
        </div>
      </form>

      <Card className="mt-4">
        <CardHeader title={t("Recent alerts")} description={t("The last 90 days.")} />
        {data.recent.length === 0 ? (
          <CardBody>
            <p className="text-sm text-muted-foreground">{t("No alerts yet.")}</p>
          </CardBody>
        ) : (
          <div className="overflow-x-auto">
            <Table>
              <thead>
                <tr>
                  <Th>{t("Since")}</Th>
                  <Th>{t("Alert")}</Th>
                  <Th>{t("State")}</Th>
                </tr>
              </thead>
              <tbody>
                {data.recent.map((a) => (
                  <Row key={a.key} a={a} />
                ))}
              </tbody>
            </Table>
          </div>
        )}
      </Card>
    </>
  );
}

function Row({ a }: { a: AlertEntry }) {
  const { t } = useT();
  return (
    <tr>
      <Td className="whitespace-nowrap text-muted-foreground">{dateTime(a.first_at)}</Td>
      <Td>
        <span>{a.subject}</span>
        {a.send_error && <span className="block text-xs text-destructive">{t("Not sent: {error}", { error: tServer(a.send_error) })}</span>}
      </Td>
      <Td>
        <Badge tone={stateTone[a.state]}>{t(stateLabels[a.state])}</Badge>
      </Td>
    </tr>
  );
}
