"use client";
import { Check, Copy, KeyRound, MoreHorizontal, Pencil, Plus, RefreshCw, Trash2, UserRound } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { useCreateSFTPAccount, useDeleteSFTPAccount, useSFTPAccounts, useUpdateSFTPAccount } from "@/lib/hooks";
import { useT } from "@/lib/i18n";
import type { Domain, SFTPAccount, SFTPAccountInput } from "@/lib/types";
import { ago, dateTime, generatePassword } from "@/lib/utils";

import {
  Badge,
  Button,
  Card,
  CardBody,
  CardHeader,
  Confirm,
  Dialog,
  DialogContent,
  Dropdown,
  DropdownContent,
  DropdownItem,
  DropdownSeparator,
  DropdownTrigger,
  Empty,
  ErrorBox,
  Field,
  Input,
  Spinner,
  Switch,
  Table,
  Td,
  Textarea,
  Th,
} from "./ui";

function CopyField({ value, label }: { value: string; label: string }) {
  const { t } = useT();
  const [done, setDone] = useState(false);
  return (
    <div className="flex gap-2">
      <Input readOnly aria-label={label} value={value} className="font-mono text-xs" onFocus={(e) => e.target.select()} />
      <Button
        type="button"
        variant="outline"
        size="icon"
        aria-label={t("Copy")}
        onClick={() =>
          navigator.clipboard.writeText(value).then(
            () => {
              setDone(true);
              setTimeout(() => setDone(false), 1500);
            },
            () => toast.error(t("Copy failed — select the text manually")),
          )
        }
      >
        {done ? <Check /> : <Copy />}
      </Button>
    </div>
  );
}

/** "2026-12-31" from an RFC 3339 time, in local time, for a date input. */
function toDateInput(iso: string | null) {
  if (!iso) return "";
  const d = new Date(iso);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

/** The end of that day in local time, as RFC 3339 ("" = no end date). */
function fromDateInput(v: string) {
  return v ? new Date(`${v}T23:59:59`).toISOString() : "";
}

type Draft = { name: string; password: string; public_key: string; directory: string; read_only: boolean; enabled: boolean; expires: string };

/** SFTP accounts of one website: name@website logins without panel access. */
export function SFTPAccounts({ domain }: { domain: Domain }) {
  const { t } = useT();
  const { data, isLoading, error } = useSFTPAccounts(domain.id);
  const [editing, setEditing] = useState<{ account: SFTPAccount | null } | null>(null);
  const [created, setCreated] = useState<{ username: string; password: string } | null>(null);
  const [toDelete, setToDelete] = useState<SFTPAccount | null>(null);
  const update = useUpdateSFTPAccount(domain.id);
  const del = useDeleteSFTPAccount(domain.id, () => setToDelete(null));

  const accounts = data?.accounts ?? [];
  const max = data?.max_accounts ?? 20;
  const full = accounts.length >= max;
  const host = typeof window === "undefined" ? "" : window.location.hostname;

  return (
    <Card>
      <CardHeader
        title={t("SFTP accounts")}
        description={t(
          "Give a developer or designer access to this website's files only, with their own login and without a panel account. They sign in as name{suffix}.",
          { suffix: data?.suffix ?? `@${domain.name}` },
        )}
        action={
          <Button
            size="sm"
            disabled={!data || full || !data.enabled}
            title={full ? t("This website has reached the limit of {count} SFTP accounts.", { count: max }) : undefined}
            onClick={() => setEditing({ account: null })}
          >
            <Plus /> {t("New account")}
          </Button>
        }
      />
      {isLoading ? (
        <div className="flex justify-center py-10">
          <Spinner />
        </div>
      ) : error ? (
        <div className="p-4">
          <ErrorBox error={error} />
        </div>
      ) : !data?.enabled ? (
        <CardBody className="text-sm text-muted-foreground">{t("SFTP is turned off on this server.")}</CardBody>
      ) : (
        <>
          <CardBody className="grid gap-3 border-b text-sm sm:grid-cols-[1fr_7rem]">
            <Field label={t("Host")}>
              <CopyField label={t("Host")} value={host} />
            </Field>
            <Field label={t("Port")}>
              <Input readOnly value={String(data.port ?? "")} className="font-mono text-xs" />
            </Field>
            <p className="text-xs text-muted-foreground sm:col-span-2">
              {t("Server key fingerprint, to compare on the first connection:")} <code className="break-all font-mono">{data.fingerprint}</code>
            </p>
          </CardBody>
          {accounts.length === 0 ? (
            <Empty icon={UserRound} title={t("No SFTP accounts")}>
              <p className="mx-auto max-w-lg">
                {t("You and the people who manage this website already reach it with their own panel username (API & SSH keys → SFTP access).")}
              </p>
            </Empty>
          ) : (
            <Table>
              <thead>
                <tr>
                  <Th>{t("Account")}</Th>
                  <Th>{t("Folder")}</Th>
                  <Th>{t("Sign in with")}</Th>
                  <Th>{t("Enabled")}</Th>
                  <Th>{t("Last sign-in")}</Th>
                  <Th className="w-12" />
                </tr>
              </thead>
              <tbody>
                {accounts.map((a) => (
                  <tr key={a.id} className={a.enabled && !a.expired ? undefined : "text-muted-foreground"}>
                    <Td>
                      <div className="font-mono text-xs font-medium">{a.username}</div>
                      <div className="mt-1 flex flex-wrap gap-1">
                        {a.read_only && <Badge tone="neutral">{t("Read-only")}</Badge>}
                        {a.expired ? (
                          <Badge tone="warning">{t("Expired")}</Badge>
                        ) : (
                          a.expires_at && (
                            <span className="text-xs text-muted-foreground" title={dateTime(a.expires_at)}>
                              {t("Until {date}", { date: dateTime(a.expires_at) })}
                            </span>
                          )
                        )}
                      </div>
                    </Td>
                    <Td className="font-mono text-xs">/{a.directory}</Td>
                    <Td className="text-xs">
                      {[a.has_password ? t("Password") : "", a.key_fingerprint ? t("SSH key") : ""].filter(Boolean).join(" · ")}
                    </Td>
                    <Td>
                      <Switch
                        checked={a.enabled}
                        disabled={update.isPending && update.variables?.aid === a.id}
                        aria-label={t("Enable {name}", { name: a.username })}
                        onCheckedChange={(v) => update.mutate({ aid: a.id, enabled: v })}
                      />
                    </Td>
                    <Td className="whitespace-nowrap">
                      {a.last_login_at ? <span title={dateTime(a.last_login_at)}>{ago(a.last_login_at)}</span> : t("Never")}
                    </Td>
                    <Td className="text-right">
                      <Dropdown>
                        <DropdownTrigger className="rounded p-1.5 hover:bg-muted" aria-label={t("Actions for {name}", { name: a.username })}>
                          <MoreHorizontal className="size-4" />
                        </DropdownTrigger>
                        <DropdownContent>
                          <DropdownItem onSelect={() => setEditing({ account: a })}>
                            <Pencil /> {t("Edit")}
                          </DropdownItem>
                          <DropdownSeparator />
                          <DropdownItem destructive onSelect={() => setToDelete(a)}>
                            <Trash2 /> {t("Delete")}
                          </DropdownItem>
                        </DropdownContent>
                      </Dropdown>
                    </Td>
                  </tr>
                ))}
              </tbody>
            </Table>
          )}
        </>
      )}

      <Dialog open={!!editing} onOpenChange={(o) => !o && setEditing(null)}>
        {editing && (
          <DialogContent title={editing.account ? t("Edit {name}", { name: editing.account.username }) : t("New SFTP account")} className="max-w-xl">
            <AccountForm
              domain={domain}
              suffix={data?.suffix ?? `@${domain.name}`}
              account={editing.account}
              onDone={(c) => {
                setEditing(null);
                if (c) setCreated(c);
              }}
            />
          </DialogContent>
        )}
      </Dialog>

      <Dialog open={!!created} onOpenChange={(o) => !o && setCreated(null)}>
        {created && (
          <DialogContent title={t("Account ready")} description={t("Send these details to the person who will use it. The password cannot be shown again.")}>
            <div className="grid gap-3">
              <Field label={t("Host")}>
                <CopyField label={t("Host")} value={host} />
              </Field>
              <Field label={t("Port")}>
                <Input readOnly value={String(data?.port ?? "")} className="font-mono text-xs" />
              </Field>
              <Field label={t("Username")}>
                <CopyField label={t("Username")} value={created.username} />
              </Field>
              {created.password && (
                <Field label={t("Password")}>
                  <CopyField label={t("Password")} value={created.password} />
                </Field>
              )}
              <div className="flex justify-end">
                <Button onClick={() => setCreated(null)}>{t("Done")}</Button>
              </div>
            </div>
          </DialogContent>
        )}
      </Dialog>

      <Confirm
        open={!!toDelete}
        onOpenChange={(o) => !o && setToDelete(null)}
        title={t("Delete {name}?", { name: toDelete?.username ?? "" })}
        description={t("It can no longer sign in, and an open session loses access within seconds. The website's files stay.")}
        loading={del.isPending}
        onConfirm={() => del.mutate(toDelete!.id)}
      />
    </Card>
  );
}

function AccountForm({
  domain,
  suffix,
  account,
  onDone,
}: {
  domain: Domain;
  suffix: string;
  account: SFTPAccount | null;
  onDone: (created?: { username: string; password: string }) => void;
}) {
  const { t } = useT();
  const [d, setD] = useState<Draft>(
    account
      ? { name: "", password: "", public_key: "", directory: account.directory, read_only: account.read_only, enabled: account.enabled, expires: toDateInput(account.expires_at) }
      : { name: "", password: generatePassword(), public_key: "", directory: "", read_only: false, enabled: true, expires: "" },
  );
  const [removePassword, setRemovePassword] = useState(false);
  const [removeKey, setRemoveKey] = useState(false);
  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => setD((p) => ({ ...p, [k]: v }));
  const create = useCreateSFTPAccount(domain.id, (a) => onDone({ username: a.username, password: d.password }));
  const update = useUpdateSFTPAccount(domain.id, () => {
    toast.success(t("SFTP account saved"));
    onDone();
  });
  const mutation = account ? update : create;

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const body: SFTPAccountInput = { directory: d.directory.trim(), read_only: d.read_only, enabled: d.enabled, expires_at: fromDateInput(d.expires) };
    if (!account) {
      body.name = d.name.trim();
      if (d.password) body.password = d.password;
      if (d.public_key.trim()) body.public_key = d.public_key.trim();
      create.mutate(body);
      return;
    }
    if (d.expires === toDateInput(account.expires_at)) delete body.expires_at; // unchanged (it may already have passed)
    if (removePassword) body.password = "";
    else if (d.password) body.password = d.password;
    if (removeKey) body.public_key = "";
    else if (d.public_key.trim()) body.public_key = d.public_key.trim();
    update.mutate({ aid: account.id, ...body });
  };

  return (
    <form className="grid gap-4" onSubmit={submit}>
      {!account && (
        <Field label={t("Name")} hint={t("Lowercase letters, digits, '.', '_' and '-'.")}>
          <div className="flex items-center gap-2">
            <Input required maxLength={32} pattern="[a-zA-Z0-9][a-zA-Z0-9_.\-]*" className="font-mono" placeholder="dev" value={d.name} onChange={(e) => set("name", e.target.value)} />
            <span className="shrink-0 font-mono text-sm text-muted-foreground">{suffix}</span>
          </div>
        </Field>
      )}

      <Field
        label={t("Password")}
        hint={
          account
            ? account.has_password
              ? t("Leave it empty to keep the current one.")
              : t("Optional if the account signs in with an SSH key.")
            : t("Copy it now: it cannot be shown again. Optional if the account signs in with an SSH key.")
        }
      >
        <div className="flex gap-2">
          <Input
            minLength={10}
            maxLength={128}
            className="font-mono"
            autoComplete="new-password"
            disabled={removePassword}
            value={d.password}
            onChange={(e) => set("password", e.target.value)}
          />
          <Button type="button" variant="outline" size="icon" aria-label={t("Generate a password")} disabled={removePassword} onClick={() => set("password", generatePassword())}>
            <RefreshCw />
          </Button>
        </div>
      </Field>
      {account?.has_password && (
        <label className="-mt-2 flex items-center gap-2 text-sm">
          <Switch checked={removePassword} onCheckedChange={setRemovePassword} aria-label={t("Remove the password")} />
          {t("Remove the password")}
        </label>
      )}

      <Field
        label={t("SSH public key")}
        hint={
          account?.key_fingerprint ? (
            <>
              <KeyRound className="mr-1 inline size-3" />
              {t("Current key: {fingerprint}. Paste a new one to replace it.", { fingerprint: account.key_fingerprint })}
            </>
          ) : (
            t("Optional. The contents of id_ed25519.pub or id_rsa.pub.")
          )
        }
      >
        <Textarea
          className="min-h-20 font-mono text-xs"
          spellCheck={false}
          placeholder="ssh-ed25519 AAAA… dev@laptop"
          disabled={removeKey}
          value={d.public_key}
          onChange={(e) => set("public_key", e.target.value)}
        />
      </Field>
      {account?.key_fingerprint && (
        <label className="-mt-2 flex items-center gap-2 text-sm">
          <Switch checked={removeKey} onCheckedChange={setRemoveKey} aria-label={t("Remove the SSH key")} />
          {t("Remove the SSH key")}
        </label>
      )}

      <Field label={t("Folder")} hint={t("Empty for the whole website, or a folder of it such as public/wp-content/uploads. The account sees nothing above it.")}>
        <Input className="font-mono" placeholder="public" value={d.directory} onChange={(e) => set("directory", e.target.value)} />
      </Field>

      <div className="grid gap-4 sm:grid-cols-2 sm:items-end">
        <Field label={t("End date")} hint={t("Optional. From the end of that day it cannot sign in.")}>
          <Input type="date" value={d.expires} onChange={(e) => set("expires", e.target.value)} />
        </Field>
        <div className="grid gap-3 pb-1">
          <label className="flex items-center gap-2 text-sm">
            <Switch checked={d.read_only} onCheckedChange={(v) => set("read_only", v)} aria-label={t("Read-only")} />
            {t("Read-only (download only)")}
          </label>
          {account && (
            <label className="flex items-center gap-2 text-sm">
              <Switch checked={d.enabled} onCheckedChange={(v) => set("enabled", v)} aria-label={t("Enabled")} />
              {t("Enabled")}
            </label>
          )}
        </div>
      </div>

      <ErrorBox error={mutation.error} />
      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" onClick={() => onDone()}>
          {t("Cancel")}
        </Button>
        <Button type="submit" loading={mutation.isPending}>
          {account ? t("Save") : t("Create account")}
        </Button>
      </div>
    </form>
  );
}
