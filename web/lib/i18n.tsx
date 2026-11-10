"use client";
// Minimal i18n: English source strings are the keys (gettext style), so an
// untranslated string still renders in English. `t` works outside React too
// (toasts, API errors); components call useT() so they re-render on change.
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";

import { catalogues, type Entry } from "@/locales";

import { api, onSessionChange } from "./api";
import { useAuth } from "./auth";

export type Locale = "en" | "es" | "ca";
/** "" means "follow the browser". */
export type LocalePref = Locale | "";

export const LOCALES: { code: Locale; name: string }[] = [
  { code: "en", name: "English" },
  { code: "es", name: "Español" },
  { code: "ca", name: "Català" },
];

const STORAGE_KEY = "opx-locale";
const supported = (l: string | null | undefined): l is Locale => l === "en" || l === "es" || l === "ca";

let current: Locale = "en";
let plurals = new Intl.PluralRules("en");

export const getLocale = () => current;

/** The browser's preferred supported language (Catalan, Spanish, else English). */
export function browserLocale(): Locale {
  const langs = typeof navigator === "undefined" ? [] : navigator.languages?.length ? navigator.languages : [navigator.language];
  for (const l of langs) {
    const base = l?.toLowerCase().split("-")[0];
    if (supported(base)) return base;
  }
  return "en";
}

function storedPref(): LocalePref {
  try {
    const v = localStorage.getItem(STORAGE_KEY);
    return supported(v) ? v : "";
  } catch {
    return "";
  }
}

function storePref(p: LocalePref) {
  try {
    if (p) localStorage.setItem(STORAGE_KEY, p);
    else localStorage.removeItem(STORAGE_KEY);
  } catch {
    /* storage unavailable */
  }
}

export type Vars = Record<string, string | number>;

/** Translates an English source string; {name} placeholders are filled from vars. A numeric `count` selects plural forms. */
export function t(key: string, vars?: Vars): string {
  const entry: Entry | undefined = catalogues[current][key] ?? catalogues.en[key];
  let s: string;
  if (entry === undefined) s = key;
  else if (typeof entry === "string") s = entry;
  else {
    const n = typeof vars?.count === "number" ? vars.count : 0;
    s = entry[plurals.select(n)] ?? entry.other ?? key;
  }
  if (!vars) return s;
  return s.replace(/\{(\w+)\}/g, (m, k: string) => (k in vars ? String(typeof vars[k] === "number" ? fmtNumber(vars[k] as number) : vars[k]) : m));
}

/** Marks a string for translation without translating it yet (module-level
 * constants); translate it with t(value) at render time. */
export const tk = (s: string) => s;

/** Translates a message that came from the API (server strings are English). */
export const tServer = (message: string) => (message in catalogues[current] || message in catalogues.en ? t(message) : message);

const numberFormats = new Map<string, Intl.NumberFormat>();
export function fmtNumber(n: number, digits?: number) {
  const k = `${current}:${digits ?? "auto"}`;
  let f = numberFormats.get(k);
  if (!f) {
    f = new Intl.NumberFormat(current, digits === undefined ? undefined : { minimumFractionDigits: digits, maximumFractionDigits: digits });
    numberFormats.set(k, f);
  }
  return f.format(n);
}

function apply(l: Locale) {
  current = l;
  plurals = new Intl.PluralRules(l);
  if (typeof document !== "undefined") document.documentElement.lang = l;
}

interface I18nState {
  locale: Locale;
  /** The saved preference; "" = automatic (browser language). */
  pref: LocalePref;
  setPref: (p: LocalePref) => void;
}

const I18nContext = createContext<I18nState>({
  locale: "en",
  pref: "",
  setPref: () => {},
});

export function I18nProvider({ children }: { children: ReactNode }) {
  const { user } = useAuth();
  const [pref, setPrefState] = useState<LocalePref>("");
  const [locale, setLocale] = useState<Locale>("en");

  const adopt = useCallback((p: LocalePref) => {
    const l = p || browserLocale();
    apply(l);
    setPrefState(p);
    setLocale(l);
  }, []);

  // First paint is English (static export); switch right after hydration.
  useEffect(() => adopt(storedPref()), [adopt]);

  // A signed-in user's saved language wins over this browser's.
  useEffect(() => {
    const off = onSessionChange((u) => {
      if (!u) return;
      const stored = storedPref();
      if (supported(u.locale)) {
        if (u.locale !== stored) {
          storePref(u.locale);
          adopt(u.locale);
        }
      } else if (stored) {
        // Chosen before signing in (e.g. on the login page): remember it on the account.
        api.patch("/auth/me", { locale: stored }).catch(() => {});
      }
    });
    return () => {
      off();
    };
  }, [adopt]);

  const setPref = useCallback(
    (p: LocalePref) => {
      storePref(p);
      adopt(p);
      if (user) api.patch("/auth/me", { locale: p }).catch(() => {});
    },
    [adopt, user],
  );

  const value = useMemo(() => ({ locale, pref, setPref }), [locale, pref, setPref]);
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

/** Subscribes the component to language changes and returns the translator. */
export function useT() {
  const ctx = useContext(I18nContext);
  return { t, ...ctx };
}
