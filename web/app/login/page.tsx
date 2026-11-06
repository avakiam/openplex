"use client";
import { ShieldCheck } from "lucide-react";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useEffect, useState } from "react";

import { Logo } from "@/components/app-shell";
import { Button, Card, ErrorBox, Field, Input, Spinner } from "@/components/ui";
import { ApiError, login } from "@/lib/api";
import { useAuth } from "@/lib/auth";

function safeNext(n: string | null) {
  // Only allow same-site relative paths (no protocol-relative //evil.com).
  return n && n.startsWith("/") && !n.startsWith("//") ? n : "/";
}

function LoginForm() {
  const router = useRouter();
  const params = useSearchParams();
  const { user, ready } = useAuth();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [needTotp, setNeedTotp] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const next = safeNext(params.get("next"));

  useEffect(() => {
    if (ready && user) router.replace(next);
  }, [ready, user, router, next]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await login(username, password, needTotp ? code : undefined);
      router.replace(next);
    } catch (err) {
      if (err instanceof ApiError && err.code === "totp_required") {
        setNeedTotp(true);
      } else {
        setError(err instanceof Error ? err.message : "Sign-in failed");
        if (err instanceof ApiError && err.code === "totp_invalid") setCode("");
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card className="w-full max-w-sm p-7 shadow-sm">
      <Logo className="mb-6 text-lg" />
      <h1 className="text-xl font-semibold">{needTotp ? "Two-factor verification" : "Sign in"}</h1>
      <p className="mt-1 text-sm text-muted-foreground">
        {needTotp ? "Enter the 6-digit code from your authenticator app." : "Manage your websites, domains and servers."}
      </p>
      <form onSubmit={submit} className="mt-6 grid gap-4">
        {!needTotp ? (
          <>
            <Field label="Username">
              <Input
                autoComplete="username"
                autoFocus
                required
                value={username}
                onChange={(e) => setUsername(e.target.value)}
              />
            </Field>
            <Field label="Password">
              <Input
                type="password"
                autoComplete="current-password"
                required
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </Field>
          </>
        ) : (
          <Field label="Authentication code">
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
        <ErrorBox error={error} />
        <Button type="submit" loading={busy} className="w-full">
          {needTotp ? (
            <>
              <ShieldCheck /> Verify
            </>
          ) : (
            "Sign in"
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
            }}
          >
            Back
          </Button>
        )}
      </form>
    </Card>
  );
}

export default function LoginPage() {
  return (
    <div className="flex min-h-dvh items-center justify-center bg-background p-4">
      <Suspense fallback={<Spinner />}>
        <LoginForm />
      </Suspense>
    </div>
  );
}
