"use client";
import { ArrowUpCircle, CheckCircle2, ExternalLink, RefreshCw, TriangleAlert } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";

import { Badge, Button, Card, CardBody, Dialog, DialogContent, ErrorBox, PageHeader, Spinner, Switch } from "@/components/ui";
import { api } from "@/lib/api";
import { keys, useAction, useUpdate } from "@/lib/hooks";
import { useT } from "@/lib/i18n";
import type { UpdateStatus } from "@/lib/types";
import { ago, dateTime } from "@/lib/utils";

export default function UpdatesPage() {
  const { t } = useT();
  const [installing, setInstalling] = useState(false);
  const { data, isLoading, error } = useUpdate(true, installing);
  const startVersion = useRef<string | null>(null);
  const [confirm, setConfirm] = useState(false);

  const check = useAction(() => api.post<UpdateStatus>("/system/update/check"), { invalidate: [keys.update] });
  const settings = useAction((on: boolean) => api.put<UpdateStatus>("/system/update/settings", { check_enabled: on }), {
    invalidate: [keys.update],
    success: t("Saved"),
  });
  const install = useAction((version: string) => api.post<UpdateStatus>("/system/update/install", { version }), {
    invalidate: [keys.update],
    onSuccess: () => {
      setConfirm(false);
      startVersion.current = data?.current ?? null;
      setInstalling(true);
    },
  });

  // While installing, the panel restarts: keep polling until it answers with
  // the new version (or the update ends), then load the new dashboard.
  useEffect(() => {
    if (!data) return;
    if (data.state === "installing" && !installing) {
      startVersion.current = data.current;
      setInstalling(true);
    }
    if (installing && data.state === "idle") {
      setInstalling(false);
      if (startVersion.current && data.current !== startVersion.current) {
        toast.success(t("OpenPlex was updated to {version}.", { version: data.current }));
        setTimeout(() => window.location.reload(), 1500);
      }
    }
  }, [data, installing, t]);

  if (isLoading) {
    return (
      <div className="flex justify-center py-20">
        <Spinner />
      </div>
    );
  }
  if (error && !installing) return <ErrorBox error={error} />;
  if (!data) return null;
  const latest = data.latest;

  return (
    <>
      <PageHeader title={t("Updates")} description={t("Keep OpenPlex up to date with its stable releases.")} />
      <div className="space-y-4">
        {data.last && <LastOutcome status={data} />}

        <Card>
          <CardBody className="space-y-4">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div>
                <p className="text-sm text-muted-foreground">{t("Installed version")}</p>
                <p className="text-2xl font-semibold tracking-tight">{data.current}</p>
              </div>
              {installing || data.state === "installing" ? (
                <Badge tone="info">
                  <Spinner className="size-3" /> {t("Installing…")}
                </Badge>
              ) : data.available ? (
                <Badge tone="warning">
                  <ArrowUpCircle className="size-3.5" /> {t("Update available")}
                </Badge>
              ) : latest ? (
                <Badge tone="success">
                  <CheckCircle2 className="size-3.5" /> {t("Up to date")}
                </Badge>
              ) : null}
            </div>

            {(installing || data.state === "installing") && (
              <div className="flex items-start gap-3 rounded-md border bg-muted/40 p-3 text-sm">
                <Spinner className="mt-0.5 size-4" />
                <p>
                  {t(
                    "Installing OpenPlex {version}. The panel restarts and will be back in about a minute; if the new version doesn't start, the previous one is restored automatically.",
                    { version: latest?.version ?? "" },
                  )}
                </p>
              </div>
            )}

            {data.available && latest && !installing && data.state !== "installing" && (
              <div className="space-y-3 rounded-md border p-4">
                <div className="flex flex-wrap items-baseline justify-between gap-2">
                  <p className="font-medium">{latest.name || `OpenPlex ${latest.version}`}</p>
                  <p className="text-xs text-muted-foreground">
                    {t("Published {date}", { date: dateTime(latest.published_at) })}
                    {latest.url && (
                      <>
                        {" · "}
                        <a href={latest.url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 hover:underline">
                          {t("Release page")} <ExternalLink className="size-3" />
                        </a>
                      </>
                    )}
                  </p>
                </div>
                {latest.notes && (
                  <div className="max-h-72 overflow-y-auto whitespace-pre-wrap rounded bg-muted/40 p-3 text-sm">{latest.notes}</div>
                )}
                {data.can_install ? (
                  <Button onClick={() => setConfirm(true)}>
                    <ArrowUpCircle className="size-4" /> {t("Update to {version}", { version: latest.version })}
                  </Button>
                ) : (
                  <p className="text-sm text-muted-foreground">{t(data.reason ?? "")}</p>
                )}
              </div>
            )}

            <div className="flex flex-wrap items-center justify-between gap-2 border-t pt-3 text-sm text-muted-foreground">
              <span>
                {data.checked_at ? t("Last checked {when}", { when: ago(data.checked_at) }) : t("Not checked yet")}
                {data.check_error && <span className="text-destructive"> · {data.check_error}</span>}
                {data.checked_at && !latest && !data.check_error && <> · {t("No release has been published yet.")}</>}
              </span>
              <Button variant="outline" size="sm" loading={check.isPending} disabled={installing} onClick={() => check.mutate(undefined)}>
                <RefreshCw className="size-4" /> {t("Check now")}
              </Button>
            </div>
          </CardBody>
        </Card>

        <Card>
          <CardBody className="flex items-start justify-between gap-4">
            <div>
              <p className="font-medium">{t("Check for updates automatically")}</p>
              <p className="text-sm text-muted-foreground">
                {t("Twice a day. Nothing is installed until an administrator clicks Update; releases must be signed by OpenPlex.")}
              </p>
            </div>
            <Switch
              checked={data.enabled}
              disabled={settings.isPending}
              onCheckedChange={(v) => settings.mutate(v)}
              aria-label={t("Check for updates automatically")}
            />
          </CardBody>
        </Card>
      </div>

      <Dialog open={confirm} onOpenChange={setConfirm}>
        <DialogContent
          title={t("Update to {version}?", { version: latest?.version ?? "" })}
          description={t(
            "The panel stops for about a minute while the new version is installed; websites, databases and mail keep running. The current version and its database are backed up first and restored automatically if the update fails.",
          )}
        >
          <div className="mt-6 flex justify-end gap-2">
            <Button variant="outline" onClick={() => setConfirm(false)}>
              {t("Cancel")}
            </Button>
            <Button loading={install.isPending} onClick={() => latest && install.mutate(latest.version)}>
              {t("Update")}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}

function LastOutcome({ status }: { status: UpdateStatus }) {
  const { t } = useT();
  const o = status.last!;
  // Only recent outcomes are worth a banner.
  if (Date.now() - Date.parse(o.time) > 7 * 86400_000) return null;
  if (o.result === "updated") {
    if (o.to !== status.current) return null;
    return (
      <div className="flex items-start gap-3 rounded-md border border-success/30 bg-success/10 p-3 text-sm">
        <CheckCircle2 className="mt-0.5 size-4 text-success" />
        <p>{t("Updated from {from} to {to} {when}.", { from: o.from, to: o.to, when: ago(o.time) })}</p>
      </div>
    );
  }
  return (
    <div className="flex items-start gap-3 rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm">
      <TriangleAlert className="mt-0.5 size-4 text-destructive" />
      <div>
        <p className="font-medium">
          {o.result === "rolled_back"
            ? t("The update to {to} was undone {when}.", { to: o.to, when: ago(o.time) })
            : t("The update to {to} failed {when}.", { to: o.to, when: ago(o.time) })}
        </p>
        {o.message && <p className="text-muted-foreground">{o.message}</p>}
      </div>
    </div>
  );
}
