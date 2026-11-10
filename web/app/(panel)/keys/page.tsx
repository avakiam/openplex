"use client";
import { Check, Copy, KeyRound, Plus, Terminal, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import {
  Button,
  Card,
  CardHeader,
  Confirm,
  Dialog,
  DialogContent,
  Empty,
  ErrorBox,
  Field,
  Input,
  PageHeader,
  Select,
  Table,
  Td,
  Textarea,
  Th,
} from "@/components/ui";
import { api } from "@/lib/api";
import { keys, useAction, useAPIKeys, useSSHKeys } from "@/lib/hooks";
import { getLocale, useT } from "@/lib/i18n";
import type { APIKey } from "@/lib/types";
import { ago } from "@/lib/utils";

function CopyField({ value }: { value: string }) {
  const { t } = useT();
  const [done, setDone] = useState(false);
  return (
    <div className="flex gap-2">
      <Input readOnly value={value} className="font-mono text-xs" onFocus={(e) => e.target.select()} />
      <Button
        type="button"
        variant="outline"
        size="icon"
        aria-label={t("Copy")}
        onClick={() => {
          navigator.clipboard.writeText(value).then(() => { setDone(true); setTimeout(() => setDone(false), 1500); }, () => toast.error(t("Copy failed — select the text manually")));
        }}
      >
        {done ? <Check /> : <Copy />}
      </Button>
    </div>
  );
}

function APIKeys() {
  const { t } = useT();
  const list = useAPIKeys();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [days, setDays] = useState("0");
  const [created, setCreated] = useState<string | null>(null);
  const [del, setDel] = useState<APIKey | null>(null);
  const create = useAction(() => api.post<{ key: string }>("/api-keys", { name, expires_in_days: Number(days) }), {
    invalidate: [keys.apiKeys],
    onSuccess: (r) => { setCreated(r.key); setName(""); },
  });
  const remove = useAction((id: number) => api.del(`/api-keys/${id}`), { success: t("API key revoked"), invalidate: [keys.apiKeys], onSuccess: () => setDel(null) });

  return (
    <Card>
      <CardHeader title={t("API keys")} description={<>{t("For scripts and CI. Send it in the header:")} <code className="font-mono text-xs">Authorization: Bearer &lt;key&gt;</code></>} action={<Button size="sm" onClick={() => { setCreated(null); setOpen(true); }}><Plus /> {t("New key")}</Button>} />
      {(list.data ?? []).length === 0 ? (
        <Empty icon={KeyRound} title={t("No API keys")} />
      ) : (
        <Table>
          <thead><tr><Th>{t("Name")}</Th><Th>{t("Key")}</Th><Th>{t("Last used")}</Th><Th>{t("Expires")}</Th><Th className="w-12" /></tr></thead>
          <tbody>
            {list.data!.map((k) => (
              <tr key={k.id}>
                <Td className="font-medium">{k.name}</Td>
                <Td className="font-mono text-xs text-muted-foreground">{k.prefix}_••••</Td>
                <Td className="text-muted-foreground">{ago(k.last_used_at)}</Td>
                <Td className="text-muted-foreground">{k.expires_at ? new Date(k.expires_at).toLocaleDateString(getLocale()) : t("Never")}</Td>
                <Td><Button size="icon" variant="ghost" aria-label={t("Revoke {name}", { name: k.name })} onClick={() => setDel(k)}><Trash2 /></Button></Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent title={created ? t("Copy your new API key") : t("New API key")} description={created ? t("This is the only time the key is shown. Store it somewhere safe.") : undefined}>
          {created ? (
            <div className="grid gap-4"><CopyField value={created} /><div className="flex justify-end"><Button onClick={() => setOpen(false)}>{t("Done")}</Button></div></div>
          ) : (
            <form className="grid gap-4" onSubmit={(e) => { e.preventDefault(); create.mutate(undefined); }}>
              <Field label={t("Name")}><Input required autoFocus maxLength={64} placeholder="deploy-bot" value={name} onChange={(e) => setName(e.target.value)} /></Field>
              <Field label={t("Expires")}>
                <Select value={days} onChange={(e) => setDays(e.target.value)}>
                  <option value="30">{t("In {count} days", { count: 30 })}</option><option value="90">{t("In {count} days", { count: 90 })}</option><option value="365">{t("In 1 year")}</option><option value="0">{t("Never")}</option>
                </Select>
              </Field>
              <ErrorBox error={create.error} />
              <div className="flex justify-end gap-2"><Button type="button" variant="outline" onClick={() => setOpen(false)}>{t("Cancel")}</Button><Button type="submit" loading={create.isPending}>{t("Create key")}</Button></div>
            </form>
          )}
        </DialogContent>
      </Dialog>
      <Confirm open={!!del} onOpenChange={(o) => !o && setDel(null)} title={t("Revoke {name}?", { name: del?.name ?? "" })} description={t("Anything using this key stops working immediately.")} confirmLabel={t("Revoke")} loading={remove.isPending} onConfirm={() => remove.mutate(del!.id)} />
    </Card>
  );
}

function SSHKeys() {
  const { t } = useT();
  const list = useSSHKeys();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [key, setKey] = useState("");
  const create = useAction(() => api.post("/ssh-keys", { name, public_key: key }), {
    success: t("SSH key added"),
    invalidate: [keys.sshKeys],
    onSuccess: () => { setOpen(false); setName(""); setKey(""); },
  });
  const remove = useAction((id: number) => api.del(`/ssh-keys/${id}`), { success: t("SSH key removed"), invalidate: [keys.sshKeys] });
  return (
    <Card>
      <CardHeader title={t("SSH keys")} description={t("Public keys authorised for SFTP/SSH access to your sites. Ed25519, ECDSA or RSA ≥ 2048.")} action={<Button size="sm" onClick={() => setOpen(true)}><Plus /> {t("Add key")}</Button>} />
      {(list.data ?? []).length === 0 ? (
        <Empty icon={Terminal} title={t("No SSH keys")} />
      ) : (
        <Table>
          <thead><tr><Th>{t("Name")}</Th><Th>{t("Fingerprint")}</Th><Th>{t("Added")}</Th><Th className="w-12" /></tr></thead>
          <tbody>
            {list.data!.map((k) => (
              <tr key={k.id}>
                <Td className="font-medium">{k.name}<div className="text-xs font-normal text-muted-foreground">{k.public_key.split(" ")[0]}</div></Td>
                <Td className="break-all font-mono text-xs text-muted-foreground">{k.fingerprint}</Td>
                <Td className="text-muted-foreground">{ago(k.created_at)}</Td>
                <Td><Button size="icon" variant="ghost" aria-label={t("Remove {name}", { name: k.name })} onClick={() => remove.mutate(k.id)}><Trash2 /></Button></Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent title={t("Add SSH public key")}>
          <form className="grid gap-4" onSubmit={(e) => { e.preventDefault(); create.mutate(undefined); }}>
            <Field label={t("Name")}><Input required autoFocus maxLength={64} placeholder={t("laptop")} value={name} onChange={(e) => setName(e.target.value)} /></Field>
            <Field label={t("Public key")} hint={t("Contents of {path}", { path: "~/.ssh/id_ed25519.pub" })}>
              <Textarea required rows={4} className="font-mono text-xs" placeholder="ssh-ed25519 AAAA… user@host" value={key} onChange={(e) => setKey(e.target.value)} />
            </Field>
            <ErrorBox error={create.error} />
            <div className="flex justify-end gap-2"><Button type="button" variant="outline" onClick={() => setOpen(false)}>{t("Cancel")}</Button><Button type="submit" loading={create.isPending}>{t("Add key")}</Button></div>
          </form>
        </DialogContent>
      </Dialog>
    </Card>
  );
}

export default function KeysPage() {
  const { t } = useT();
  return (
    <>
      <PageHeader title={t("API & SSH keys")} description={t("Credentials for automation and file access.")} />
      <div className="grid gap-4">
        <APIKeys />
        <SSHKeys />
      </div>
    </>
  );
}
