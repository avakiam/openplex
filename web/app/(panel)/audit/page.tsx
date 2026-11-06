"use client";
import { ScrollText, Search } from "lucide-react";
import { useMemo, useState } from "react";

import { Badge, Card, Empty, ErrorBox, Input, PageHeader, Spinner, Table, Td, Th } from "@/components/ui";
import { useAudit, useUsers } from "@/lib/hooks";
import { dateTime } from "@/lib/utils";

const tone = (a: string) =>
  a.includes("failed") || a.includes("reuse") ? "danger" : a.endsWith("deleted") || a.includes("2fa_disabled") ? "warning" : a.startsWith("auth.") ? "info" : "neutral";

export default function AuditPage() {
  const { data, isLoading, error } = useAudit();
  const users = useUsers();
  const [q, setQ] = useState("");
  const name = (id: number | null) => (id ? (users.data?.find((u) => u.id === id)?.username ?? `#${id}`) : "—");
  const rows = useMemo(() => {
    const s = q.trim().toLowerCase();
    return (data ?? []).filter((e) => !s || [e.action, e.target, e.detail, e.ip, name(e.user_id)].some((v) => v.toLowerCase().includes(s)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data, q, users.data]);

  return (
    <>
      <PageHeader title="Audit log" description="Security-relevant actions across the panel (latest 200)." />
      <Card>
        <div className="border-b p-3">
          <div className="relative max-w-sm">
            <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
            <Input placeholder="Filter by action, user, IP…" className="pl-8" value={q} onChange={(e) => setQ(e.target.value)} />
          </div>
        </div>
        {isLoading ? (
          <div className="flex justify-center py-14"><Spinner /></div>
        ) : error ? (
          <div className="p-4"><ErrorBox error={error} /></div>
        ) : rows.length === 0 ? (
          <Empty icon={ScrollText} title="No entries" />
        ) : (
          <Table>
            <thead><tr><Th>Time</Th><Th>User</Th><Th>Action</Th><Th>Target</Th><Th>IP</Th></tr></thead>
            <tbody>
              {rows.map((e) => (
                <tr key={e.id}>
                  <Td className="tabular whitespace-nowrap text-muted-foreground">{dateTime(e.created_at)}</Td>
                  <Td>{name(e.user_id)}</Td>
                  <Td><Badge tone={tone(e.action)} className="font-mono">{e.action}</Badge></Td>
                  <Td className="max-w-md break-all">{e.target}{e.detail && <span className="text-muted-foreground"> · {e.detail}</span>}</Td>
                  <Td className="font-mono text-xs text-muted-foreground">{e.ip}</Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
    </>
  );
}
