"use client";
import { Boxes, Cpu, Globe, HardDrive, MemoryStick, Plus, Server, Timer } from "lucide-react";
import Link from "next/link";
import { useEffect, useMemo, useState } from "react";

import { Meter, TimeChart } from "@/components/charts";
import { StatusBadge, containerTone } from "@/components/status";
import { Badge, Button, Card, CardBody, CardHeader, Empty, PageHeader, Spinner, Table, Td, Th } from "@/components/ui";
import { openSocket } from "@/lib/api";
import { isAdmin, useAuth } from "@/lib/auth";
import { useContainers, useDomains, useStatsHistory, useSystemInfo } from "@/lib/hooks";
import type { ContainerStats, HostSample } from "@/lib/types";
import { bytes, duration, pct, rate } from "@/lib/utils";

const MAX_SAMPLES = 300;

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
        title="Server overview"
        description={
          info.data
            ? `${info.data.host.hostname} · ${info.data.host.platform} ${info.data.host.platform_version} · ${info.data.host.cpus} vCPU · Docker ${info.data.docker?.version ?? "unavailable"}`
            : "Live host telemetry"
        }
        actions={
          <Badge tone={connected ? "success" : "neutral"}>
            <span className={`size-1.5 rounded-full ${connected ? "bg-success" : "bg-muted-foreground"}`} />
            {connected ? "Live" : "Connecting…"}
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
            <Tile icon={Cpu} label="CPU" value={pct(last.cpu_percent, 1)} meter={[last.cpu_percent, 100]} sub={`Load ${last.load1.toFixed(2)} · ${last.load5.toFixed(2)} · ${last.load15.toFixed(2)}`} />
            <Tile icon={MemoryStick} label="Memory" value={bytes(last.mem_used)} meter={[last.mem_used, last.mem_total]} sub={`of ${bytes(last.mem_total)} · swap ${bytes(last.swap_used)}`} />
            <Tile icon={HardDrive} label="Disk" value={bytes(last.disk_used)} meter={[last.disk_used, last.disk_total]} sub={`of ${bytes(last.disk_total)} (${pct((last.disk_used / last.disk_total) * 100)})`} />
            <Tile icon={Timer} label="Uptime" value={duration(last.uptime_secs)} sub={`↓ ${rate(last.net_rx_rate)} · ↑ ${rate(last.net_tx_rate)}`} />
          </div>
          <div className="mt-4 grid gap-4 lg:grid-cols-3">
            <Card>
              <CardHeader title="CPU usage" />
              <CardBody>
                <TimeChart title="CPU usage" times={times} max={100} format={(v) => pct(v)} series={[{ key: "cpu", label: "CPU", color: "var(--chart-1)", values: samples.map((s) => s.cpu_percent) }]} />
              </CardBody>
            </Card>
            <Card>
              <CardHeader title="Memory used" />
              <CardBody>
                <TimeChart title="Memory used" times={times} max={last.mem_total} format={(v) => bytes(v, 1)} series={[{ key: "mem", label: "Memory", color: "var(--chart-1)", values: samples.map((s) => s.mem_used) }]} />
              </CardBody>
            </Card>
            <Card>
              <CardHeader title="Network throughput" />
              <CardBody>
                <TimeChart
                  title="Network throughput"
                  times={times}
                  format={rate}
                  series={[
                    { key: "rx", label: "Received", color: "var(--chart-1)", values: samples.map((s) => s.net_rx_rate) },
                    { key: "tx", label: "Sent", color: "var(--chart-2)", values: samples.map((s) => s.net_tx_rate) },
                  ]}
                />
              </CardBody>
            </Card>
          </div>
        </>
      ) : (
        <Card>
          <Empty icon={Server} title="Collecting the first samples…">Telemetry appears within a few seconds.</Empty>
        </Card>
      )}

      <Card className="mt-4">
        <CardHeader title="Containers" description="Every container managed by OpenPlex, sampled every few seconds." />
        {rows.length === 0 ? (
          <Empty icon={Boxes} title="No managed containers yet" />
        ) : (
          <Table>
            <thead>
              <tr>
                <Th>Name</Th>
                <Th>Role</Th>
                <Th>State</Th>
                <Th className="text-right">CPU</Th>
                <Th className="text-right">Memory</Th>
                <Th className="text-right">Network ↓ / ↑</Th>
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
                  <Td className="capitalize">{c.kind}</Td>
                  <Td>
                    <Badge tone={containerTone(c.state)} className="capitalize">{c.state}</Badge>
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
        title={`Welcome, ${user?.username}`}
        description="An overview of your hosting."
        actions={
          <Button asChild>
            <Link href="/domains/?new=1">
              <Plus /> Add website
            </Link>
          </Button>
        }
      />
      <div className="grid gap-4 sm:grid-cols-3">
        <Tile icon={Globe} label="Websites" value={String(sites.length)} sub={user?.max_domains ? `of ${user.max_domains} allowed` : "No quota limit"} />
        <Tile icon={Server} label="Running" value={String(counts.running)} />
        <Tile icon={Boxes} label="Need attention" value={String(counts.attention)} />
      </div>
      <Card className="mt-4">
        <CardHeader title="Your websites" action={<Link className="text-sm text-primary hover:underline" href="/domains/">View all</Link>} />
        {domains.isLoading ? (
          <div className="flex justify-center py-10"><Spinner /></div>
        ) : sites.length === 0 ? (
          <Empty icon={Globe} title="No websites yet">Add your first website to get started.</Empty>
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
