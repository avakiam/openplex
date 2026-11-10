"use client";
import { Info, Network, Server, TriangleAlert } from "lucide-react";
import Link from "next/link";
import { useEffect, useState } from "react";

import { DelegationBadge } from "@/components/dns-records";
import { Badge, Button, Card, CardBody, CardHeader, Empty, ErrorBox, Field, Input, PageHeader, Spinner, Switch, Table, Td, Th } from "@/components/ui";
import { api } from "@/lib/api";
import { keys, useAction, useDNS } from "@/lib/hooks";
import { tServer, useT } from "@/lib/i18n";
import type { DNSSettings, DNSStatus } from "@/lib/types";
import { ago } from "@/lib/utils";

function SettingsCard({ st }: { st: DNSStatus }) {
  const { t } = useT();
  const pick = (s: DNSStatus): DNSSettings => ({ enabled: s.enabled, ns1: s.ns1, ns2: s.ns2, ipv4: s.ipv4, ipv6: s.ipv6, hostmaster: s.hostmaster });
  const [f, setF] = useState<DNSSettings>(pick(st));
  const key = JSON.stringify(pick(st));
  useEffect(() => setF(pick(st)), [key]); // eslint-disable-line react-hooks/exhaustive-deps
  const set = <K extends keyof DNSSettings>(k: K, v: DNSSettings[K]) => setF((s) => ({ ...s, [k]: v }));
  const save = useAction(() => api.put("/dns/settings", f), { success: t("DNS settings saved"), invalidate: [keys.dns] });
  return (
    <Card>
      <CardHeader title={t("DNS server")} description={t("Answers for the domains of this panel on port 53 (PowerDNS).")} />
      <form
        className="grid gap-4 p-5"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate(undefined);
        }}
      >
        <label className="flex items-start gap-3">
          <Switch checked={f.enabled} onCheckedChange={(v) => set("enabled", v)} className="mt-0.5" aria-label={t("DNS server on")} />
          <span>
            <span className="block font-medium">{f.enabled ? t("DNS server on") : t("DNS server off")}</span>
            <span className="text-sm text-muted-foreground">{t("Zones and records are kept either way; they are only published while the server is on.")}</span>
          </span>
        </label>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label={t("Nameserver 1")}>
            <Input className="font-mono" placeholder="ns1.example.com" value={f.ns1} onChange={(e) => set("ns1", e.target.value)} />
          </Field>
          <Field label={t("Nameserver 2")}>
            <Input className="font-mono" placeholder="ns2.example.com" value={f.ns2} onChange={(e) => set("ns2", e.target.value)} />
          </Field>
          <Field label={t("Server IPv4 address")} hint={t("New websites point here. Changing it also moves the records that use the old address.")}>
            <Input className="font-mono" placeholder="203.0.113.10" value={f.ipv4} onChange={(e) => set("ipv4", e.target.value)} />
          </Field>
          <Field label={t("Server IPv6 address (optional)")}>
            <Input className="font-mono" placeholder="2001:db8::10" value={f.ipv6} onChange={(e) => set("ipv6", e.target.value)} />
          </Field>
          <Field
            label={t("Hostmaster e-mail (optional)")}
            hint={t("Published in each zone's SOA record. Default: hostmaster@ the domain.")}
            className="sm:col-span-2"
          >
            <Input type="email" value={f.hostmaster} onChange={(e) => set("hostmaster", e.target.value)} />
          </Field>
        </div>
        <ErrorBox error={save.error} />
        <div className="flex justify-end">
          <Button type="submit" loading={save.isPending}>
            {t("Save settings")}
          </Button>
        </div>
      </form>
    </Card>
  );
}

function StatusCard({ st, zones }: { st: DNSStatus; zones: string[] }) {
  const { t } = useT();
  // Nameservers inside one of our own zones need glue records at the registrar.
  const glue = [st.ns1, st.ns2].filter((ns) => ns && zones.some((z) => ns === z || ns.endsWith("." + z)));
  return (
    <Card>
      <CardHeader
        title={t("Status")}
        action={st.enabled ? <Badge tone={st.running ? "success" : "danger"}>{st.running ? t("Running") : t("Not running")}</Badge> : <Badge>{t("Off")}</Badge>}
      />
      <CardBody className="grid gap-3 text-sm">
        {!st.available && <ErrorBox error={t("Docker is not available on this server.")} />}
        {st.last_error && <ErrorBox error={tServer(st.last_error)} />}
        <div className="flex items-center gap-2 text-muted-foreground">
          <Server className="size-4" /> {t("Answers on: {addresses}", { addresses: st.listen.join(", ") || "—" })}
        </div>
        {st.last_sync && <p className="text-xs text-muted-foreground">{t("Records last published {when}.", { when: ago(st.last_sync) })}</p>}
        {st.ns1 && st.ns2 && (
          <div className="grid gap-2 rounded-md bg-muted/60 p-3">
            <p className="flex items-center gap-2 font-medium">
              <Info className="size-4" /> {t("At each domain's registrar")}
            </p>
            <ol className="ml-5 list-decimal space-y-1 text-muted-foreground">
              <li>{t("Set the nameservers to {ns1} and {ns2}.", { ns1: st.ns1, ns2: st.ns2 })}</li>
              {glue.length > 0 && (
                <li>
                  {t(
                    'Because the nameservers are inside one of your domains, also create these glue records ("child nameservers") at the registrar of that domain:',
                  )}
                  <ul className="mt-1 font-mono text-xs text-foreground">
                    {glue.map((ns) => (
                      <li key={ns}>
                        {ns} → {[st.ipv4, st.ipv6].filter(Boolean).join(", ")}
                      </li>
                    ))}
                  </ul>
                </li>
              )}
              <li>{t("Changes at the registrar can take from minutes to a few hours to spread.")}</li>
            </ol>
          </div>
        )}
      </CardBody>
    </Card>
  );
}

export default function DNSPage() {
  const { t } = useT();
  const { data, isLoading, error } = useDNS();
  return (
    <>
      <PageHeader title="DNS" description={t("Nameservers for the domains hosted here, and whether each domain points at them.")} />
      {isLoading ? (
        <div className="flex justify-center py-10">
          <Spinner />
        </div>
      ) : error || !data ? (
        <ErrorBox error={error} />
      ) : (
        <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
          <div className="grid content-start gap-4">
            <SettingsCard st={data.status} />
            <StatusCard st={data.status} zones={data.zones.map((z) => z.name)} />
          </div>
          <Card>
            <CardHeader title={t("Zones")} description={t("One per website and alias. Edit the records on the website's DNS tab.")} />
            {data.zones.length === 0 ? (
              <Empty icon={Network} title={t("No zones yet")}>
                {t("A zone is created with each website.")}
              </Empty>
            ) : (
              <Table>
                <thead>
                  <tr>
                    <Th>{t("Domain")}</Th>
                    <Th className="text-right">{t("Records")}</Th>
                    <Th>{t("Delegation")}</Th>
                  </tr>
                </thead>
                <tbody>
                  {data.zones.map((z) => (
                    <tr key={z.id}>
                      <Td>
                        <Link className="font-medium hover:underline" href={`/domains/view/?id=${z.domain_id}`}>
                          {z.name}
                        </Link>
                      </Td>
                      <Td className="text-right tabular">{z.records}</Td>
                      <Td>
                        <DelegationBadge d={z.delegation} />
                        {z.delegation.status !== "ok" && z.delegation.servers.length > 0 && (
                          <div className="mt-1 text-xs text-muted-foreground">{z.delegation.servers.join(", ")}</div>
                        )}
                      </Td>
                    </tr>
                  ))}
                </tbody>
              </Table>
            )}
            {data.status.enabled && !data.status.running && (
              <div className="flex gap-2 border-t p-4 text-sm text-warning">
                <TriangleAlert className="size-4" /> {t("The DNS server should be running but is not.")}
              </div>
            )}
          </Card>
        </div>
      )}
    </>
  );
}
