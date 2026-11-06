"use client";
import { Globe, Lock, LockOpen, Plus, Search } from "lucide-react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { Suspense, useMemo, useState } from "react";

import { CreateDomainDialog } from "@/components/create-domain-dialog";

import { StatusBadge } from "@/components/status";
import { Badge, Button, Card, Empty, ErrorBox, Input, PageHeader, Spinner, Table, Td, Th } from "@/components/ui";
import { canManageUsers, useAuth } from "@/lib/auth";
import { useDomains, useUsers } from "@/lib/hooks";
import type { Domain } from "@/lib/types";

function runtimeLabel(d: Domain) {
  if (d.kind === "alias") return "—";
  return d.runtime === "static" ? "Static" : `${d.runtime === "php" ? "PHP" : d.runtime === "node" ? "Node.js" : d.runtime === "python" ? "Python" : "Go"} ${d.runtime_version}`;
}

function DomainsList() {
  const params = useSearchParams();
  const { data, isLoading, error } = useDomains();
  const { user } = useAuth();
  const users = useUsers();
  const [open, setOpen] = useState(params.get("new") === "1");
  const [q, setQ] = useState("");
  const owner = (id: number) => users.data?.find((u) => u.id === id)?.username;

  const rows = useMemo(() => {
    const list = (data ?? []).filter((d) => d.name.includes(q.trim().toLowerCase()));
    // Group aliases and subdomains under their parent site.
    const top = list.filter((d) => d.kind === "primary" || !list.some((p) => p.id === d.parent_id));
    return top.flatMap((t) => [t, ...list.filter((c) => c.parent_id === t.id && c !== t)]);
  }, [data, q]);

  return (
    <>
      <PageHeader
        title="Websites & Domains"
        description="Each website runs in its own isolated container behind the HTTPS edge proxy."
        actions={
          <Button onClick={() => setOpen(true)}>
            <Plus /> Add website
          </Button>
        }
      />
      <Card>
        <div className="border-b p-3">
          <div className="relative max-w-sm">
            <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
            <Input placeholder="Search domains…" className="pl-8" value={q} onChange={(e) => setQ(e.target.value)} />
          </div>
        </div>
        {isLoading ? (
          <div className="flex justify-center py-14">
            <Spinner />
          </div>
        ) : error ? (
          <div className="p-4"><ErrorBox error={error} /></div>
        ) : rows.length === 0 ? (
          <Empty icon={Globe} title={q ? "No matching domains" : "No websites yet"}>
            {!q && <Button className="mt-3" onClick={() => setOpen(true)}><Plus /> Add your first website</Button>}
          </Empty>
        ) : (
          <Table>
            <thead>
              <tr>
                <Th>Domain</Th>
                <Th>Runtime</Th>
                <Th>HTTPS</Th>
                <Th>Status</Th>
                {canManageUsers(user) && <Th>Owner</Th>}
              </tr>
            </thead>
            <tbody>
              {rows.map((d) => {
                const target = d.kind === "alias" ? d.parent_id : d.id;
                return (
                  <tr key={d.id} className="hover:bg-muted/40">
                    <Td>
                      <Link href={`/domains/view/?id=${target}`} className={`font-medium hover:underline ${d.kind !== "primary" ? "pl-5" : ""}`}>
                        {d.kind !== "primary" && <span className="mr-1.5 text-muted-foreground">↳</span>}
                        {d.name}
                      </Link>
                      {d.kind !== "primary" && <Badge className="ml-2 capitalize">{d.kind}</Badge>}
                    </Td>
                    <Td className="text-muted-foreground">{runtimeLabel(d)}</Td>
                    <Td>
                      {d.kind === "alias" ? (
                        <span className="text-muted-foreground">inherited</span>
                      ) : d.ssl_enabled ? (
                        <span className="inline-flex items-center gap-1 text-success"><Lock className="size-3.5" /> On</span>
                      ) : (
                        <span className="inline-flex items-center gap-1 text-muted-foreground"><LockOpen className="size-3.5" /> Off</span>
                      )}
                    </Td>
                    <Td>
                      <StatusBadge status={d.status} title={d.status_message} />
                    </Td>
                    {canManageUsers(user) && <Td className="text-muted-foreground">{owner(d.owner_id) ?? `#${d.owner_id}`}</Td>}
                  </tr>
                );
              })}
            </tbody>
          </Table>
        )}
      </Card>
      <CreateDomainDialog open={open} onOpenChange={setOpen} />
    </>
  );
}

export default function DomainsPage() {
  return (
    <Suspense fallback={<Spinner />}>
      <DomainsList />
    </Suspense>
  );
}
