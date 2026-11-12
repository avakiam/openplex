"use client";
import { Download, Plus, ShieldCheck, TriangleAlert } from "lucide-react";
import { useMemo, useState } from "react";
import { toast } from "sonner";

import { Button, Dialog, DialogContent, ErrorBox, Field, Spinner, Textarea } from "@/components/ui";
import { api, download } from "@/lib/api";
import { keys, useAction, useRemoteDBInfo } from "@/lib/hooks";
import { useT } from "@/lib/i18n";
import type { Database } from "@/lib/types";

const CERT_FILE = "openplex-databases.pem";

function Code({ children }: { children: string }) {
  return <pre className="overflow-x-auto rounded-md bg-muted px-3 py-2 font-mono text-xs whitespace-pre-wrap break-all">{children}</pre>;
}

/** Remote access of a MariaDB/PostgreSQL database: the addresses allowed to
 * reach it from outside the server, and how to connect. */
export function RemoteAccessDialog({ db, onClose }: { db: Database | null; onClose: () => void }) {
  const { t } = useT();
  const info = useRemoteDBInfo(!!db);
  // The page remounts this dialog per database (key), so this starts from its list.
  const [text, setText] = useState(() => db?.remote_allow.join("\n") ?? "");
  const entries = useMemo(
    () =>
      text
        .split(/[\s,;]+/)
        .map((s) => s.trim())
        .filter(Boolean),
    [text],
  );
  const save = useAction((allow: string[]) => api.put<Database>(`/databases/${db!.id}/remote`, { allow }), {
    invalidate: [keys.databases],
    onSuccess: (d) => {
      toast.success(d.remote_allow.length ? t("Remote access is on") : t("Remote access is off"));
      setText(d.remote_allow.join("\n"));
    },
  });
  const saved = useMemo(() => {
    const d = save.data && db && save.data.id === db.id ? save.data : db;
    return d?.remote_allow ?? [];
  }, [save.data, db]);
  const myIP = info.data?.client_ip ?? "";
  const anyone = entries.some((e) => e === "0.0.0.0/0" || e === "::/0");
  const port = db?.remote_port ?? 0;
  const host = typeof window === "undefined" ? "" : window.location.hostname;
  const ufw = !!info.data?.ufw_blocks.includes(port);

  return (
    <Dialog
      open={!!db}
      onOpenChange={(o) => {
        if (!o) {
          save.reset();
          onClose();
        }
      }}
    >
      <DialogContent
        className="max-w-2xl"
        title={t("Remote access to {name}", { name: db?.name ?? "" })}
        description={t(
          "Let database tools outside this server (DBeaver, TablePlus, HeidiSQL, mysql, psql…) connect to this database. Only the addresses listed get in, and connections must use TLS.",
        )}
      >
        {!db || info.isLoading ? (
          <div className="flex justify-center py-8">
            <Spinner />
          </div>
        ) : (
          <form
            className="grid gap-4 text-sm"
            onSubmit={(e) => {
              e.preventDefault();
              save.mutate(entries);
            }}
          >
            <Field
              label={t("Allowed addresses")}
              hint={t("One IP address or network (CIDR, e.g. 203.0.113.0/24) per line, up to 20. Leave it empty to turn remote access off.")}
            >
              <Textarea rows={4} className="font-mono text-xs" value={text} onChange={(e) => setText(e.target.value)} placeholder="203.0.113.7" />
            </Field>
            {myIP && !entries.includes(myIP) && (
              <div>
                <Button type="button" variant="outline" size="sm" onClick={() => setText((s) => (s.trim() ? s.trim() + "\n" : "") + myIP)}>
                  <Plus /> {t("Add my address ({ip})", { ip: myIP })}
                </Button>
              </div>
            )}
            {anyone && (
              <div className="flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/10 p-3 text-destructive">
                <TriangleAlert className="mt-0.5 size-4 shrink-0" />
                <p>{t("Anyone on the internet will be able to try passwords on this database. Prefer the addresses you connect from.")}</p>
              </div>
            )}
            {ufw && (
              <div className="flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/10 p-3 text-destructive">
                <TriangleAlert className="mt-0.5 size-4 shrink-0" />
                <p>
                  {t("The server's firewall (ufw) does not let port {port} in, so remote clients cannot connect. An administrator must run on the server:", {
                    port: String(port),
                  })}{" "}
                  <code className="font-mono text-xs">ufw allow {port}/tcp</code>
                </p>
              </div>
            )}
            <ErrorBox error={save.error} />
            <div className="flex justify-end gap-2">
              {saved.length > 0 && (
                <Button type="button" variant="outline" loading={save.isPending && save.variables?.length === 0} onClick={() => save.mutate([])}>
                  {t("Turn off")}
                </Button>
              )}
              <Button type="submit" loading={save.isPending && (save.variables?.length ?? 0) > 0} disabled={entries.length === 0 && saved.length === 0}>
                {t("Save")}
              </Button>
            </div>

            {saved.length > 0 && (
              <div className="grid gap-3 border-t pt-4">
                <p className="flex items-center gap-2 font-medium">
                  <ShieldCheck className="size-4 text-success" /> {t("How to connect")}
                </p>
                <p className="text-muted-foreground">
                  {t(
                    "Use host {host}, port {port}, the database's own user name and password (Connection details), and TLS (SSL) set to required. The server's certificate is OpenPlex's own: download it to let your client verify it, or compare its fingerprint.",
                    { host, port: String(port) },
                  )}
                </p>
                <Field label={t("Certificate fingerprint (SHA-256)")}>
                  <code className="block rounded-md bg-muted px-3 py-2 font-mono text-[11px] break-all">{info.data?.fingerprint}</code>
                </Field>
                <div>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => download("/databases/remote/certificate", CERT_FILE).catch((e: Error) => toast.error(e.message))}
                  >
                    <Download /> {t("Download certificate")}
                  </Button>
                </div>
                {db.engine === "mariadb" ? (
                  <Code>{`mariadb -h ${host} -P ${port} -u ${db.username} -p --ssl-ca=${CERT_FILE} ${db.name}`}</Code>
                ) : (
                  <Code>{`psql "host=${host} port=${port} dbname=${db.name} user=${db.username} sslmode=verify-ca sslrootcert=${CERT_FILE}"`}</Code>
                )}
              </div>
            )}
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
