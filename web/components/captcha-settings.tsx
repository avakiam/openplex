"use client";
import { ExternalLink, Info, TriangleAlert } from "lucide-react";
import { useEffect, useState } from "react";

import { CaptchaWidget } from "@/components/captcha";
import { Button, Card, CardBody, CardHeader, ErrorBox, Field, Input, Select, Spinner, Switch } from "@/components/ui";
import { api } from "@/lib/api";
import { keys, useAction, useCaptcha, useMail } from "@/lib/hooks";
import { tk, useT } from "@/lib/i18n";
import type { CaptchaProvider, CaptchaSettings } from "@/lib/types";

const providers: { id: Exclude<CaptchaProvider, "">; name: string; console: string; note: string }[] = [
  {
    id: "turnstile",
    name: "Cloudflare Turnstile",
    console: "https://dash.cloudflare.com/?to=/:account/turnstile",
    note: tk("Free and usually invisible: most visitors don't have to click anything."),
  },
  { id: "hcaptcha", name: "hCaptcha", console: "https://dashboard.hcaptcha.com/sites", note: tk("Free plan; privacy-focused.") },
  {
    id: "recaptcha",
    name: "Google reCAPTCHA v2",
    console: "https://www.google.com/recaptcha/admin/create",
    note: tk("Create a v2 “I'm not a robot” checkbox key (v3 keys don't work here)."),
  },
];

/** value, after it stopped changing for a moment (the widget reloads on each key). */
function useSettled(value: string, ms = 700) {
  const [v, setV] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setV(value), ms);
    return () => clearTimeout(id);
  }, [value, ms]);
  return v;
}

function Form({ cur }: { cur: CaptchaSettings }) {
  const { t, locale } = useT();
  const { data: mail } = useMail();
  const [provider, setProvider] = useState<CaptchaProvider>(cur.provider);
  const [siteKey, setSiteKey] = useState(cur.site_key);
  const [secret, setSecret] = useState("");
  const [panel, setPanel] = useState(cur.panel);
  const [webmail, setWebmail] = useState(cur.webmail);
  const [token, setToken] = useState("");
  const [reset, setReset] = useState(0);
  const settledKey = useSettled(siteKey.trim());
  const p = providers.find((x) => x.id === provider);
  const changed = provider !== cur.provider || siteKey.trim() !== cur.site_key || secret !== "" || (panel && !cur.panel) || (webmail && !cur.webmail);
  const needsTest = !!provider && (panel || webmail) && changed;

  const save = useAction(() => api.put<CaptchaSettings>("/captcha", { provider, site_key: siteKey, secret, panel, webmail, test_token: token }), {
    success: t("Captcha settings saved"),
    invalidate: [keys.captcha],
    onSuccess: () => setSecret(""),
    onError: () => setReset((n) => n + 1),
  });

  const hosts = [
    typeof window !== "undefined" ? window.location.hostname : "",
    mail?.status.webmail_url ? new URL(mail.status.webmail_url).hostname : "",
  ].filter(Boolean);

  return (
    <form
      className="grid gap-5"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate(undefined);
      }}
    >
      <Field label={t("Captcha provider")}>
        <Select value={provider} onChange={(e) => setProvider(e.target.value as CaptchaProvider)}>
          <option value="">{t("None")}</option>
          {providers.map((x) => (
            <option key={x.id} value={x.id}>
              {x.name}
            </option>
          ))}
        </Select>
      </Field>
      {p && (
        <>
          <p className="flex gap-2 text-sm text-muted-foreground">
            <Info className="mt-0.5 size-4 shrink-0" />
            <span>
              {t(p.note)}{" "}
              <a className="inline-flex items-center gap-1 font-medium text-foreground hover:underline" href={p.console} target="_blank" rel="noreferrer">
                {t("Get the keys")} <ExternalLink className="size-3" />
              </a>
              {hosts.length > 0 && <span className="block">{t("Allow these host names for the key: {hosts}.", { hosts: hosts.join(", ") })}</span>}
            </span>
          </p>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={t("Site key")}>
              <Input className="font-mono" value={siteKey} onChange={(e) => setSiteKey(e.target.value)} autoComplete="off" spellCheck={false} />
            </Field>
            <Field label={t("Secret key")} hint={cur.secret_set && cur.provider === provider ? t("Leave empty to keep the saved one.") : undefined}>
              <Input
                className="font-mono"
                type="password"
                value={secret}
                onChange={(e) => setSecret(e.target.value)}
                autoComplete="new-password"
                placeholder={cur.secret_set && cur.provider === provider ? "••••••••" : ""}
              />
            </Field>
          </div>
          <div className="grid gap-3">
            <label className="flex items-start gap-3">
              <Switch checked={panel} onCheckedChange={setPanel} className="mt-0.5" aria-label={t("Panel login")} />
              <span>
                <span className="block font-medium">{t("Panel login")}</span>
                <span className="text-sm text-muted-foreground">{t("Asked before the password on this panel's sign-in page.")}</span>
              </span>
            </label>
            <label className="flex items-start gap-3">
              <Switch checked={webmail} onCheckedChange={setWebmail} className="mt-0.5" aria-label={t("Webmail login")} />
              <span>
                <span className="block font-medium">{t("Webmail login")}</span>
                <span className="text-sm text-muted-foreground">{t("Asked on the webmail's sign-in page. Opening the webmail from the panel skips it.")}</span>
              </span>
            </label>
          </div>
          {needsTest && settledKey.length >= 8 && (
            <div className="grid gap-2 rounded-md border bg-muted/40 p-4">
              <p className="text-sm">{t("Solve this captcha to check the keys before turning it on:")}</p>
              <CaptchaWidget key={provider + settledKey} provider={p.id} siteKey={settledKey} lang={locale} resetSignal={reset} onToken={setToken} />
            </div>
          )}
        </>
      )}
      <ErrorBox error={save.error} />
      <div className="flex justify-end">
        <Button type="submit" loading={save.isPending} disabled={!!provider && ((needsTest && !token) || !siteKey.trim())}>
          {t("Save settings")}
        </Button>
      </div>
    </form>
  );
}

export function CaptchaSettingsCard() {
  const { t } = useT();
  const { data, isLoading, error } = useCaptcha();
  return (
    <Card>
      <CardHeader title={t("Anti-robot check (captcha)")} description={t("Stops bots from guessing passwords on the panel and webmail sign-in pages.")} />
      <CardBody className="grid gap-4">
        {isLoading ? (
          <Spinner />
        ) : error || !data ? (
          <ErrorBox error={error} />
        ) : (
          <>
            {data.disabled_by_config && (
              <p className="flex gap-2 rounded-md border border-warning/40 bg-warning/10 p-3 text-sm">
                <TriangleAlert className="mt-0.5 size-4 shrink-0 text-warning" />
                {t("The panel login doesn't ask for it: OPENPLEX_CAPTCHA=false is set on this server.")}
              </p>
            )}
            <Form key={data.provider + data.site_key + data.panel + data.webmail} cur={data} />
          </>
        )}
      </CardBody>
    </Card>
  );
}
