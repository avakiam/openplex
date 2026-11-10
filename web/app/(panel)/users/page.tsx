"use client";
import { KeyRound, MoreHorizontal, Plus, ShieldOff, Trash2, UserCheck, UserX, Users } from "lucide-react";
import { useState } from "react";

import { roleLabels } from "@/components/status";
import {
  Badge,
  Button,
  Card,
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
  PageHeader,
  Select,
  Spinner,
  Table,
  Td,
  Th,
} from "@/components/ui";
import { api } from "@/lib/api";
import { isAdmin, useAuth } from "@/lib/auth";
import { keys, useAction, useDomains, useUsers } from "@/lib/hooks";
import { LOCALES, useT, type LocalePref } from "@/lib/i18n";
import type { Role, User } from "@/lib/types";
import { ago } from "@/lib/utils";

const roleTone = { admin: "danger", reseller: "info", customer: "neutral" } as const;

function UserDialog({ open, onOpenChange, edit }: { open: boolean; onOpenChange: (o: boolean) => void; edit?: User }) {
  const { t } = useT();
  const { user: me } = useAuth();
  const users = useUsers();
  const [f, setF] = useState({ username: "", email: "", password: "", role: "customer" as Role, parent_id: "", max_domains: "0", max_databases: "0", locale: "" as LocalePref });
  const resellers = (users.data ?? []).filter((u) => u.role === "reseller");
  const reset = () => setF(edit ? { username: edit.username, email: edit.email, password: "", role: edit.role, parent_id: edit.parent_id ? String(edit.parent_id) : "", max_domains: String(edit.max_domains), max_databases: String(edit.max_databases), locale: edit.locale } : { username: "", email: "", password: "", role: "customer", parent_id: "", max_domains: "0", max_databases: "0", locale: "" });
  const save = useAction(
    () =>
      edit
        ? api.patch(`/users/${edit.id}`, {
            email: f.email,
            locale: f.locale,
            ...(isAdmin(me) && edit.id !== me?.id ? { role: f.role, parent_id: f.role === "customer" ? Number(f.parent_id || 0) : 0 } : {}),
            ...(edit.id !== me?.id ? { max_domains: Number(f.max_domains), max_databases: Number(f.max_databases) } : {}),
          })
        : api.post("/users", {
            username: f.username,
            email: f.email,
            password: f.password,
            role: f.role,
            parent_id: f.role === "customer" && f.parent_id ? Number(f.parent_id) : undefined,
            max_domains: Number(f.max_domains),
            max_databases: Number(f.max_databases),
            locale: f.locale,
          }),
    { success: edit ? t("User updated") : t("User created"), invalidate: [keys.users], onSuccess: () => onOpenChange(false) },
  );
  return (
    <Dialog open={open} onOpenChange={(o) => { if (o) reset(); onOpenChange(o); }}>
      <DialogContent title={edit ? t("Edit {username}", { username: edit.username }) : t("New user")}>
        <form className="grid gap-4" onSubmit={(e) => { e.preventDefault(); save.mutate(undefined); }} onFocus={() => !f.username && !edit && undefined}>
          {!edit && (
            <Field label={t("Username")} hint={t("Lowercase letters, digits, . _ -")}>
              <Input required autoFocus value={f.username} onChange={(e) => setF({ ...f, username: e.target.value.toLowerCase() })} />
            </Field>
          )}
          <Field label={t("Email")}><Input type="email" value={f.email} onChange={(e) => setF({ ...f, email: e.target.value })} /></Field>
          <Field label={t("Language")}>
            <Select value={f.locale} onChange={(e) => setF({ ...f, locale: e.target.value as LocalePref })}>
              <option value="">{t("Automatic")}</option>
              {LOCALES.map((l) => <option key={l.code} value={l.code}>{l.name}</option>)}
            </Select>
          </Field>
          {!edit && (
            <Field label={t("Password")} hint={t("At least 10 characters with letters and digits.")}>
              <Input type="password" required minLength={10} autoComplete="new-password" value={f.password} onChange={(e) => setF({ ...f, password: e.target.value })} />
            </Field>
          )}
          {isAdmin(me) && edit?.id !== me?.id && (
            <div className="grid grid-cols-2 gap-3">
              <Field label={t("Role")}>
                <Select value={f.role} onChange={(e) => setF({ ...f, role: e.target.value as Role })}>
                  <option value="customer">{t(roleLabels.customer)}</option><option value="reseller">{t(roleLabels.reseller)}</option><option value="admin">{t(roleLabels.admin)}</option>
                </Select>
              </Field>
              {f.role === "customer" && (
                <Field label={t("Reseller")}>
                  <Select value={f.parent_id} onChange={(e) => setF({ ...f, parent_id: e.target.value })}>
                    <option value="">{t("None (direct)")}</option>
                    {resellers.map((r) => <option key={r.id} value={r.id}>{r.username}</option>)}
                  </Select>
                </Field>
              )}
            </div>
          )}
          {edit?.id !== me?.id && (
            <div className="grid grid-cols-2 gap-3">
              <Field label={t("Website quota")} hint={t("0 = unlimited")}>
                <Input type="number" min={0} value={f.max_domains} onChange={(e) => setF({ ...f, max_domains: e.target.value })} />
              </Field>
              <Field label={t("Database quota")} hint={t("0 = unlimited")}>
                <Input type="number" min={0} value={f.max_databases} onChange={(e) => setF({ ...f, max_databases: e.target.value })} />
              </Field>
            </div>
          )}
          <ErrorBox error={save.error} />
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>{t("Cancel")}</Button>
            <Button type="submit" loading={save.isPending}>{edit ? t("Save") : t("Create user")}</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function PasswordDialog({ user, onClose }: { user: User | null; onClose: () => void }) {
  const { t } = useT();
  const [pw, setPw] = useState("");
  const save = useAction(() => api.post(`/users/${user!.id}/password`, { password: pw }), { success: t("Password changed — all sessions signed out"), onSuccess: () => { setPw(""); onClose(); } });
  return (
    <Dialog open={!!user} onOpenChange={(o) => !o && onClose()}>
      <DialogContent title={t("Set password for {username}", { username: user?.username ?? "" })} description={t("The user is signed out of all sessions.")}>
        <form className="grid gap-4" onSubmit={(e) => { e.preventDefault(); save.mutate(undefined); }}>
          <Field label={t("New password")}><Input type="password" required minLength={10} autoFocus autoComplete="new-password" value={pw} onChange={(e) => setPw(e.target.value)} /></Field>
          <ErrorBox error={save.error} />
          <div className="flex justify-end gap-2"><Button type="button" variant="outline" onClick={onClose}>{t("Cancel")}</Button><Button type="submit" loading={save.isPending}>{t("Set password")}</Button></div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export default function UsersPage() {
  const { t } = useT();
  const { user: me } = useAuth();
  const { data, isLoading, error } = useUsers();
  const domains = useDomains();
  const [create, setCreate] = useState(false);
  const [edit, setEdit] = useState<User | undefined>();
  const [pw, setPw] = useState<User | null>(null);
  const [del, setDel] = useState<User | null>(null);
  const inv = { invalidate: [keys.users] };
  const toggle = useAction((u: User) => api.patch(`/users/${u.id}`, { disabled: !u.disabled }), { success: t("Updated"), ...inv });
  const reset2fa = useAction((u: User) => api.post(`/users/${u.id}/2fa/reset`), { success: t("Two-factor authentication reset"), ...inv });
  const remove = useAction((u: User) => api.del(`/users/${u.id}`), { success: t("User deleted"), ...inv, onSuccess: () => setDel(null) });
  const count = (id: number) => (domains.data ?? []).filter((d) => d.owner_id === id && d.kind !== "alias").length;
  const name = (id: number | null) => (id ? data?.find((u) => u.id === id)?.username : null);

  return (
    <>
      <PageHeader
        title={t("Users")}
        description={isAdmin(me) ? t("Administrators, resellers and customers.") : t("Your customers.")}
        actions={<Button onClick={() => setCreate(true)}><Plus /> {t("New user")}</Button>}
      />
      <Card>
        {isLoading ? (
          <div className="flex justify-center py-14"><Spinner /></div>
        ) : error ? (
          <div className="p-4"><ErrorBox error={error} /></div>
        ) : !data?.length ? (
          <Empty icon={Users} title={t("No users")} />
        ) : (
          <Table>
            <thead>
              <tr><Th>{t("User")}</Th><Th>{t("Role")}</Th><Th>{t("Websites")}</Th><Th>2FA</Th><Th>{t("Status")}</Th><Th>{t("Created")}</Th><Th className="w-12" /></tr>
            </thead>
            <tbody>
              {data.map((u) => (
                <tr key={u.id}>
                  <Td>
                    <div className="font-medium">{u.username}{u.id === me?.id && <span className="ml-1 text-xs text-muted-foreground">{t("(you)")}</span>}</div>
                    <div className="text-xs text-muted-foreground">{u.email || "—"}{name(u.parent_id) && ` · ${t("via {reseller}", { reseller: name(u.parent_id)! })}`}</div>
                  </Td>
                  <Td><Badge tone={roleTone[u.role]}>{t(roleLabels[u.role])}</Badge></Td>
                  <Td className="tabular">{count(u.id)}{u.max_domains ? <span className="text-muted-foreground"> / {u.max_domains}</span> : ""}</Td>
                  <Td>{u.totp_enabled ? <Badge tone="success">{t("Enabled")}</Badge> : <span className="text-muted-foreground">{t("Disabled")}</span>}</Td>
                  <Td>{u.disabled ? <Badge tone="warning">{t("Suspended")}</Badge> : <Badge tone="success">{t("Active")}</Badge>}</Td>
                  <Td className="text-muted-foreground">{ago(u.created_at)}</Td>
                  <Td>
                    <Dropdown>
                      <DropdownTrigger className="rounded p-1.5 hover:bg-muted" aria-label={t("Actions for {username}", { username: u.username })}><MoreHorizontal className="size-4" /></DropdownTrigger>
                      <DropdownContent>
                        <DropdownItem onSelect={() => setEdit(u)}><UserCheck /> {t("Edit")}</DropdownItem>
                        {u.id !== me?.id && (
                          <>
                            <DropdownItem onSelect={() => setPw(u)}><KeyRound /> {t("Set password")}</DropdownItem>
                            {u.totp_enabled && <DropdownItem onSelect={() => reset2fa.mutate(u)}><ShieldOff /> {t("Reset 2FA")}</DropdownItem>}
                            <DropdownItem onSelect={() => toggle.mutate(u)}>{u.disabled ? <><UserCheck /> {t("Reactivate")}</> : <><UserX /> {t("Suspend")}</>}</DropdownItem>
                            <DropdownSeparator />
                            <DropdownItem destructive onSelect={() => setDel(u)}><Trash2 /> {t("Delete")}</DropdownItem>
                          </>
                        )}
                      </DropdownContent>
                    </Dropdown>
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
      <UserDialog open={create} onOpenChange={setCreate} />
      <UserDialog open={!!edit} onOpenChange={(o) => !o && setEdit(undefined)} edit={edit} key={edit?.id ?? "none"} />
      <PasswordDialog user={pw} onClose={() => setPw(null)} />
      <Confirm open={!!del} onOpenChange={(o) => !o && setDel(null)} title={t("Delete {username}?", { username: del?.username ?? "" })} description={t("Users who still own websites or customers cannot be deleted — remove or reassign those first.")} loading={remove.isPending} onConfirm={() => remove.mutate(del!)} />
    </>
  );
}
