"use client";
// Login captcha widget: Cloudflare Turnstile, hCaptcha or Google reCAPTCHA v2,
// rendered explicitly so it can be reset after every attempt (tokens are
// single use). The API checks the token; the widget only produces it.
import { useEffect, useRef } from "react";

import type { CaptchaProvider } from "@/lib/types";

interface WidgetAPI {
  render(el: HTMLElement, opts: Record<string, unknown>): string | number;
  reset(id?: string | number): void;
  remove?(id?: string | number): void;
}

const sources: Record<Exclude<CaptchaProvider, "">, { url: string; global: string }> = {
  turnstile: { url: "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit", global: "turnstile" },
  hcaptcha: { url: "https://js.hcaptcha.com/1/api.js?render=explicit&recaptchacompat=off", global: "hcaptcha" },
  recaptcha: { url: "https://www.google.com/recaptcha/api.js?render=explicit", global: "grecaptcha" },
};

const loading: Partial<Record<string, Promise<WidgetAPI>>> = {};

function load(provider: Exclude<CaptchaProvider, "">, lang: string): Promise<WidgetAPI> {
  const key = provider + "|" + lang;
  if (!loading[key]) {
    loading[key] = new Promise<WidgetAPI>((resolve, reject) => {
      const src = sources[provider];
      const cb = `opxCaptcha_${provider}`;
      const w = window as unknown as Record<string, unknown>;
      w[cb] = () => resolve(w[src.global] as WidgetAPI);
      const s = document.createElement("script");
      s.src = `${src.url}&onload=${cb}${provider === "turnstile" ? "" : `&hl=${encodeURIComponent(lang)}`}`;
      s.async = true;
      s.onerror = () => {
        delete loading[key];
        reject(new Error("captcha script failed to load"));
      };
      document.head.appendChild(s);
    });
  }
  return loading[key];
}

export function CaptchaWidget({
  provider,
  siteKey,
  lang,
  resetSignal,
  onToken,
  onError,
}: {
  provider: Exclude<CaptchaProvider, "">;
  siteKey: string;
  lang: string;
  /** Changing this number clears the widget for a new attempt. */
  resetSignal?: number;
  onToken: (token: string) => void;
  onError?: () => void;
}) {
  const el = useRef<HTMLDivElement>(null);
  const widget = useRef<{ api: WidgetAPI; id: string | number } | null>(null);
  const handlers = useRef({ onToken, onError });
  handlers.current = { onToken, onError };

  useEffect(() => {
    let cancelled = false;
    const box = el.current;
    load(provider, lang)
      .then((api) => {
        if (cancelled || !box) return;
        box.innerHTML = "";
        const target = document.createElement("div");
        box.appendChild(target);
        const id = api.render(target, {
          sitekey: siteKey,
          ...(provider === "turnstile" ? { language: lang, theme: "auto" } : {}),
          callback: (token: string) => handlers.current.onToken(token),
          "expired-callback": () => handlers.current.onToken(""),
          "error-callback": () => {
            handlers.current.onToken("");
            handlers.current.onError?.();
          },
        });
        widget.current = { api, id };
      })
      .catch(() => handlers.current.onError?.());
    return () => {
      cancelled = true;
      const w = widget.current;
      widget.current = null;
      try {
        w?.api.remove?.(w.id);
      } catch {
        /* already gone */
      }
      if (box) box.innerHTML = "";
    };
  }, [provider, siteKey, lang]);

  useEffect(() => {
    if (!resetSignal || !widget.current) return;
    try {
      widget.current.api.reset(widget.current.id);
    } catch {
      /* widget not ready */
    }
    handlers.current.onToken("");
  }, [resetSignal]);

  return <div ref={el} className="flex min-h-[65px] justify-center" />;
}
