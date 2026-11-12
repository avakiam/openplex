"use client";
import { ShieldCheck, ShieldOff } from "lucide-react";
import QRCode from "qrcode";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";

import { roleLabels } from "@/components/status";
import { Badge, Button, Card, CardBody, CardHeader, ErrorBox, Field, Input, PageHeader, Select } from "@/components/ui";
import { api, logout, refresh } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { useAction } from "@/lib/hooks";
import { LOCALES, browserLocale, useT, type LocalePref } from "@/lib/i18n";

function PasswordCard() {
  const { t } = useT();
  const router = useRouter();
  const [cur, setCur] = useState("");
  // Right after a one-time sign-in link the account's owner doesn't know its
  // password yet: the API lets them set one without it.
  const [byLink, setByLink] = useState(false);
  useEffect(() => {
    setByLink(new URLSearchParams(window.location.search).get("from") === "link");
  }, []);
  const [next, setNext] = useState("");
  const [again, setAgain] = useState("");
  const save = useAction(
    async () => {
      if (next !== again) throw new Error(t("The new passwords do not match"));
      await api.post("/auth/password", { current_password: byLink ? "" : cur, new_password: next });
      await logout(); // the change ended every session: this one too
    },
    { success: t("Password changed — please sign in again"), onSuccess: () => router.replace("/login/") },
  );
  return (
    <Card>
      <CardHeader title={t("Password")} description={t("Changing it signs you out everywhere.")} />
      <CardBody>
        <form className="grid max-w-sm gap-4" onSubmit={(e) => { e.preventDefault(); save.mutate(undefined); }}>
          {byLink ? (
            <p className="text-sm text-muted-foreground">{t("You signed in with a one-time link: choose the password you'll sign in with from now on.")}</p>
          ) : (
            <Field label={t("Current password")}><Input type="password" required autoComplete="current-password" value={cur} onChange={(e) => setCur(e.target.value)} /></Field>
          )}
          <Field label={t("New password")} hint={t("At least 10 characters with letters and digits.")}><Input type="password" required minLength={10} autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} /></Field>
          <Field label={t("Repeat new password")}><Input type="password" required minLength={10} autoComplete="new-password" value={again} onChange={(e) => setAgain(e.target.value)} /></Field>
          <ErrorBox error={save.error} />
          <div><Button type="submit" loading={save.isPending}>{t("Change password")}</Button></div>
        </form>
      </CardBody>
    </Card>
  );
}

function TwoFactorCard() {
  const { t } = useT();
  const { user } = useAuth();
  const [setup, setSetup] = useState<{ secret: string; otpauth_url: string } | null>(null);
  const [qr, setQr] = useState("");
  const [code, setCode] = useState("");
  const [pw, setPw] = useState("");

  useEffect(() => {
    if (setup) QRCode.toDataURL(setup.otpauth_url, { margin: 1, width: 200 }).then(setQr);
  }, [setup]);

  const start = useAction(() => api.post<{ secret: string; otpauth_url: string }>("/auth/2fa/setup"), { onSuccess: setSetup });
  const enable = useAction(() => api.post("/auth/2fa/enable", { code }), {
    success: t("Two-factor authentication enabled"),
    onSuccess: () => { setSetup(null); setCode(""); refresh(); },
  });
  const disable = useAction(() => api.post("/auth/2fa/disable", { password: pw, code }), {
    success: t("Two-factor authentication disabled"),
    onSuccess: () => { setPw(""); setCode(""); refresh(); },
  });

  return (
    <Card>
      <CardHeader
        title={t("Two-factor authentication")}
        description={t("Require a code from an authenticator app (Google Authenticator, 1Password, Aegis…) at sign-in.")}
        action={user?.totp_enabled ? <Badge tone="success"><ShieldCheck className="size-3" /> {t("Enabled")}</Badge> : <Badge>{t("Disabled")}</Badge>}
      />
      <CardBody>
        {user?.totp_enabled ? (
          <form className="grid max-w-sm gap-4" onSubmit={(e) => { e.preventDefault(); disable.mutate(undefined); }}>
            <p className="text-sm text-muted-foreground">{t("To turn it off, confirm your password and a current code.")}</p>
            <Field label={t("Password")}><Input type="password" required value={pw} onChange={(e) => setPw(e.target.value)} /></Field>
            <Field label={t("Code")}><Input inputMode="numeric" required maxLength={6} className="tabular tracking-widest" value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} /></Field>
            <ErrorBox error={disable.error} />
            <div><Button type="submit" variant="destructive" loading={disable.isPending}><ShieldOff /> {t("Disable 2FA")}</Button></div>
          </form>
        ) : setup ? (
          <form className="grid gap-5 sm:grid-cols-[200px_1fr]" onSubmit={(e) => { e.preventDefault(); enable.mutate(undefined); }}>
            {/* eslint-disable-next-line @next/next/no-img-element */}
            {qr ? <img src={qr} alt={t("QR code for your authenticator app")} className="size-[200px] rounded-md border bg-white p-1" /> : <div className="size-[200px] rounded-md border" />}
            <div className="grid content-start gap-4">
              <p className="text-sm">{t("1. Scan the QR code with your authenticator app, or enter this key manually:")}</p>
              <code className="break-all rounded bg-muted px-2 py-1.5 text-sm">{setup.secret}</code>
              <Field label={t("2. Enter the 6-digit code it shows")}>
                <Input inputMode="numeric" required autoFocus maxLength={6} className="tabular max-w-40 tracking-widest" value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} />
              </Field>
              <ErrorBox error={enable.error} />
              <div className="flex gap-2">
                <Button type="submit" loading={enable.isPending}><ShieldCheck /> {t("Verify & enable")}</Button>
                <Button type="button" variant="outline" onClick={() => setSetup(null)}>{t("Cancel")}</Button>
              </div>
            </div>
          </form>
        ) : (
          <Button loading={start.isPending} onClick={() => start.mutate(undefined)}><ShieldCheck /> {t("Set up two-factor authentication")}</Button>
        )}
      </CardBody>
    </Card>
  );
}

function LanguageCard() {
  const { t, pref, setPref } = useT();
  const auto = LOCALES.find((l) => l.code === browserLocale())!.name;
  return (
    <Card>
      <CardHeader title={t("Language")} description={t("The language of the panel for your account.")} />
      <CardBody>
        <div className="grid max-w-sm gap-4">
          <Field label={t("Language")} hint={t("Automatic follows your browser's language.")}>
            <Select value={pref} onChange={(e) => setPref(e.target.value as LocalePref)}>
              <option value="">{t("Automatic ({language})", { language: auto })}</option>
              {LOCALES.map((l) => (
                <option key={l.code} value={l.code}>
                  {l.name}
                </option>
              ))}
            </Select>
          </Field>
        </div>
      </CardBody>
    </Card>
  );
}

export default function AccountPage() {
  const { t } = useT();
  const { user } = useAuth();
  return (
    <>
      <PageHeader
        title={t("Account & security")}
        description={user ? t("Signed in as {username} ({role}).", { username: user.username, role: t(roleLabels[user.role]) }) : undefined}
      />
      <div className="grid gap-4 lg:grid-cols-2">
        <TwoFactorCard />
        <PasswordCard />
        <LanguageCard />
      </div>
    </>
  );
}
