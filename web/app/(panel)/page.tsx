"use client";
import { Boxes, Cpu, Globe, HardDrive, MemoryStick, Plus, Server, Timer } from "lucide-react";
import Link from "next/link";
import { useEffect, useMemo, useState } from "react";

import { Meter, TimeChart } from "@/components/charts";
import { StatusBadge, containerTone, stateLabels } from "@/components/status";
import { Badge, Button, Card, CardBody, CardHeader, Empty, PageHeader, Spinner, Table, Td, Th } from "@/components/ui";
import { openSocket } from "@/lib/api";
import { isAdmin, useAuth } from "@/lib/auth";
import { useContainers, useDomains, useStatsHistory, useSystemInfo } from "@/lib/hooks";
import { fmtNumber, tk, useT } from "@/lib/i18n";
import type { ContainerStats, HostSample } from "@/lib/types";
import { bytes, duration, pct, rate } from "@/lib/utils";

const MAX_SAMPLES = 300;

const kindLabels: Record<string, string> = {
  site: tk("Website"),
  edge: tk("Edge proxy"),
  db: tk("Database server"),
  redis: "Redis",
  job: tk("Job"),
  shell: tk("Terminal"),
  tool: tk("Tool"),
};

/** Host samples: REST history, then live updates over WebSocket. */
function useLiveHost(enabled: boolean) {
  const history = useStatsHistory(enabled);
  const [samples, setSamples] = useState<HostSample[]>([]);
  const [live, setLive] = useState<ContainerStats[] | null>(null);
  const [connected, setConnected] = useState(false);

  useEffect(() => {
    if (history.data) setSamples(history.data.history.slice(-MAX_SAMPLES));
  }, [history.data]);

  useEffect(() => {
    if (!enabled) return;
    let ws: WebSocket | null = null;
    let closed = false;
    let retry: ReturnType<typeof setTimeout>;
    const connect = async () => {
      try {
        ws = await openSocket("/system/stats/ws");
      } catch {
        if (!closed) retry = setTimeout(connect, 5000);
        return;
      }
      ws.onopen = () => setConnected(true);
      ws.onmessage = (ev) => {
        const msg = JSON.parse(ev.data);
        if (msg.type === "host") setSamples((s) => [...s.slice(-(MAX_SAMPLES - 1)), msg.sample]);
        if (msg.type === "containers") setLive(msg.containers);
      };
      ws.onclose = () => {
        setConnected(false);
        if (!closed) retry = setTimeout(connect, 3000);
      };
    };
    connect();
    return () => {
      closed = true;
      clearTimeout(retry);
      ws?.close();
    };
  }, [enabled]);

  return { samples, live, connected, loading: history.isLoading };
}

function Tile({
  icon: Icon,
  label,
  value,
  sub,
  meter,
}: {
  icon: React.ComponentType<{ className?: string }>;
  label: string;
  value: string;
  sub?: string;
  meter?: [number, number];
}) {
  return (
    <Card className="p-4">
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Icon className="size-4" />
        {label}
      </div>
      <div className="tabular mt-2 text-2xl font-semibold">{value}</div>
      {meter && <Meter value={meter[0]} max={meter[1]} className="mt-3" />}
      {sub && <div className="tabular mt-2 text-xs text-muted-foreground">{sub}</div>}
    </Card>
  );
}

function AdminDashboard() {
  const { t } = useT();
  const info = useSystemInfo();
  const { samples, live, connected, loading } = useLiveHost(true);
  const containers = useContainers();
  const domains = useDomains();
  const rows = live ?? containers.data ?? [];
  const last = samples[samples.length - 1];
  const times = useMemo(() => samples.map((s) => Date.parse(s.time)), [samples]);
  const nameOf = (id?: number) => domains.data?.find((d) => d.id === id)?.name;

  return (
    <>
      <PageHeader
        title={t("Server overview")}
        description={
          info.data
            ? `${info.data.host.hostname} · ${info.data.host.platform} ${info.data.host.platform_version} · ${info.data.host.cpus} vCPU · Docker ${info.data.docker?.version ?? t("unavailable")}`
            : t("Live host telemetry")
        }
        actions={
          <Badge tone={connected ? "success" : "neutral"}>
            <span className={`size-1.5 rounded-full ${connected ? "bg-success" : "bg-muted-foreground"}`} />
            {connected ? t("Live") : t("Connecting…")}
          </Badge>
        }
      />
      {loading && !last ? (
        <div className="flex justify-center py-20">
          <Spinner />
        </div>
      ) : last ? (
        <>
          <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
            <Tile
              icon={Cpu}
              label="CPU"
              value={pct(last.cpu_percent, 1)}
              meter={[last.cpu_percent, 100]}
              sub={t("Load {load1} · {load5} · {load15}", {
                load1: fmtNumber(last.load1, 2),
                load5: fmtNumber(last.load5, 2),
                load15: fmtNumber(last.load15, 2),
              })}
            />
            <Tile
              icon={MemoryStick}
              label={t("Memory")}
              value={bytes(last.mem_used)}
              meter={[last.mem_used, last.mem_total]}
              sub={t("of {total} · swap {swap}", {
                total: bytes(last.mem_total),
                swap: bytes(last.swap_used),
              })}
            />
            <Tile
              icon={HardDrive}
              label={t("Disk")}
              value={bytes(last.disk_used)}
              meter={[last.disk_used, last.disk_total]}
              sub={t("of {total} ({percent})", {
                total: bytes(last.disk_total),
                percent: pct((last.disk_used / last.disk_total) * 100),
              })}
            />
            <Tile icon={Timer} label={t("Uptime")} value={duration(last.uptime_secs)} sub={`↓ ${rate(last.net_rx_rate)} · ↑ ${rate(last.net_tx_rate)}`} />
          </div>
          <div className="mt-4 grid gap-4 lg:grid-cols-3">
            <Card>
              <CardHeader title={t("CPU usage")} />
              <CardBody>
                <TimeChart
                  title={t("CPU usage")}
                  times={times}
                  max={100}
                  format={(v) => pct(v)}
                  series={[
                    {
                      key: "cpu",
                      label: "CPU",
                      color: "var(--chart-1)",
                      values: samples.map((s) => s.cpu_percent),
                    },
                  ]}
                />
              </CardBody>
            </Card>
            <Card>
              <CardHeader title={t("Memory used")} />
              <CardBody>
                <TimeChart
                  title={t("Memory used")}
                  times={times}
                  max={last.mem_total}
                  format={(v) => bytes(v, 1)}
                  series={[
                    {
                      key: "mem",
                      label: t("Memory"),
                      color: "var(--chart-1)",
                      values: samples.map((s) => s.mem_used),
                    },
                  ]}
                />
              </CardBody>
            </Card>
            <Card>
              <CardHeader title={t("Network throughput")} />
              <CardBody>
                <TimeChart
                  title={t("Network throughput")}
                  times={times}
                  format={rate}
                  series={[
                    {
                      key: "rx",
                      label: t("Received"),
                      color: "var(--chart-1)",
                      values: samples.map((s) => s.net_rx_rate),
                    },
                    {
                      key: "tx",
                      label: t("Sent"),
                      color: "var(--chart-2)",
                      values: samples.map((s) => s.net_tx_rate),
                    },
                  ]}
                />
              </CardBody>
            </Card>
          </div>
        </>
      ) : (
        <Card>
          <Empty icon={Server} title={t("Collecting the first samples…")}>
            {t("Telemetry appears within a few seconds.")}
          </Empty>
        </Card>
      )}

      <Card className="mt-4">
        <CardHeader title={t("Containers")} description={t("Every container managed by OpenPlex, sampled every few seconds.")} />
        {rows.length === 0 ? (
          <Empty icon={Boxes} title={t("No managed containers yet")} />
        ) : (
          <Table>
            <thead>
              <tr>
                <Th>{t("Name")}</Th>
                <Th>{t("Role")}</Th>
                <Th>{t("State")}</Th>
                <Th className="text-right">CPU</Th>
                <Th className="text-right">{t("Memory")}</Th>
                <Th className="text-right">{t("Network ↓ / ↑")}</Th>
                <Th className="text-right">PIDs</Th>
              </tr>
            </thead>
            <tbody className="tabular">
              {rows.map((c) => (
                <tr key={c.id}>
                  <Td>
                    <div className="font-medium">{c.domain_id ? (nameOf(c.domain_id) ?? c.name) : c.name}</div>
                    <div className="font-mono text-xs text-muted-foreground">{c.name}</div>
                  </Td>
                  <Td className={kindLabels[c.kind] ? undefined : "capitalize"}>{kindLabels[c.kind] ? t(kindLabels[c.kind]) : c.kind}</Td>
                  <Td>
                    <Badge tone={containerTone(c.state)} className={stateLabels[c.state] ? undefined : "capitalize"}>
                      {stateLabels[c.state] ? t(stateLabels[c.state]) : c.state}
                    </Badge>
                  </Td>
                  <Td className="text-right">{pct(c.cpu_percent, 1)}</Td>
                  <Td className="text-right">
                    {bytes(c.mem_usage)}
                    <span className="text-muted-foreground"> / {bytes(c.mem_limit)}</span>
                  </Td>
                  <Td className="text-right">
                    {bytes(c.net_rx)} / {bytes(c.net_tx)}
                  </Td>
                  <Td className="text-right">{c.pids}</Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
    </>
  );
}

function TenantDashboard() {
  const { t } = useT();
  const { user } = useAuth();
  const domains = useDomains();
  const list = domains.data ?? [];
  const sites = list.filter((d) => d.kind !== "alias");
  const counts = {
    running: sites.filter((d) => d.status === "running").length,
    attention: sites.filter((d) => d.status === "error").length,
  };
  return (
    <>
      <PageHeader
        title={t("Welcome, {name}", { name: user?.username ?? "" })}
        description={t("An overview of your hosting.")}
        actions={
          <Button asChild>
            <Link href="/domains/?new=1">
              <Plus /> {t("Add website")}
            </Link>
          </Button>
        }
      />
      <div className="grid gap-4 sm:grid-cols-3">
        <Tile
          icon={Globe}
          label={t("Websites")}
          value={String(sites.length)}
          sub={user?.max_domains ? t("of {max} allowed", { max: user.max_domains }) : t("No quota limit")}
        />
        <Tile icon={Server} label={t("Running")} value={String(counts.running)} />
        <Tile icon={Boxes} label={t("Need attention")} value={String(counts.attention)} />
      </div>
      <Card className="mt-4">
        <CardHeader
          title={t("Your websites")}
          action={
            <Link className="text-sm text-primary hover:underline" href="/domains/">
              {t("View all")}
            </Link>
          }
        />
        {domains.isLoading ? (
          <div className="flex justify-center py-10">
            <Spinner />
          </div>
        ) : sites.length === 0 ? (
          <Empty icon={Globe} title={t("No websites yet")}>
            {t("Add your first website to get started.")}
          </Empty>
        ) : (
          <ul className="divide-y">
            {sites.slice(0, 8).map((d) => (
              <li key={d.id}>
                <Link href={`/domains/view/?id=${d.id}`} className="flex items-center justify-between gap-4 px-5 py-3 hover:bg-muted/50">
                  <span className="font-medium">{d.name}</span>
                  <StatusBadge status={d.status} />
                </Link>
              </li>
            ))}
          </ul>
        )}
      </Card>
    </>
  );
}

export default function DashboardPage() {
  const { user } = useAuth();
  return isAdmin(user) ? <AdminDashboard /> : <TenantDashboard />;
}
