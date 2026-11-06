"use client";
import "@xterm/xterm/css/xterm.css";

import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { Terminal } from "@xterm/xterm";
import { Maximize2, Minimize2, Plug, RotateCw } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";

import { openSocket } from "@/lib/api";
import { cn } from "@/lib/utils";

import { Badge, Button, Switch } from "./ui";

type State = "connecting" | "open" | "closed";

const theme = {
  background: "#0b0b0e",
  foreground: "#e6e6ea",
  cursor: "#4b82f0",
  selectionBackground: "#2a3d66",
  black: "#1b1b20", red: "#ff6b6b", green: "#3fb27a", yellow: "#e0a03a", blue: "#4b82f0",
  magenta: "#c678dd", cyan: "#56b6c2", white: "#d7d7dc",
  brightBlack: "#5c5c66", brightRed: "#ff8787", brightGreen: "#69db9c", brightYellow: "#ffd166",
  brightBlue: "#74a0ff", brightMagenta: "#e599f7", brightCyan: "#7fdbe8", brightWhite: "#ffffff",
};

export function WebTerminal({ domainId, canRoot }: { domainId: number; canRoot: boolean }) {
  const host = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const [state, setState] = useState<State>("connecting");
  const [reason, setReason] = useState("");
  const [full, setFull] = useState(false);
  const [root, setRoot] = useState(false);
  const [session, setSession] = useState(0); // bump to reconnect

  // Create the terminal once.
  useEffect(() => {
    const term = new Terminal({
      cursorBlink: true,
      fontFamily: 'ui-monospace, SFMono-Regular, "Cascadia Code", Menlo, Consolas, monospace',
      fontSize: 13,
      lineHeight: 1.2,
      scrollback: 5000,
      theme,
      allowProposedApi: false,
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.loadAddon(new WebLinksAddon());
    term.open(host.current!);
    fit.fit();
    termRef.current = term;
    fitRef.current = fit;
    const ro = new ResizeObserver(() => {
      try {
        fit.fit();
      } catch {
        /* element hidden */
      }
    });
    ro.observe(host.current!);
    const onInput = term.onData((d) => {
      const ws = wsRef.current;
      if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ t: "i", d }));
    });
    const onResize = term.onResize(({ cols, rows }) => {
      const ws = wsRef.current;
      if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ t: "r", c: cols, r: rows }));
    });
    return () => {
      onInput.dispose();
      onResize.dispose();
      ro.disconnect();
      term.dispose();
    };
  }, []);

  // (Re)connect whenever the session key or root mode changes.
  useEffect(() => {
    const term = termRef.current!;
    let closed = false;
    setState("connecting");
    setReason("");
    term.reset();
    (async () => {
      let ws: WebSocket;
      try {
        ws = await openSocket(`/domains/${domainId}/terminal/ws?cols=${term.cols}&rows=${term.rows}${root ? "&root=1" : ""}`);
      } catch (e) {
        setState("closed");
        setReason(e instanceof Error ? e.message : "connection failed");
        return;
      }
      if (closed) return ws.close();
      ws.binaryType = "arraybuffer";
      wsRef.current = ws;
      ws.onopen = () => {
        setState("open");
        term.focus();
      };
      ws.onmessage = (ev) => {
        term.write(typeof ev.data === "string" ? ev.data : new Uint8Array(ev.data as ArrayBuffer));
      };
      ws.onclose = (ev) => {
        setState("closed");
        setReason(ev.reason || "disconnected");
        term.write(`\r\n\x1b[2m[${ev.reason || "connection closed"}]\x1b[0m\r\n`);
      };
    })();
    return () => {
      closed = true;
      wsRef.current?.close();
      wsRef.current = null;
    };
  }, [domainId, root, session]);

  const refit = useCallback(() => setTimeout(() => fitRef.current?.fit(), 50), []);
  useEffect(() => {
    refit();
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && e.shiftKey && setFull(false);
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [full, refit]);

  return (
    <div className={cn("flex flex-col gap-2", full && "fixed inset-0 z-50 bg-background p-3")}>
      <div className="flex flex-wrap items-center gap-2">
        <Badge tone={state === "open" ? "success" : state === "connecting" ? "info" : "neutral"}>
          <Plug className="size-3" /> {state === "open" ? "Connected" : state === "connecting" ? "Connecting…" : "Disconnected"}
        </Badge>
        {state === "closed" && reason && <span className="text-xs text-muted-foreground">{reason}</span>}
        <div className="ml-auto flex items-center gap-3">
          {canRoot && (
            <label className="flex items-center gap-2 text-xs text-muted-foreground" title="Administrators only">
              <Switch checked={root} onCheckedChange={setRoot} /> root
            </label>
          )}
          <Button size="sm" variant="outline" onClick={() => setSession((n) => n + 1)}>
            <RotateCw /> {state === "closed" ? "Reconnect" : "Restart"}
          </Button>
          <Button size="sm" variant="outline" onClick={() => setFull((f) => !f)} aria-label={full ? "Exit full screen" : "Full screen"}>
            {full ? <Minimize2 /> : <Maximize2 />}
          </Button>
        </div>
      </div>
      <div
        ref={host}
        className={cn("overflow-hidden rounded-md border bg-[#0b0b0e] p-2", full ? "min-h-0 flex-1" : "h-[60vh] min-h-80")}
        onClick={() => termRef.current?.focus()}
      />
      <p className="text-xs text-muted-foreground">
        Runs as the website&apos;s own user in its environment. Idle sessions close after 30 minutes. {full && "Shift+Esc exits full screen."}
      </p>
    </div>
  );
}
