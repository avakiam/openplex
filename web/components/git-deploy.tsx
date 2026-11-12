"use client";
import { Check, Copy, GitBranch, Play, RefreshCw, Trash2, TriangleAlert } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";

import { Badge, Button, Card, CardBody, CardHeader, ErrorBox, Field, Input, Spinner, Switch, Textarea } from "@/components/ui";
import { api } from "@/lib/api";
import { keys, useAction, useGitDeploy } from "@/lib/hooks";
import { tServer, useT } from "@/lib/i18n";
import type { Domain, GitDeploy } from "@/lib/types";
import { ago } from "@/lib/utils";

function CopyField({ label, value, hint }: { label: string; value: string; hint?: React.ReactNode }) {
  const { t } = useT();
  const [copied, setCopied] = useState(false);
  return (
    <Field label={label} hint={hint}>
      <div className="flex gap-2">
        <Input readOnly value={value} className="font-mono text-xs" onFocus={(e) => e.target.select()} />
        <Button
          type="button"
          variant="outline"
          size="icon"
          aria-label={t("Copy")}
          onClick={() =>
            navigator.clipboard.writeText(value).then(() => {
              setCopied(true);
              setTimeout(() => setCopied(false), 1500);
            })
          }
        >
          {copied ? <Check /> : <Copy />}
        </Button>
      </div>
    </Field>
  );
}

const statusTone = { running: "neutral", success: "success", error: "danger" } as const;

/** The Git tab of a website: repository settings, deploy key, webhook, deploys. */
export function GitDeployTab({ d, readOnly }: { d: Domain; readOnly: boolean }) {
  const { t } = useT();
  const { data: g, isLoading, error } = useGitDeploy(d.id);
  const [f, setF] = useState({ repo_url: "", branch: "main", target: d.document_root, build_command: "", auto_deploy: true });
  useEffect(() => {
    if (g) setF({ repo_url: g.repo_url, branch: g.branch, target: g.target, build_command: g.build_command, auto_deploy: g.auto_deploy });
  }, [g]);
  const inv = { invalidate: [keys.git(d.id)] };
  const save = useAction((rotate: boolean) => api.put<GitDeploy>(`/domains/${d.id}/git`, { ...f, rotate_webhook: rotate }), { success: t("Saved"), ...inv });
  const deploy = useAction(() => api.post<{ queued: boolean }>(`/domains/${d.id}/git/deploy`), {
    ...inv,
    onSuccess: (r) => toast.success(r.queued ? t("A deploy is running; another one will follow it") : t("Deploying")),
  });
  const remove = useAction(() => api.del(`/domains/${d.id}/git`), { success: t("Git deploy removed. The files stay."), ...inv });
  if (isLoading) return <Spinner />;
  if (error) return <ErrorBox error={error} />;
  const set = <K extends keyof typeof f>(k: K, v: (typeof f)[K]) => setF((s) => ({ ...s, [k]: v }));
  const webhook = g ? `${window.location.origin}${g.webhook_path}` : "";
  const running = g?.status === "running";

  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <Card>
        <CardHeader
          title={t("Repository")}
          description={t("The branch is fetched into a folder of this website; files the repository does not track (uploads, .env…) are kept.")}
        />
        <CardBody>
          <form
            className="grid gap-4"
            onSubmit={(e) => {
              e.preventDefault();
              save.mutate(false);
            }}
          >
            <Field label={t("Repository address")} hint={t("SSH (git@github.com:owner/repo.git) for private repositories, or HTTPS for public ones.")}>
              <Input
                required
                disabled={readOnly}
                className="font-mono text-sm"
                value={f.repo_url}
                placeholder="git@github.com:owner/repo.git"
                onChange={(e) => set("repo_url", e.target.value.trim())}
              />
            </Field>
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label={t("Branch")}>
                <Input required disabled={readOnly} className="font-mono text-sm" value={f.branch} onChange={(e) => set("branch", e.target.value.trim())} />
              </Field>
              <Field
                label={t("Folder")}
                hint={t("Inside the website's directory; empty is the directory itself. The document root is {root}.", { root: d.document_root || "/" })}
              >
                <Input disabled={readOnly} className="font-mono text-sm" value={f.target} placeholder="/" onChange={(e) => set("target", e.target.value)} />
              </Field>
            </div>
            <Field
              label={t("Build command (optional)")}
              hint={
                d.runtime === "static"
                  ? t("Runs in the folder, in a shell with git, curl and common tools.")
                  : t("Runs in the folder, inside this website's {runtime} container: composer install, npm ci && npm run build…", {
                      runtime: `${d.runtime} ${d.runtime_version}`,
                    })
              }
            >
              <Textarea
                rows={2}
                disabled={readOnly}
                className="font-mono text-sm"
                value={f.build_command}
                onChange={(e) => set("build_command", e.target.value)}
              />
            </Field>
            <label className="flex items-center justify-between gap-3 text-sm">
              <span>
                {t("Deploy on every push")}
                <span className="block text-xs text-muted-foreground">{t("When the repository calls the webhook below.")}</span>
              </span>
              <Switch checked={f.auto_deploy} disabled={readOnly} onCheckedChange={(v) => set("auto_deploy", v)} aria-label={t("Deploy on every push")} />
            </label>
            {["node", "python", "go"].includes(d.runtime) && (
              <p className="text-xs text-muted-foreground">{t("The application is restarted after each deploy.")}</p>
            )}
            <ErrorBox error={save.error} />
            {!readOnly && (
              <div className="flex flex-wrap justify-end gap-2">
                {g && (
                  <Button type="button" variant="ghost" loading={remove.isPending} onClick={() => remove.mutate(undefined)}>
                    <Trash2 /> {t("Remove")}
                  </Button>
                )}
                <Button type="submit" loading={save.isPending}>
                  {g ? t("Save") : t("Set up Git deploy")}
                </Button>
              </div>
            )}
          </form>
        </CardBody>
      </Card>

      {g && (
        <div className="grid content-start gap-4">
          <Card>
            <CardHeader title={t("Connect the repository")} />
            <CardBody className="grid gap-4">
              <CopyField
                label={t("Deploy key")}
                value={g.public_key}
                hint={t(
                  "Add it to the repository as a read-only deploy key (GitHub: Settings → Deploy keys; GitLab: Settings → Repository → Deploy keys). Not needed for public HTTPS repositories.",
                )}
              />
              <CopyField
                label={t("Webhook address")}
                value={webhook}
                hint={
                  <>
                    {t("Add it as a push webhook (content type application/json). It is secret: anyone with it can start a deploy.")}{" "}
                    {!readOnly && (
                      <button type="button" className="text-primary hover:underline" onClick={() => save.mutate(true)}>
                        {t("Change the address")}
                      </button>
                    )}
                  </>
                }
              />
            </CardBody>
          </Card>
          <Card>
            <CardHeader
              title={t("Deploys")}
              action={
                !readOnly && (
                  <Button size="sm" loading={deploy.isPending} disabled={running} onClick={() => deploy.mutate(undefined)}>
                    <Play /> {t("Deploy now")}
                  </Button>
                )
              }
            />
            <CardBody className="grid gap-3 text-sm">
              <ErrorBox error={deploy.error} />
              {!g.status && <p className="text-muted-foreground">{t("Nothing deployed yet. Add the deploy key to the repository, then press Deploy now.")}</p>}
              {g.status && (
                <div className="flex flex-wrap items-center gap-2">
                  {running ? <RefreshCw className="size-4 animate-spin" /> : <GitBranch className="size-4 text-muted-foreground" />}
                  <Badge tone={statusTone[g.status]}>{g.status === "running" ? t("Deploying…") : g.status === "success" ? t("Deployed") : t("Failed")}</Badge>
                  {g.last_commit && (
                    <span className="font-mono text-xs">
                      {g.last_commit.slice(0, 10)} <span className="font-sans text-muted-foreground">{g.last_subject}</span>
                    </span>
                  )}
                  {g.last_deploy_at && !running && (
                    <span className="text-xs text-muted-foreground">
                      {ago(g.last_deploy_at)} · {g.last_trigger === "webhook" ? t("by the webhook") : t("by hand")}
                    </span>
                  )}
                  {g.pending && <span className="text-xs text-muted-foreground">{t("another deploy follows")}</span>}
                </div>
              )}
              {g.last_error && !running && (
                <p className="flex items-start gap-2 text-xs text-destructive">
                  <TriangleAlert className="mt-0.5 size-4 shrink-0" />
                  <span className="break-words">{tServer(g.last_error)}</span>
                </p>
              )}
              {g.last_output && !running && (
                <details>
                  <summary className="cursor-pointer text-xs text-muted-foreground">{t("Output of the last deploy")}</summary>
                  <pre className="mt-2 max-h-80 overflow-auto rounded-md bg-muted p-3 text-xs whitespace-pre-wrap">{g.last_output}</pre>
                </details>
              )}
            </CardBody>
          </Card>
        </div>
      )}
    </div>
  );
}
