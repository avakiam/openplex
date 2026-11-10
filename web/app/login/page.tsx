"use client";
import { ShieldCheck } from "lucide-react";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useEffect, useState } from "react";

import { Logo } from "@/components/app-shell";
import { CaptchaWidget } from "@/components/captcha";
import { LanguageSelect } from "@/components/language-select";
import { Button, Card, ErrorBox, Field, Input, Spinner } from "@/components/ui";
import { API, ApiError, login } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { useT } from "@/lib/i18n";
import type { CaptchaProvider } from "@/lib/types";

function safeNext(n: string | null) {
  // Only paths on this origin: resolving catches "//evil.com", backslash and tab tricks…
  if (!n || !n.startsWith("/") || typeof window === "undefined") return "/";
  try {
    const u = new URL(n, window.location.origin);
    return u.origin === window.location.origin ? u.pathname + u.search + u.hash : "/";
  } catch {
    return "/";
  }
}

function LoginForm() {
  const router = useRouter();
  const params = useSearchParams();
  const { user, ready } = useAuth();
  const { t, locale } = useT();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [needTotp, setNeedTotp] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const next = safeNext(params.get("next"));
  const [captcha, setCaptcha] = useState<{ provider: Exclude<CaptchaProvider, "">; site_key: string } | null>(null);
  const [captchaToken, setCaptchaToken] = useState("");
  const [captchaPass, setCaptchaPass] = useState<string | undefined>();
  const [resetSignal, setResetSignal] = useState(0);

  useEffect(() => {
    fetch(`${API}/auth/captcha`, { credentials: "same-origin" })
      .then((r) => (r.ok ? r.json() : null))
      .then((c: { provider: CaptchaProvider; site_key: string } | null) => {
        if (c?.provider && c.site_key) setCaptcha({ provider: c.provider, site_key: c.site_key });
      })
      .catch(() => {});
  }, []);

  useEffect(() => {
    if (ready && user) router.replace(next);
  }, [ready, user, router, next]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await login(username, password, needTotp ? code : undefined, { token: captchaToken, pass: captchaPass });
      router.replace(next);
    } catch (err) {
      // A token works once: the next attempt needs a new one (or the pass).
      setResetSignal((n) => n + 1);
      setCaptchaPass(err instanceof ApiError ? err.captchaPass : undefined);
      if (err instanceof ApiError && err.code === "totp_required") {
        setNeedTotp(true);
      } else {
        setError(err instanceof Error ? err.message : t("Sign-in failed"));
        if (err instanceof ApiError && err.code === "totp_invalid") setCode("");
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card className="w-full max-w-sm p-7 shadow-sm">
      <Logo className="mb-6 text-lg" />
      <h1 className="text-xl font-semibold">{needTotp ? t("Two-factor verification") : t("Sign in")}</h1>
      <p className="mt-1 text-sm text-muted-foreground">
        {needTotp ? t("Enter the 6-digit code from your authenticator app.") : t("Manage your websites, domains and servers.")}
      </p>
      <form onSubmit={submit} className="mt-6 grid gap-4">
        {!needTotp ? (
          <>
            <Field label={t("Username")}>
              <Input autoComplete="username" autoFocus required value={username} onChange={(e) => setUsername(e.target.value)} />
            </Field>
            <Field label={t("Password")}>
              <Input type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
            </Field>
          </>
        ) : (
          <Field label={t("Authentication code")}>
            <Input
              inputMode="numeric"
              autoComplete="one-time-code"
              pattern="[0-9]{6}"
              maxLength={6}
              autoFocus
              required
              className="tabular text-center text-lg tracking-[0.5em]"
              value={code}
              onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
            />
          </Field>
        )}
        {captcha && !captchaPass && (
          <CaptchaWidget
            provider={captcha.provider}
            siteKey={captcha.site_key}
            lang={locale}
            resetSignal={resetSignal}
            onToken={setCaptchaToken}
            onError={() => setError(t("The anti-robot check could not load. Reload the page and try again."))}
          />
        )}
        <ErrorBox error={error} />
        <Button type="submit" loading={busy} disabled={!!captcha && !captchaPass && !captchaToken} className="w-full">
          {needTotp ? (
            <>
              <ShieldCheck /> {t("Verify")}
            </>
          ) : (
            t("Sign in")
          )}
        </Button>
        {needTotp && (
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              setNeedTotp(false);
              setCode("");
              setError(null);
              setCaptchaPass(undefined);
            }}
          >
            {t("Back")}
          </Button>
        )}
      </form>
    </Card>
  );
}

export default function LoginPage() {
  return (
    <div className="relative flex min-h-dvh items-center justify-center bg-background p-4">
      <div className="absolute right-4 top-4">
        <LanguageSelect />
      </div>
      <Suspense fallback={<Spinner />}>
        <LoginForm />
      </Suspense>
    </div>
  );
}
