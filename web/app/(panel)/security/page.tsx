"use client";
import { Ban, Globe, Info, Plus, RotateCcw, Shield, ShieldAlert, ShieldCheck, ShieldOff, Trash2, TriangleAlert, Undo2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import {
  Badge,
  Button,
  Card,
  CardBody,
  CardHeader,
  Confirm,
  Empty,
  ErrorBox,
  Field,
  Input,
  PageHeader,
  Select,
  Spinner,
  Switch,
  Table,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Td,
  Th,
} from "@/components/ui";
import { api } from "@/lib/api";
import { keys, useAction, useFail2Ban, useFirewall } from "@/lib/hooks";
import { tk, tServer, useT } from "@/lib/i18n";
import type { FirewallProtocol, FirewallRule, FirewallStatus } from "@/lib/types";
import { ago, dateTime } from "@/lib/utils";

const protocolLabels: Record<FirewallProtocol, string> = { tcp: "TCP", udp: "UDP", both: tk("TCP + UDP") };

const sameRules = (a: FirewallRule[], b: FirewallRule[]) =>
  a.length === b.length && a.every((r, i) => r.protocol === b[i].protocol && r.ports === b[i].ports && r.source === b[i].source && r.comment === b[i].comment);

/** Seconds left until iso, updated every second. */
function useCountdown(iso: string | undefined) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!iso) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [iso]);
  return iso ? Math.max(0, Math.ceil((Date.parse(iso) - now) / 1000)) : 0;
}

function PendingBanner({ st }: { st: FirewallStatus }) {
  const { t } = useT();
  const left = useCountdown(st.pending?.expires_at);
  const keep = useAction(() => api.post("/firewall/confirm"), { success: t("Firewall rules saved"), invalidate: [keys.firewall] });
  const undo = useAction(() => api.post("/firewall/revert"), { success: t("Previous rules restored"), invalidate: [keys.firewall] });
  if (!st.pending) return null;
  return (
    <div role="alert" className="mb-4 flex flex-wrap items-center gap-4 rounded-lg border border-warning/50 bg-warning/10 p-4">
      <TriangleAlert className="size-5 shrink-0 text-warning" />
      <div className="min-w-0 flex-1 basis-72 text-sm">
        <p className="font-medium">{t("The new rules are active. Keep them?")}</p>
        <p className="text-muted-foreground">
          {t("Open a new SSH connection to check you can still get in. If you do nothing, the previous rules come back in {seconds} s.", { seconds: left })}
        </p>
      </div>
      <div className="flex gap-2">
        <Button variant="outline" loading={undo.isPending} onClick={() => undo.mutate(undefined)}>
          <Undo2 /> {t("Undo now")}
        </Button>
        <Button loading={keep.isPending} disabled={left === 0} onClick={() => keep.mutate(undefined)}>
          <ShieldCheck /> {t("Keep the new rules")}
        </Button>
      </div>
    </div>
  );
}

const emptyRule: FirewallRule = { protocol: "tcp", ports: "", source: "", comment: "" };

function RulesTab({ st }: { st: FirewallStatus }) {
  const { t } = useT();
  const base = st.pending ?? st;
  const [enabled, setEnabled] = useState(base.enabled);
  const [rules, setRules] = useState<FirewallRule[]>(base.rules);
  const [draft, setDraft] = useState<FirewallRule>(emptyRule);
  // Follow the server whenever it changes (applied, kept, rolled back).
  const baseKey = JSON.stringify([base.enabled, base.rules]);
  useEffect(() => {
    setEnabled(base.enabled);
    setRules(base.rules);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [baseKey]);
  const dirty = enabled !== base.enabled || !sameRules(rules, base.rules);
  const apply = useAction(() => api.post("/firewall/apply", { enabled, rules }), { invalidate: [keys.firewall] });
  const sshPorts = st.ssh_ports.join(", ");
  const sshMissing = enabled && st.ssh_ports.some((p) => !rules.some((r) => r.protocol !== "udp" && portIn(p, r.ports)));

  const add = (e: React.FormEvent) => {
    e.preventDefault();
    setRules((rs) => [...rs, { ...draft, ports: draft.ports.trim(), source: draft.source.trim(), comment: draft.comment.trim() }]);
    setDraft(emptyRule);
  };

  return (
    <div className="grid gap-4">
      <Card>
        <CardBody className="grid gap-4">
          <label className="flex items-start gap-3">
            <Switch checked={enabled} onCheckedChange={setEnabled} className="mt-0.5" aria-label={t("Firewall on")} />
            <span>
              <span className="block font-medium">{enabled ? t("Firewall on") : t("Firewall off")}</span>
              <span className="text-sm text-muted-foreground">
                {enabled
                  ? t("Incoming connections are refused unless a rule below allows them.")
                  : t("Every port of the server is reachable. Blocked addresses still apply.")}
              </span>
            </span>
          </label>
          <div className="flex gap-2 rounded-md bg-muted/60 p-3 text-sm text-muted-foreground">
            <Globe className="mt-0.5 size-4 shrink-0" />
            <span>{t("Web traffic (ports 80 and 443) always reaches your websites and does not need a rule. To stop a visitor, block their address.")}</span>
          </div>
          {st.ufw_active && (
            <div className="flex gap-2 rounded-md border border-warning/40 bg-warning/8 p-3 text-sm">
              <Info className="mt-0.5 size-4 shrink-0 text-warning" />
              <span>
                {t(
                  "ufw is also active on this server. Its rules were imported here; while both are on, a port must be allowed in both. Once this firewall is on and working, turn ufw off with:",
                )}{" "}
                <code className="rounded bg-muted px-1 text-xs">sudo ufw disable</code>
              </span>
            </div>
          )}
        </CardBody>
      </Card>

      <Card>
        <CardHeader title={t("Allowed incoming connections")} description={t("SSH on this server listens on port {ports}.", { ports: sshPorts })} />
        {rules.length === 0 ? (
          <Empty icon={ShieldOff} title={t("No rules yet")} />
        ) : (
          <Table>
            <thead>
              <tr>
                <Th>{t("Port")}</Th>
                <Th>{t("Protocol")}</Th>
                <Th>{t("From")}</Th>
                <Th>{t("Comment")}</Th>
                <Th className="w-12" />
              </tr>
            </thead>
            <tbody>
              {rules.map((r, i) => (
                <tr key={i}>
                  <Td className="font-mono">{r.ports || t("all ports")}</Td>
                  <Td>{t(protocolLabels[r.protocol])}</Td>
                  <Td className="font-mono">{r.source || <span className="font-sans text-muted-foreground">{t("anywhere")}</span>}</Td>
                  <Td className="text-muted-foreground">{r.comment}</Td>
                  <Td>
                    <Button size="icon" variant="ghost" aria-label={t("Remove rule")} onClick={() => setRules((rs) => rs.filter((_, j) => j !== i))}>
                      <Trash2 />
                    </Button>
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
        <form onSubmit={add} className="grid gap-3 border-t p-5 sm:grid-cols-[7rem_8rem_1fr_1fr_auto] sm:items-end">
          <Field label={t("Port")}>
            <Input className="font-mono" placeholder="8080" value={draft.ports} onChange={(e) => setDraft({ ...draft, ports: e.target.value })} />
          </Field>
          <Field label={t("Protocol")}>
            <Select value={draft.protocol} onChange={(e) => setDraft({ ...draft, protocol: e.target.value as FirewallProtocol })}>
              {(Object.keys(protocolLabels) as FirewallProtocol[]).map((p) => (
                <option key={p} value={p}>
                  {t(protocolLabels[p])}
                </option>
              ))}
            </Select>
          </Field>
          <Field label={t("From (optional)")}>
            <Input className="font-mono" placeholder="203.0.113.0/24" value={draft.source} onChange={(e) => setDraft({ ...draft, source: e.target.value })} />
          </Field>
          <Field label={t("Comment")}>
            <Input maxLength={100} value={draft.comment} onChange={(e) => setDraft({ ...draft, comment: e.target.value })} />
          </Field>
          <Button type="submit" variant="outline" disabled={!draft.ports.trim() && !draft.source.trim()}>
            <Plus /> {t("Add")}
          </Button>
        </form>
      </Card>

      {sshMissing && (
        <div className="flex gap-2 rounded-md border border-destructive/40 bg-destructive/8 p-3 text-sm text-destructive">
          <ShieldAlert className="mt-0.5 size-4 shrink-0" />
          {t("No rule allows SSH (port {ports}). Add one, or you would lose SSH access to the server.", { ports: sshPorts })}
        </div>
      )}
      {st.last_error && <ErrorBox error={t("The last change could not be applied: {error}", { error: st.last_error })} />}
      <ErrorBox error={apply.error} />
      <div className="flex flex-wrap items-center justify-end gap-2">
        {st.applied_at && <span className="mr-auto text-xs text-muted-foreground">{t("Rules last loaded {when}.", { when: ago(st.applied_at) })}</span>}
        <Button
          variant="outline"
          disabled={!dirty || apply.isPending}
          onClick={() => {
            setEnabled(base.enabled);
            setRules(base.rules);
          }}
        >
          <RotateCcw /> {t("Discard changes")}
        </Button>
        <Button disabled={!dirty || !!st.pending || sshMissing} loading={apply.isPending} onClick={() => apply.mutate(undefined)}>
          <Shield /> {t("Apply changes")}
        </Button>
      </div>
      {!!st.pending && dirty && <p className="text-right text-xs text-muted-foreground">{t("Keep or undo the applied rules before making more changes.")}</p>}
    </div>
  );
}

function portIn(port: number, ports: string) {
  if (ports === "") return false;
  const [lo, hi] = ports.split(/[-:]/).map(Number);
  return port >= lo && port <= (hi || lo);
}

const durations = [
  { secs: 3600, label: tk("1 hour") },
  { secs: 86400, label: tk("1 day") },
  { secs: 7 * 86400, label: tk("1 week") },
  { secs: 30 * 86400, label: tk("30 days") },
  { secs: 0, label: tk("Permanently") },
];

function BlocksTab({ st }: { st: FirewallStatus }) {
  const { t } = useT();
  const [address, setAddress] = useState("");
  const [duration, setDuration] = useState(86400);
  const [comment, setComment] = useState("");
  const block = useAction(() => api.post("/firewall/blocks", { address: address.trim(), duration_sec: duration, comment }), {
    success: t("Address blocked"),
    invalidate: [keys.firewall],
    onSuccess: () => {
      setAddress("");
      setComment("");
    },
  });
  const unblock = useAction((id: number) => api.del(`/firewall/blocks/${id}`), { success: t("Address unblocked"), invalidate: [keys.firewall] });
  return (
    <Card>
      <CardHeader
        title={t("Blocked addresses")}
        description={t("Every connection from these addresses is dropped, including visits to your websites. Your address: {ip}", { ip: st.your_ip })}
      />
      <form
        className="grid gap-3 border-b p-5 sm:grid-cols-[1fr_9rem_1fr_auto] sm:items-end"
        onSubmit={(e) => {
          e.preventDefault();
          block.mutate(undefined);
        }}
      >
        <Field label={t("IP address or network")}>
          <Input required className="font-mono" placeholder="203.0.113.7" value={address} onChange={(e) => setAddress(e.target.value)} />
        </Field>
        <Field label={t("For")}>
          <Select value={duration} onChange={(e) => setDuration(Number(e.target.value))}>
            {durations.map((d) => (
              <option key={d.secs} value={d.secs}>
                {t(d.label)}
              </option>
            ))}
          </Select>
        </Field>
        <Field label={t("Comment")}>
          <Input maxLength={100} value={comment} onChange={(e) => setComment(e.target.value)} />
        </Field>
        <Button type="submit" variant="destructive" loading={block.isPending}>
          <Ban /> {t("Block")}
        </Button>
      </form>
      {block.error && (
        <div className="px-5 pt-3">
          <ErrorBox error={block.error} />
        </div>
      )}
      {st.blocks.length === 0 ? (
        <Empty icon={ShieldCheck} title={t("No blocked addresses")} />
      ) : (
        <Table>
          <thead>
            <tr>
              <Th>{t("Address")}</Th>
              <Th>{t("Comment")}</Th>
              <Th>{t("Until")}</Th>
              <Th>{t("Added")}</Th>
              <Th className="w-12" />
            </tr>
          </thead>
          <tbody>
            {st.blocks.map((b) => (
              <tr key={b.id}>
                <Td className="font-mono">{b.address}</Td>
                <Td className="text-muted-foreground">{b.comment}</Td>
                <Td className="whitespace-nowrap">{b.expires_at ? dateTime(b.expires_at) : <Badge>{t("permanent")}</Badge>}</Td>
                <Td className="whitespace-nowrap text-muted-foreground">{ago(b.created_at)}</Td>
                <Td>
                  <Button
                    size="icon"
                    variant="ghost"
                    aria-label={t("Unblock {address}", { address: b.address })}
                    loading={unblock.isPending && unblock.variables === b.id}
                    onClick={() => unblock.mutate(b.id)}
                  >
                    <Undo2 />
                  </Button>
                </Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </Card>
  );
}

const jailLabels: Record<string, string> = { sshd: tk("SSH logins"), openplex: tk("Panel logins") };

function Fail2BanTab() {
  const { t } = useT();
  const { data, isLoading, error } = useFail2Ban();
  const [target, setTarget] = useState<{ jail: string; ip: string } | null>(null);
  const unban = useAction((v: { jail: string; ip: string }) => api.post("/fail2ban/unban", v), {
    success: t("Ban lifted"),
    invalidate: [keys.fail2ban],
    onSuccess: () => setTarget(null),
  });
  if (isLoading)
    return (
      <div className="flex justify-center py-10">
        <Spinner />
      </div>
    );
  if (error) return <ErrorBox error={error} />;
  if (!data?.installed || !data.running)
    return (
      <Card>
        <Empty icon={ShieldOff} title={data?.installed ? t("Fail2Ban is installed but not running") : t("Fail2Ban is not installed")}>
          <p>{t("Re-run the OpenPlex installer to set it up, or start it on the server with:")}</p>
          <code className="mt-2 inline-block rounded bg-muted px-2 py-1 text-xs">
            {data?.installed ? "sudo systemctl enable --now fail2ban" : "sudo apt install fail2ban python3-systemd"}
          </code>
        </Empty>
      </Card>
    );
  return (
    <div className="grid gap-4">
      <p className="text-sm text-muted-foreground">
        {t("Fail2Ban watches the logs and bans addresses that keep failing to log in. A ban blocks the address completely for an hour.")}
      </p>
      {data.jails.length === 0 && (
        <Card>
          <Empty icon={Shield} title={t("No jails are active")} />
        </Card>
      )}
      <div className="grid gap-4 lg:grid-cols-2">
        {data.jails.map((j) => (
          <Card key={j.name}>
            <CardHeader title={jailLabels[j.name] ? t(jailLabels[j.name]) : j.name} description={j.name} />
            <CardBody className="grid gap-4">
              <dl className="grid grid-cols-3 gap-3 text-center tabular">
                {(
                  [
                    [t("Failed attempts now"), j.currently_failed],
                    [t("Banned now"), j.currently_banned],
                    [t("Bans in total"), j.total_banned],
                  ] as const
                ).map(([label, n]) => (
                  <div key={label} className="rounded-md bg-muted/60 p-3">
                    <dd className="text-2xl font-semibold">{n}</dd>
                    <dt className="text-xs text-muted-foreground">{label}</dt>
                  </div>
                ))}
              </dl>
              {j.banned.length > 0 ? (
                <ul className="divide-y rounded-md border">
                  {j.banned.map((ip) => (
                    <li key={ip} className="flex items-center justify-between gap-2 px-3 py-1.5">
                      <span className="font-mono text-sm">{ip}</span>
                      <Button size="sm" variant="ghost" onClick={() => setTarget({ jail: j.name, ip })}>
                        <Undo2 /> {t("Unban")}
                      </Button>
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="text-sm text-muted-foreground">{t("No addresses are banned right now.")}</p>
              )}
            </CardBody>
          </Card>
        ))}
      </div>
      <Confirm
        open={!!target}
        onOpenChange={(o) => !o && setTarget(null)}
        title={t("Unban {ip}?", { ip: target?.ip ?? "" })}
        description={t("The address can connect again right away. Fail2Ban bans it again if the failures continue.")}
        confirmLabel={t("Unban")}
        loading={unban.isPending}
        onConfirm={() => target && unban.mutate(target)}
      />
    </div>
  );
}

export default function SecurityPage() {
  const { t } = useT();
  const { data: st, isLoading, error } = useFirewall();
  const blocked = useMemo(() => st?.blocks.length ?? 0, [st]);
  const on = (st?.pending ?? st)?.enabled ?? false; // what is in effect, pending changes included
  return (
    <>
      <PageHeader
        title={t("Firewall")}
        description={t("Which connections reach this server, blocked addresses and brute-force protection.")}
        actions={st?.available ? <Badge tone={on ? "success" : "warning"}>{on ? t("Firewall on") : t("Firewall off")}</Badge> : undefined}
      />
      {isLoading ? (
        <div className="flex justify-center py-10">
          <Spinner />
        </div>
      ) : error ? (
        <ErrorBox error={error} />
      ) : !st ? null : (
        <>
          {st.available && <PendingBanner st={st} />}
          <Tabs defaultValue="rules">
            <TabsList>
              <TabsTrigger value="rules">{t("Rules")}</TabsTrigger>
              <TabsTrigger value="blocks">
                {t("Blocked addresses")}
                {blocked > 0 && <Badge className="ml-1.5">{blocked}</Badge>}
              </TabsTrigger>
              <TabsTrigger value="fail2ban">Fail2Ban</TabsTrigger>
            </TabsList>
            <TabsContent value="rules" className="mt-4">
              {st.available ? (
                <RulesTab st={st} />
              ) : (
                <Card>
                  <Empty icon={ShieldOff} title={t("The firewall cannot be managed on this server")}>
                    {st.unavailable && tServer(st.unavailable)}
                  </Empty>
                </Card>
              )}
            </TabsContent>
            <TabsContent value="blocks" className="mt-4">
              {st.available ? (
                <BlocksTab st={st} />
              ) : (
                <Card>
                  <Empty icon={ShieldOff} title={t("The firewall cannot be managed on this server")}>
                    {st.unavailable && tServer(st.unavailable)}
                  </Empty>
                </Card>
              )}
            </TabsContent>
            <TabsContent value="fail2ban" className="mt-4">
              <Fail2BanTab />
            </TabsContent>
          </Tabs>
        </>
      )}
    </>
  );
}
