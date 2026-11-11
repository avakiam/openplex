"use client";
import { Check, Copy, FolderSync, KeyRound, Plus, RefreshCw, Terminal, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import {
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
  PageHeader,
  Select,
  Table,
  Td,
  Textarea,
  Th,
} from "@/components/ui";
import { api } from "@/lib/api";
import { keys, useAction, useAPIKeys, useSFTP, useSSHKeys } from "@/lib/hooks";
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

/** A 20-character password from an unambiguous alphabet, with letters and digits (the policy). */
function generatePassword(): string {
  const abc = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  const buf = new Uint32Array(20);
  crypto.getRandomValues(buf);
  const pw = Array.from(buf, (n) => abc[n % abc.length]).join("");
  return /\d/.test(pw) && /[a-z]/i.test(pw) ? pw : generatePassword();
}

function SFTPAccess() {
  const { t } = useT();
  const { data } = useSFTP();
  const [open, setOpen] = useState(false);
  const [del, setDel] = useState(false);
  const [password, setPassword] = useState("");
  const save = useAction(() => api.put("/auth/me/sftp", { password }), {
    success: t("SFTP password saved"),
    invalidate: [keys.sftp],
    onSuccess: () => setOpen(false),
  });
  const remove = useAction(() => api.del("/auth/me/sftp"), {
    success: t("SFTP password removed"),
    invalidate: [keys.sftp],
    onSuccess: () => setDel(false),
  });
  if (!data) return null;
  const host = typeof window === "undefined" ? "" : window.location.hostname;

  return (
    <Card>
      <CardHeader
        title={t("SFTP access")}
        description={t("Upload and edit your websites' files with FileZilla, WinSCP, Cyberduck or any SFTP client.")}
      />
      <CardBody className="grid gap-4 text-sm">
        {!data.enabled ? (
          <p className="text-muted-foreground">{t("SFTP is turned off on this server.")}</p>
        ) : (
          <>
            <div className="grid gap-3 sm:grid-cols-[1fr_7rem_1fr]">
              <Field label={t("Host")}>
                <CopyField value={host} />
              </Field>
              <Field label={t("Port")}>
                <Input readOnly value={String(data.port)} className="font-mono text-xs" />
              </Field>
              <Field label={t("Username")}>
                <CopyField value={data.username} />
              </Field>
            </div>
            <p className="text-muted-foreground">
              {t("You will see one folder per website whose files you can manage ({count} now).", { count: String(data.sites) })}{" "}
              {t("Server key fingerprint, to compare on the first connection:")} <code className="break-all font-mono text-xs">{data.fingerprint}</code>
            </p>
            <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border p-3">
              <div>
                <p className="font-medium">{t("Sign in with")}</p>
                <p className="text-muted-foreground">
                  {data.password_set ? t("An SFTP password") : t("No SFTP password")} ·{" "}
                  {t("SSH keys (below): {count}", { count: String(data.keys) })}
                </p>
              </div>
              <div className="flex gap-2">
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    setPassword(generatePassword());
                    setOpen(true);
                  }}
                >
                  <FolderSync /> {data.password_set ? t("Change SFTP password") : t("Set an SFTP password")}
                </Button>
                {data.password_set && (
                  <Button size="sm" variant="outline" className="text-destructive" onClick={() => setDel(true)}>
                    <Trash2 /> {t("Remove")}
                  </Button>
                )}
              </div>
            </div>
          </>
        )}
      </CardBody>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent
          title={data.password_set ? t("Change SFTP password") : t("Set an SFTP password")}
          description={t("Only for SFTP, different from your panel password: an SFTP client stores it, and it can never open the panel.")}
        >
          <form
            className="grid gap-4"
            onSubmit={(e) => {
              e.preventDefault();
              save.mutate(undefined);
            }}
          >
            <Field label={t("Password")} hint={t("Copy it now: it cannot be shown again.")}>
              <div className="flex gap-2">
                <Input required minLength={10} maxLength={128} className="font-mono" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />
                <Button type="button" variant="outline" size="icon" aria-label={t("Generate a password")} onClick={() => setPassword(generatePassword())}>
                  <RefreshCw />
                </Button>
              </div>
            </Field>
            <ErrorBox error={save.error} />
            <div className="flex justify-end gap-2">
              <Button type="button" variant="outline" onClick={() => setOpen(false)}>
                {t("Cancel")}
              </Button>
              <Button type="submit" loading={save.isPending}>
                {t("Save")}
              </Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>
      <Confirm
        open={del}
        onOpenChange={setDel}
        title={t("Remove the SFTP password?")}
        description={t("SFTP clients that use it stop connecting. Your SSH keys keep working.")}
        loading={remove.isPending}
        onConfirm={() => remove.mutate(undefined)}
      />
    </Card>
  );
}

export default function KeysPage() {
  const { t } = useT();
  return (
    <>
      <PageHeader title={t("API & SSH keys")} description={t("Credentials for automation and file access.")} />
      <div className="grid gap-4">
        <SFTPAccess />
        <SSHKeys />
        <APIKeys />
      </div>
    </>
  );
}
