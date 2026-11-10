"use client";
import { CircleCheck, Info, Network, Pencil, Plus, Trash2, TriangleAlert } from "lucide-react";
import Link from "next/link";
import { useState } from "react";

import {
  Badge,
  Button,
  Card,
  CardBody,
  CardHeader,
  Confirm,
  Dialog,
  DialogContent,
  Empty,
  ErrorBox,
  Field,
  Input,
  Select,
  Spinner,
  Table,
  Td,
  Th,
} from "@/components/ui";
import { api } from "@/lib/api";
import { isAdmin, useAuth } from "@/lib/auth";
import { keys, useAction, useDomains, useZone } from "@/lib/hooks";
import { tk, useT } from "@/lib/i18n";
import type { DNSDelegation, DNSRecord, DNSType, Domain } from "@/lib/types";
import { duration } from "@/lib/utils";

const placeholders: Record<DNSType, string> = {
  A: "203.0.113.10",
  AAAA: "2001:db8::10",
  CNAME: "example.net",
  MX: "mail.example.com",
  TXT: "v=spf1 mx -all",
  SRV: "5 5060 sip.example.com",
  NS: "ns1.other-provider.net",
  CAA: "0 issue letsencrypt.org",
};

const hints: Partial<Record<DNSType, string>> = {
  CNAME: tk("Another host name. Use @ for this domain."),
  SRV: tk("Weight, port and target, separated by spaces."),
  NS: tk("Delegates this subdomain to other nameservers."),
  CAA: tk("Flags, tag (issue, issuewild or iodef) and value."),
};

const ttls = [300, 3600, 14400, 86400];

type Draft = { id?: number; name: string; type: DNSType; content: string; priority: number; ttl: number };

export function DelegationBadge({ d }: { d: DNSDelegation }) {
  const { t } = useT();
  switch (d.status) {
    case "ok":
      return (
        <Badge tone="success">
          <CircleCheck className="size-3" /> {t("Pointed at this server")}
        </Badge>
      );
    case "partial":
      return <Badge tone="warning">{t("Partly pointed at this server")}</Badge>;
    case "other":
      return <Badge title={d.servers.join(", ")}>{t("Uses other nameservers")}</Badge>;
    default:
      return <Badge>{t("Not delegated")}</Badge>;
  }
}

function RecordDialog({ domainId, zone, draft, types, onClose }: { domainId: number; zone: string; draft: Draft; types: DNSType[]; onClose: () => void }) {
  const { t } = useT();
  const [f, setF] = useState(draft);
  const save = useAction(
    () => {
      const body = { name: f.name, type: f.type, content: f.content, priority: Number(f.priority), ttl: Number(f.ttl) };
      return f.id ? api.patch(`/domains/${domainId}/dns/records/${f.id}`, body) : api.post(`/domains/${domainId}/dns/records`, body);
    },
    { success: f.id ? t("Record saved") : t("Record added"), invalidate: [keys.zone(domainId)], onSuccess: onClose },
  );
  const prio = f.type === "MX" || f.type === "SRV";
  return (
    <form
      className="grid gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate(undefined);
      }}
    >
      <div className="grid gap-3 sm:grid-cols-[8rem_1fr]">
        <Field label={t("Type")}>
          <Select value={f.type} onChange={(e) => setF({ ...f, type: e.target.value as DNSType })}>
            {types.map((ty) => (
              <option key={ty} value={ty}>
                {ty}
              </option>
            ))}
          </Select>
        </Field>
        <Field label={t("Name")} hint={t("@ is {zone} itself.", { zone })}>
          <div className="flex items-center gap-1">
            <Input className="font-mono" placeholder="@" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
            <span className="shrink-0 font-mono text-xs text-muted-foreground">.{zone}</span>
          </div>
        </Field>
      </div>
      <div className={prio ? "grid gap-3 sm:grid-cols-[8rem_1fr]" : "grid"}>
        {prio && (
          <Field label={t("Priority")} hint={t("Lower is tried first.")}>
            <Input type="number" min={0} max={65535} value={f.priority} onChange={(e) => setF({ ...f, priority: Number(e.target.value) })} />
          </Field>
        )}
        <Field label={t("Value")} hint={hints[f.type] ? t(hints[f.type]!) : undefined}>
          <Input
            required
            className="font-mono"
            placeholder={placeholders[f.type]}
            value={f.content}
            onChange={(e) => setF({ ...f, content: e.target.value })}
          />
        </Field>
      </div>
      <Field label={t("TTL")} hint={t("How long other servers may cache this record.")}>
        <Select value={f.ttl} onChange={(e) => setF({ ...f, ttl: Number(e.target.value) })}>
          {(ttls.includes(f.ttl) ? ttls : [...ttls, f.ttl].sort((a, b) => a - b)).map((s) => (
            <option key={s} value={s}>
              {duration(s)}
            </option>
          ))}
        </Select>
      </Field>
      <ErrorBox error={save.error} />
      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" onClick={onClose}>
          {t("Cancel")}
        </Button>
        <Button type="submit" loading={save.isPending}>
          {f.id ? t("Save") : t("Add record")}
        </Button>
      </div>
    </form>
  );
}

export function DNSRecords({ domain }: { domain: Domain }) {
  const { t } = useT();
  const { user } = useAuth();
  const domains = useDomains();
  const { data, isLoading, error } = useZone(domain.id, domain.kind !== "subdomain");
  const [editing, setEditing] = useState<Draft | null>(null);
  const [deleting, setDeleting] = useState<DNSRecord | null>(null);
  const del = useAction((id: number) => api.del(`/domains/${domain.id}/dns/records/${id}`), {
    success: t("Record deleted"),
    invalidate: [keys.zone(domain.id)],
    onSuccess: () => setDeleting(null),
  });

  if (domain.kind === "subdomain") {
    const parent = domains.data?.find((x) => x.id === domain.parent_id);
    return (
      <Card>
        <Empty icon={Network} title={t("This subdomain's records are in the DNS zone of its main domain")}>
          {parent && (
            <Button asChild variant="outline" className="mt-3">
              <Link href={`/domains/view/?id=${parent.id}`}>{t("Open {domain}", { domain: parent.name })}</Link>
            </Button>
          )}
        </Empty>
      </Card>
    );
  }
  if (isLoading)
    return (
      <div className="flex justify-center py-10">
        <Spinner />
      </div>
    );
  if (error || !data) return <ErrorBox error={error} />;
  const zone = data.zone.name;
  const fqdn = (name: string) => (name === "@" ? zone : `${name}.${zone}`);
  const ns = data.nameservers.filter(Boolean);

  return (
    <div className="grid gap-4">
      <Card>
        <CardHeader title={t("DNS zone {zone}", { zone })} action={<DelegationBadge d={data.delegation} />} />
        <CardBody className="grid gap-3 text-sm">
          {!data.server_enabled ? (
            <div className="flex gap-2 rounded-md border border-warning/40 bg-warning/8 p-3">
              <TriangleAlert className="mt-0.5 size-4 shrink-0 text-warning" />
              <span>
                {t("The DNS server is off, so these records are not published yet.")}{" "}
                {isAdmin(user) ? (
                  <Link className="underline" href="/dns/">
                    {t("Turn it on")}
                  </Link>
                ) : (
                  t("Ask your provider to turn it on.")
                )}
              </span>
            </div>
          ) : data.delegation.status !== "ok" && ns.length > 0 ? (
            <div className="flex gap-2 rounded-md bg-muted/60 p-3 text-muted-foreground">
              <Info className="mt-0.5 size-4 shrink-0" />
              <span>
                {t("For these records to be used, set the nameservers of {zone} at your domain registrar to:", { zone })}{" "}
                <span className="font-mono text-foreground">{ns.join(", ")}</span>
                {data.delegation.servers.length > 0 && <> · {t("Now: {servers}", { servers: data.delegation.servers.join(", ") })}</>}
              </span>
            </div>
          ) : null}
          <p className="text-xs text-muted-foreground">{t("The NS and SOA records come from the server's DNS settings and are added automatically.")}</p>
        </CardBody>
      </Card>

      <Card>
        <CardHeader
          title={t("Records")}
          action={
            <Button
              size="sm"
              disabled={data.records.length >= data.max_records}
              onClick={() => setEditing({ name: "", type: "A", content: "", priority: 10, ttl: 3600 })}
            >
              <Plus /> {t("Add record")}
            </Button>
          }
        />
        {data.records.length === 0 ? (
          <Empty icon={Network} title={t("No records yet")} />
        ) : (
          <Table>
            <thead>
              <tr>
                <Th>{t("Name")}</Th>
                <Th>{t("Type")}</Th>
                <Th>{t("Value")}</Th>
                <Th>{t("TTL")}</Th>
                <Th className="w-24" />
              </tr>
            </thead>
            <tbody>
              {data.records.map((r) => (
                <tr key={r.id}>
                  <Td className="font-mono text-sm">{fqdn(r.name)}</Td>
                  <Td>
                    <Badge>{r.type}</Badge>
                  </Td>
                  <Td className="max-w-md break-all font-mono text-sm">
                    {(r.type === "MX" || r.type === "SRV") && <span className="mr-1.5 text-muted-foreground">{r.priority}</span>}
                    {r.content}
                  </Td>
                  <Td className="whitespace-nowrap text-muted-foreground">{duration(r.ttl)}</Td>
                  <Td className="whitespace-nowrap text-right">
                    <Button size="icon" variant="ghost" aria-label={t("Edit record")} onClick={() => setEditing({ ...r })}>
                      <Pencil />
                    </Button>
                    <Button size="icon" variant="ghost" aria-label={t("Delete record")} onClick={() => setDeleting(r)}>
                      <Trash2 />
                    </Button>
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>

      <Dialog open={!!editing} onOpenChange={(o) => !o && setEditing(null)}>
        {editing && (
          <DialogContent title={editing.id ? t("Edit record") : t("Add record")} className="max-w-xl">
            <RecordDialog domainId={domain.id} zone={zone} draft={editing} types={data.types} onClose={() => setEditing(null)} />
          </DialogContent>
        )}
      </Dialog>
      <Confirm
        open={!!deleting}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={t("Delete this record?")}
        description={deleting ? `${fqdn(deleting.name)} ${deleting.type} ${deleting.content}` : ""}
        loading={del.isPending}
        onConfirm={() => deleting && del.mutate(deleting.id)}
      />
    </div>
  );
}
