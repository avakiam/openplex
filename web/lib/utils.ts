import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

import { fmtNumber, getLocale, t } from "./i18n";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

const units = ["B", "KB", "MB", "GB", "TB"];

export function bytes(n: number, digits = 1) {
  if (!Number.isFinite(n) || n <= 0) return "0 B";
  const i = Math.min(units.length - 1, Math.floor(Math.log(n) / Math.log(1024)));
  return `${fmtNumber(n / 1024 ** i, i === 0 ? 0 : digits)} ${units[i]}`;
}

export function rate(n: number) {
  return `${bytes(n)}/s`;
}

export function pct(n: number, digits = 0) {
  return `${fmtNumber(Number.isFinite(n) ? n : 0, digits)}%`;
}

export function duration(secs: number) {
  const d = Math.floor(secs / 86400);
  const h = Math.floor((secs % 86400) / 3600);
  const m = Math.floor((secs % 3600) / 60);
  // English keeps the compact "3d 4h"; Spanish and Catalan use spaced SI-style symbols ("3 d 4 h", "5 min").
  const [sd, sh, sm] = getLocale() === "en" ? ["d", "h", "m"] : [" d", " h", " min"];
  if (d > 0) return `${d}${sd} ${h}${sh}`;
  if (h > 0) return `${h}${sh} ${m}${sm}`;
  return `${m}${sm}`;
}

const rtfs = new Map<string, Intl.RelativeTimeFormat>();

export function ago(iso: string | null | undefined) {
  if (!iso) return t("never");
  let rtf = rtfs.get(getLocale());
  if (!rtf) {
    rtf = new Intl.RelativeTimeFormat(getLocale(), { numeric: "auto" });
    rtfs.set(getLocale(), rtf);
  }
  const s = (Date.parse(iso) - Date.now()) / 1000;
  const abs = Math.abs(s);
  if (abs < 60) return rtf.format(Math.round(s), "second");
  if (abs < 3600) return rtf.format(Math.round(s / 60), "minute");
  if (abs < 86400) return rtf.format(Math.round(s / 3600), "hour");
  return rtf.format(Math.round(s / 86400), "day");
}

export function dateTime(iso: string) {
  return new Date(iso).toLocaleString(getLocale());
}
