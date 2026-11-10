"use client";
import { Pause, Play, Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { openSocket } from "@/lib/api";
import { fmtNumber, getLocale, t, tServer, useT } from "@/lib/i18n";

import { Badge, Button } from "./ui";

const MAX_LINES = 2000;

interface AccessLine {
  ts?: number;
  status?: number;
  duration?: number;
  size?: number;
  request?: { remote_ip?: string; client_ip?: string; method?: string; host?: string; uri?: string; headers?: Record<string, string[]> };
}

/** Pretty-prints Caddy's JSON access log lines; passes anything else through. */
function format(line: string, kind: "access" | "error") {
  if (kind !== "access") return { text: line, tone: "" };
  try {
    const l = JSON.parse(line) as AccessLine;
    const r = l.request ?? {};
    const time = l.ts ? new Date(l.ts * 1000).toLocaleTimeString(getLocale()) : "";
    const ms = l.duration ? `${fmtNumber(l.duration * 1000, 1)} ms` : "";
    const tone = (l.status ?? 0) >= 500 ? "text-destructive" : (l.status ?? 0) >= 400 ? "text-warning" : "";
    return { text: `${time}  ${r.client_ip ?? r.remote_ip ?? ""}  ${r.method ?? ""} ${r.uri ?? ""}  ${l.status ?? ""}  ${l.size ?? 0}B  ${ms}`, tone };
  } catch {
    return { text: line, tone: "" };
  }
}

export function LogViewer({ domainId, kind }: { domainId: number; kind: "access" | "error" }) {
  useT();
  const [lines, setLines] = useState<string[]>([]);
  const [state, setState] = useState<"connecting" | "live" | "closed">("connecting");
  const [paused, setPaused] = useState(false);
  const [note, setNote] = useState("");
  const boxRef = useRef<HTMLDivElement>(null);
  const pausedRef = useRef(paused);
  pausedRef.current = paused;

  useEffect(() => {
    let ws: WebSocket | null = null;
    let closed = false;
    let buf = "";
    setLines([]);
    setNote("");
    setState("connecting");
    (async () => {
      try {
        ws = await openSocket(`/domains/${domainId}/logs/ws?type=${kind}`);
      } catch (e) {
        setState("closed");
        setNote(e instanceof Error ? e.message : t("connection failed"));
        return;
      }
      if (closed) return ws.close();
      ws.onopen = () => setState("live");
      ws.onmessage = (ev) => {
        buf += String(ev.data);
        const parts = buf.split("\n");
        buf = parts.pop() ?? "";
        if (parts.length && !pausedRef.current) setLines((l) => [...l, ...parts.filter(Boolean)].slice(-MAX_LINES));
      };
      ws.onclose = (ev) => {
        setState("closed");
        if (ev.reason) setNote(tServer(ev.reason));
      };
    })();
    return () => {
      closed = true;
      ws?.close();
    };
  }, [domainId, kind]);

  useEffect(() => {
    const el = boxRef.current;
    if (el && !paused) el.scrollTop = el.scrollHeight;
  }, [lines, paused]);

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <Badge tone={state === "live" ? "success" : state === "connecting" ? "info" : "neutral"}>
          {state === "live" ? t("Streaming") : state === "connecting" ? t("Connecting…") : t("Disconnected")}
        </Badge>
        {note && <span className="text-xs text-muted-foreground">{note}</span>}
        <div className="ml-auto flex gap-1">
          <Button size="sm" variant="outline" onClick={() => setPaused((p) => !p)}>
            {paused ? <Play /> : <Pause />} {paused ? t("Resume") : t("Pause")}
          </Button>
          <Button size="sm" variant="outline" onClick={() => setLines([])}>
            <Trash2 /> {t("Clear")}
          </Button>
        </div>
      </div>
      <div ref={boxRef} className="h-[28rem] overflow-auto rounded-md border bg-zinc-950 p-3 font-mono text-xs leading-relaxed text-zinc-200">
        {lines.length === 0 ? (
          <span className="text-zinc-500">
            {kind === "access" ? t("Waiting for requests… (visit the site to see traffic here)") : t("No output yet.")}
          </span>
        ) : (
          lines.map((l, i) => {
            const f = format(l, kind);
            return (
              <div key={i} className={`whitespace-pre-wrap break-all ${f.tone === "text-destructive" ? "text-red-400" : f.tone === "text-warning" ? "text-amber-300" : ""}`}>
                {f.text}
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}
