"use client";
import { CircleCheck, Info, ShieldAlert, ShieldCheck, TriangleAlert } from "lucide-react";
import { useState } from "react";

import { Button, ErrorBox, Spinner } from "@/components/ui";
import { api } from "@/lib/api";
import { keys, useAction } from "@/lib/hooks";
import { tk, useT } from "@/lib/i18n";
import type { SecurityCheck, SiteApp } from "@/lib/types";

/** What each failed check means; shown only when a check fails. */
const checkTexts: Record<string, { title: string; hint: string }> = {
  wp_file_edit: {
    title: tk("The theme and plugin editor is on"),
    hint: tk("Anyone who gets into the administration can change the site's PHP code from the browser."),
  },
  wp_debug: { title: tk("Debug mode is on"), hint: tk("Errors and internal details may be shown to visitors.") },
  wp_xmlrpc: {
    title: tk("XML-RPC is on"),
    hint: tk("A common target of password-guessing attacks. Few sites need it (Jetpack and some mobile apps do)."),
  },
  wp_config_perms: { title: tk("Other users of the server can read wp-config.php"), hint: tk("It holds the database password.") },
  wp_admin_user: {
    title: tk("An administrator is called “admin”"),
    hint: tk("It is the first name attackers try: create another administrator and delete this one."),
  },
  wp_inactive_plugins: { title: tk("Inactive plugins are installed"), hint: tk("Delete the plugins you don't use: they can still have vulnerabilities.") },
  joomla_debug: { title: tk("Debug mode is on"), hint: tk("Errors and internal details may be shown to visitors.") },
  joomla_error_reporting: { title: tk("PHP errors are shown to visitors"), hint: tk("Error messages reveal paths and internal details.") },
  joomla_force_ssl: { title: tk("Joomla does not force HTTPS"), hint: tk("The site and its administration can still be used over plain HTTP.") },
  joomla_installation_dir: { title: tk("The installation folder is still there"), hint: tk("It could be used to reinstall the site.") },
  joomla_config_perms: { title: tk("Other users of the server can read configuration.php"), hint: tk("It holds the database password.") },
  joomla_admin_user: {
    title: tk("A user is called “admin”"),
    hint: tk("It is the first name attackers try: create another Super User and delete this one."),
  },
  drupal_trusted_hosts: { title: tk("Trusted host names are not set"), hint: tk("Drupal accepts any host name, which allows host header attacks.") },
  drupal_update_free_access: { title: tk("update.php is open to anyone"), hint: tk("Anyone could run the database updates.") },
  drupal_error_level: { title: tk("PHP errors are shown to visitors"), hint: tk("Error messages reveal paths and internal details.") },
  drupal_settings_perms: { title: tk("settings.php can be changed"), hint: tk("Drupal recommends making it and its folder read-only.") },
  drupal_admin_user: {
    title: tk("The main administrator is called “admin”"),
    hint: tk("It is the first name attackers try: rename it under People."),
  },
  drupal_advisories: { title: tk("Packages with known vulnerabilities"), hint: tk("Update them under Updates.") },
  site_https: { title: tk("HTTPS is not forced"), hint: tk("Turn on HTTPS and the redirect from HTTP in the website's Settings.") },
  site_php_eol: { title: tk("This PHP version no longer gets security fixes"), hint: tk("Choose a newer PHP version in the website's Settings.") },
  site_display_errors: { title: tk("PHP shows errors to visitors"), hint: tk("Turn off “Show errors on the pages” in the website's PHP settings.") },
};

const severityIcon = {
  critical: <ShieldAlert className="mt-0.5 size-4 shrink-0 text-destructive" />,
  warning: <TriangleAlert className="mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-400" />,
  info: <Info className="mt-0.5 size-4 shrink-0 text-muted-foreground" />,
};
const severityOrder = { critical: 0, warning: 1, info: 2 };

/** Security checks of an installed application, with one-click fixes. */
export function AppSecuritySection({ app, canManage }: { app: SiteApp; canManage: boolean }) {
  const { t } = useT();
  const [chosen, setChosen] = useState<string[] | null>(null);
  const checks: SecurityCheck[] = [...(app.updates?.security ?? []), ...app.site_checks];
  const failed = checks.filter((c) => !c.ok).sort((a, b) => severityOrder[a.severity] - severityOrder[b.severity]);
  const fixable = failed.filter((c) => c.fixable).map((c) => c.id);
  const selected = chosen ?? fixable;
  const harden = useAction(() => api.post(`/domains/${app.domain_id}/app/harden`, { fixes: selected }), {
    success: t("Applying the fixes"),
    invalidate: [keys.siteApp(app.domain_id)],
    onSuccess: () => setChosen(null),
  });
  const appChecked = !!app.updates?.security;

  return (
    <div className="grid gap-2 border-t pt-3">
      <p className="font-medium">{t("Security")}</p>
      {app.task === "securing" && (
        <p className="flex items-center gap-2">
          <Spinner className="size-4" /> {t("Applying the fixes…")}
        </p>
      )}
      {!appChecked && !app.task && <p className="text-xs text-muted-foreground">{t("The application's checks run with the update check.")}</p>}
      {failed.length === 0 && appChecked && (
        <p className="flex items-center gap-2 text-emerald-600 dark:text-emerald-400">
          <ShieldCheck className="size-4" /> {t("No problems found.")}
        </p>
      )}
      {failed.length > 0 && (
        <ul className="grid gap-2 text-xs">
          {failed.map((c) => {
            const text = checkTexts[c.id];
            return (
              <li key={c.id} className="flex items-start gap-2">
                {severityIcon[c.severity]}
                <div className="min-w-0 flex-1">
                  <p className="text-sm">{text ? t(text.title) : c.id}</p>
                  {text && <p className="text-muted-foreground">{t(text.hint)}</p>}
                  {c.detail && <p className="break-words font-mono text-muted-foreground">{c.detail}</p>}
                </div>
                {c.fixable && canManage && !app.task && (
                  <label className="flex shrink-0 items-center gap-1 text-muted-foreground">
                    <input
                      type="checkbox"
                      checked={selected.includes(c.id)}
                      onChange={(e) => setChosen(e.target.checked ? [...selected, c.id] : selected.filter((x) => x !== c.id))}
                    />
                    {t("Fix")}
                  </label>
                )}
              </li>
            );
          })}
        </ul>
      )}
      {checks.some((c) => c.ok) && (
        <p className="flex items-center gap-1 text-xs text-muted-foreground">
          <CircleCheck className="size-3.5" /> {t("{count} checks passed", { count: checks.filter((c) => c.ok).length })}
        </p>
      )}
      <ErrorBox error={harden.error} />
      {canManage && fixable.length > 0 && !app.task && (
        <Button variant="outline" loading={harden.isPending} disabled={selected.length === 0} onClick={() => harden.mutate(undefined)}>
          <ShieldCheck /> {t("Fix the selected problems")}
        </Button>
      )}
    </div>
  );
}
