// Thin fetch client for /api/v1. The access token lives only in memory; the
// refresh token is an httpOnly cookie scoped to /api/v1/auth, so a page reload
// silently re-establishes the session via POST /auth/refresh.
import { t, tServer } from "./i18n";
import type { User } from "./types";

export const API = "/api/v1";

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    /** Lets the two-factor step skip a second captcha (login only). */
    public captchaPass?: string,
  ) {
    super(message);
  }
}

interface Session {
  accessToken: string;
  expiresAt: number;
  user: User;
}

let session: Session | null = null;
let refreshing: Promise<Session | null> | null = null;
const listeners = new Set<(u: User | null) => void>();

export function onSessionChange(fn: (u: User | null) => void) {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

function setSession(s: Session | null) {
  session = s;
  listeners.forEach((l) => l(s?.user ?? null));
}

export function currentUser() {
  return session?.user ?? null;
}

interface TokenResponse {
  access_token: string;
  expires_at: string;
  user: User;
}

function adopt(t: TokenResponse): Session {
  const s = { accessToken: t.access_token, expiresAt: Date.parse(t.expires_at), user: t.user };
  setSession(s);
  return s;
}

async function parseError(res: Response): Promise<ApiError> {
  let code = "http_" + res.status;
  let message = res.statusText || t("Request failed");
  let captchaPass: string | undefined;
  let template: string | undefined;
  let params: Record<string, string> | undefined;
  try {
    const body = await res.json();
    code = body.error ?? code;
    message = body.message ?? message;
    captchaPass = body.captcha_pass || undefined;
    template = body.template || undefined;
    params = body.params || undefined;
  } catch {
    /* non-JSON error body */
  }
  return new ApiError(res.status, code, tServer(message, template, params), captchaPass);
}

/** Exchanges the refresh cookie for a new access token (deduplicated). */
export function refresh(): Promise<Session | null> {
  if (!refreshing) {
    refreshing = fetch(`${API}/auth/refresh`, { method: "POST", credentials: "same-origin" })
      .then(async (res) => {
        if (!res.ok) {
          setSession(null);
          return null;
        }
        return adopt(await res.json());
      })
      .catch(() => null)
      .finally(() => {
        refreshing = null;
      });
  }
  return refreshing;
}

async function token(): Promise<string | null> {
  if (session && session.expiresAt - Date.now() > 30_000) return session.accessToken;
  const s = await refresh();
  return s?.accessToken ?? null;
}

export interface LoginCaptcha {
  token?: string; // solved widget
  pass?: string; // from a totp_required answer
}

export async function login(username: string, password: string, totpCode?: string, captcha?: LoginCaptcha) {
  const res = await fetch(`${API}/auth/login`, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      username,
      password,
      totp_code: totpCode || undefined,
      captcha_token: captcha?.token || undefined,
      captcha_pass: captcha?.pass || undefined,
    }),
  });
  if (!res.ok) throw await parseError(res);
  return adopt(await res.json()).user;
}

/** Signs in with a one-time link from `openplexd login-link`. */
export async function loginWithLink(token: string) {
  const res = await fetch(`${API}/auth/link`, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ token }),
  });
  if (!res.ok) throw await parseError(res);
  const body = await res.json();
  // "handover" (openplexd login-link) or "sso" (a billing system's client area)
  return { user: adopt(body).user, purpose: body.purpose as "handover" | "sso" };
}

export async function logout() {
  await fetch(`${API}/auth/logout`, { method: "POST", credentials: "same-origin" }).catch(() => {});
  setSession(null);
}

export async function request<T>(method: string, path: string, body?: unknown, init?: RequestInit): Promise<T> {
  const send = async (tok: string | null) => {
    const headers = new Headers(init?.headers);
    if (tok) headers.set("Authorization", `Bearer ${tok}`);
    const isForm = body instanceof FormData;
    if (body !== undefined && !isForm) headers.set("Content-Type", "application/json");
    return fetch(API + path, {
      ...init,
      method,
      headers,
      credentials: "same-origin",
      body: body === undefined ? undefined : isForm ? body : JSON.stringify(body),
    });
  };
  let res = await send(await token());
  if (res.status === 401) {
    const s = await refresh();
    if (!s) throw new ApiError(401, "unauthorized", t("Your session has expired"));
    res = await send(s.accessToken);
  }
  if (!res.ok) throw await parseError(res);
  if (res.status === 204 || res.status === 202) return undefined as T;
  const text = await res.text();
  return (text ? JSON.parse(text) : undefined) as T;
}

export const api = {
  get: <T>(p: string) => request<T>("GET", p),
  post: <T>(p: string, b?: unknown) => request<T>("POST", p, b ?? {}),
  put: <T>(p: string, b?: unknown) => request<T>("PUT", p, b),
  patch: <T>(p: string, b?: unknown) => request<T>("PATCH", p, b),
  del: <T>(p: string) => request<T>("DELETE", p),
};

/** Downloads a file through an authenticated fetch (no token in the URL). */
export async function download(path: string, filename: string) {
  const res = await fetch(API + path, { headers: { Authorization: `Bearer ${await token()}` } });
  if (!res.ok) throw await parseError(res);
  const url = URL.createObjectURL(await res.blob());
  const a = Object.assign(document.createElement("a"), { href: url, download: filename });
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 10_000);
}

/** Opens an authenticated WebSocket using a single-use ticket. */
export async function openSocket(path: string): Promise<WebSocket> {
  const { ticket } = await api.post<{ ticket: string }>("/auth/ws-ticket");
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const sep = path.includes("?") ? "&" : "?";
  // In `next dev` the socket must bypass the dev server and hit openplexd directly.
  const host = process.env.NEXT_PUBLIC_WS_HOST || location.host;
  return new WebSocket(`${proto}//${host}${API}${path}${sep}ticket=${encodeURIComponent(ticket)}`);
}
