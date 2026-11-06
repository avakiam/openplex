"use client";
import { ShieldCheck, ShieldOff } from "lucide-react";
import QRCode from "qrcode";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";

import { Badge, Button, Card, CardBody, CardHeader, ErrorBox, Field, Input, PageHeader } from "@/components/ui";
import { api, refresh } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { useAction } from "@/lib/hooks";

function PasswordCard() {
  const router = useRouter();
  const [cur, setCur] = useState("");
  const [next, setNext] = useState("");
  const [again, setAgain] = useState("");
  const save = useAction(
    async () => {
      if (next !== again) throw new Error("The new passwords do not match");
      await api.post("/auth/password", { current_password: cur, new_password: next });
    },
    { success: "Password changed — please sign in again", onSuccess: () => router.replace("/login/") },
  );
  return (
    <Card>
      <CardHeader title="Password" description="Changing it signs you out everywhere." />
      <CardBody>
        <form className="grid max-w-sm gap-4" onSubmit={(e) => { e.preventDefault(); save.mutate(undefined); }}>
          <Field label="Current password"><Input type="password" required autoComplete="current-password" value={cur} onChange={(e) => setCur(e.target.value)} /></Field>
          <Field label="New password" hint="At least 10 characters with letters and digits."><Input type="password" required minLength={10} autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} /></Field>
          <Field label="Repeat new password"><Input type="password" required minLength={10} autoComplete="new-password" value={again} onChange={(e) => setAgain(e.target.value)} /></Field>
          <ErrorBox error={save.error} />
          <div><Button type="submit" loading={save.isPending}>Change password</Button></div>
        </form>
      </CardBody>
    </Card>
  );
}

function TwoFactorCard() {
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
    success: "Two-factor authentication enabled",
    onSuccess: () => { setSetup(null); setCode(""); refresh(); },
  });
  const disable = useAction(() => api.post("/auth/2fa/disable", { password: pw, code }), {
    success: "Two-factor authentication disabled",
    onSuccess: () => { setPw(""); setCode(""); refresh(); },
  });

  return (
    <Card>
      <CardHeader
        title="Two-factor authentication"
        description="Require a code from an authenticator app (Google Authenticator, 1Password, Aegis…) at sign-in."
        action={user?.totp_enabled ? <Badge tone="success"><ShieldCheck className="size-3" /> Enabled</Badge> : <Badge>Off</Badge>}
      />
      <CardBody>
        {user?.totp_enabled ? (
          <form className="grid max-w-sm gap-4" onSubmit={(e) => { e.preventDefault(); disable.mutate(undefined); }}>
            <p className="text-sm text-muted-foreground">To turn it off, confirm your password and a current code.</p>
            <Field label="Password"><Input type="password" required value={pw} onChange={(e) => setPw(e.target.value)} /></Field>
            <Field label="Code"><Input inputMode="numeric" required maxLength={6} className="tabular tracking-widest" value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} /></Field>
            <ErrorBox error={disable.error} />
            <div><Button type="submit" variant="destructive" loading={disable.isPending}><ShieldOff /> Disable 2FA</Button></div>
          </form>
        ) : setup ? (
          <form className="grid gap-5 sm:grid-cols-[200px_1fr]" onSubmit={(e) => { e.preventDefault(); enable.mutate(undefined); }}>
            {/* eslint-disable-next-line @next/next/no-img-element */}
            {qr ? <img src={qr} alt="QR code for your authenticator app" className="size-[200px] rounded-md border bg-white p-1" /> : <div className="size-[200px] rounded-md border" />}
            <div className="grid content-start gap-4">
              <p className="text-sm">1. Scan the QR code with your authenticator app, or enter this key manually:</p>
              <code className="break-all rounded bg-muted px-2 py-1.5 text-sm">{setup.secret}</code>
              <Field label="2. Enter the 6-digit code it shows">
                <Input inputMode="numeric" required autoFocus maxLength={6} className="tabular max-w-40 tracking-widest" value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} />
              </Field>
              <ErrorBox error={enable.error} />
              <div className="flex gap-2">
                <Button type="submit" loading={enable.isPending}><ShieldCheck /> Verify & enable</Button>
                <Button type="button" variant="outline" onClick={() => setSetup(null)}>Cancel</Button>
              </div>
            </div>
          </form>
        ) : (
          <Button loading={start.isPending} onClick={() => start.mutate(undefined)}><ShieldCheck /> Set up two-factor authentication</Button>
        )}
      </CardBody>
    </Card>
  );
}

export default function AccountPage() {
  const { user } = useAuth();
  return (
    <>
      <PageHeader title="Account & security" description={`Signed in as ${user?.username} (${user?.role}).`} />
      <div className="grid gap-4 lg:grid-cols-2">
        <TwoFactorCard />
        <PasswordCard />
      </div>
    </>
  );
}
