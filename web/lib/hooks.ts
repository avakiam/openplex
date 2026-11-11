"use client";
// TanStack Query hooks, one per resource. Mutations invalidate the queries they affect.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { api } from "./api";
import { t } from "./i18n";
import type {
  SFTPStatus,
  AppInfo,
  SiteApp,
  UpdateStatus,
  AccessGrant,
  APIKey,
  BackupRun,
  BackupSettings,
  Snapshot,
  Database,
  DatabaseEngine,
  AuditEntry,
  ContainerStats,
  CronInput,
  CronJob,
  CronList,
  CronPreview,
  CronRun,
  DNSOverview,
  DNSZoneDetail,
  Domain,
  DomainMail,
  Fail2BanStatus,
  FileEntry,
  FirewallStatus,
  HostSample,
  CaptchaSettings,
  CatalogEntry,
  Extension,
  ExtensionUI,
  MailOverview,
  Redirect,
  Runtime,
  SSHKey,
  SystemInfo,
  User,
} from "./types";

export const keys = {
  me: ["me"] as const,
  users: ["users"] as const,
  domains: ["domains"] as const,
  domain: (id: number) => ["domains", id] as const,
  redirects: (id: number) => ["domains", id, "redirects"] as const,
  cron: (id: number) => ["domains", id, "cron"] as const,
  cronRuns: (id: number, jid: number) => ["domains", id, "cron", jid, "runs"] as const,
  files: (id: number, path: string) => ["domains", id, "files", path] as const,
  runtimes: ["runtimes"] as const,
  databases: ["databases"] as const,
  dbSizes: ["databases", "sizes"] as const,
  engines: ["database-engines"] as const,
  apiKeys: ["api-keys"] as const,
  backupSettings: ["backups", "settings"] as const,
  backupRuns: ["backups", "runs"] as const,
  itemBackups: (base: string) => ["backups", "item", base] as const,
  userAccess: (id: number) => ["users", id, "access"] as const,
  sshKeys: ["ssh-keys"] as const,
  sftp: ["auth", "me", "sftp"] as const,
  audit: ["audit"] as const,
  info: ["system", "info"] as const,
  stats: ["system", "stats"] as const,
  containers: ["system", "containers"] as const,
  firewall: ["firewall"] as const,
  fail2ban: ["fail2ban"] as const,
  dns: ["dns"] as const,
  zone: (id: number) => ["domains", id, "dns"] as const,
  mail: ["mail"] as const,
  captcha: ["captcha"] as const,
  domainMail: (id: number) => ["domains", id, "mail"] as const,
  extensions: ["extensions"] as const,
  update: ["system", "update"] as const,
  apps: ["apps"] as const,
  siteApp: (id: number) => ["domains", id, "app"] as const,
  extensionsUI: ["extensions", "ui"] as const,
  marketplace: ["extensions", "marketplace"] as const,
};

/** Wraps useMutation with toast feedback and cache invalidation. */
export function useAction<TArgs, TRes = unknown>(
  fn: (a: TArgs) => Promise<TRes>,
  opts: { success?: string; invalidate?: readonly (readonly unknown[])[]; onSuccess?: (r: TRes) => void; onError?: (e: Error) => void } = {},
) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: (r) => {
      if (opts.success) toast.success(opts.success);
      opts.invalidate?.forEach((k) => qc.invalidateQueries({ queryKey: k }));
      opts.onSuccess?.(r);
    },
    onError: (e: Error) => {
      toast.error(e.message);
      opts.onError?.(e);
    },
  });
}

export const useMe = () => useQuery({ queryKey: keys.me, queryFn: () => api.get<User>("/auth/me") });
export const useUsers = () => useQuery({ queryKey: keys.users, queryFn: () => api.get<User[]>("/users") });
export const useRuntimes = () => useQuery({ queryKey: keys.runtimes, queryFn: () => api.get<Runtime[]>("/runtimes"), staleTime: Infinity });

export const useDomains = () =>
  useQuery({
    queryKey: keys.domains,
    queryFn: () => api.get<Domain[]>("/domains"),
    // Poll while anything is being deployed so status badges settle on their own.
    refetchInterval: (q) => (q.state.data?.some((d) => d.status === "pending") ? 2000 : false),
  });

export const useDomain = (id: number) =>
  useQuery({
    queryKey: keys.domain(id),
    queryFn: () => api.get<Domain>(`/domains/${id}`),
    enabled: id > 0,
    refetchInterval: (q) => (q.state.data?.status === "pending" ? 2000 : false),
  });

export const useRedirects = (id: number) => useQuery({ queryKey: keys.redirects(id), queryFn: () => api.get<Redirect[]>(`/domains/${id}/redirects`) });

export const useFiles = (id: number, path: string) =>
  useQuery({
    queryKey: keys.files(id, path),
    queryFn: () => api.get<{ path: string; entries: FileEntry[] }>(`/domains/${id}/files?path=${encodeURIComponent(path)}`),
  });

export const useAPIKeys = () => useQuery({ queryKey: keys.apiKeys, queryFn: () => api.get<APIKey[]>("/api-keys") });
export const useSSHKeys = () => useQuery({ queryKey: keys.sshKeys, queryFn: () => api.get<SSHKey[]>("/ssh-keys") });
export const useAudit = () => useQuery({ queryKey: keys.audit, queryFn: () => api.get<AuditEntry[]>("/audit?limit=200") });

export const useSystemInfo = (enabled = true) => useQuery({ queryKey: keys.info, queryFn: () => api.get<SystemInfo>("/system/info"), enabled });
export const useStatsHistory = (enabled = true) =>
  useQuery({
    queryKey: keys.stats,
    queryFn: () => api.get<{ history: HostSample[] }>("/system/stats"),
    enabled,
  });
export const useContainers = (enabled = true) =>
  useQuery({
    queryKey: keys.containers,
    queryFn: () => api.get<ContainerStats[]>("/system/containers"),
    enabled,
    refetchInterval: 5000,
  });

export const useDomainStats = (id: number, enabled: boolean) =>
  useQuery({
    queryKey: ["domains", id, "stats"],
    queryFn: () => api.get<ContainerStats>(`/domains/${id}/stats`),
    enabled,
    refetchInterval: 5000,
    retry: false,
  });

export const useEngines = () => useQuery({ queryKey: keys.engines, queryFn: () => api.get<DatabaseEngine[]>("/database-engines"), staleTime: Infinity });

export const useUserAccess = (id: number, enabled = true) =>
  useQuery({ queryKey: keys.userAccess(id), queryFn: () => api.get<AccessGrant[]>(`/users/${id}/access`), enabled });
export const useDatabases = () =>
  useQuery({
    queryKey: keys.databases,
    queryFn: () => api.get<Database[]>("/databases"),
    refetchInterval: (q) => (q.state.data?.some((d) => d.status === "pending") ? 2000 : false),
  });

export const useDatabaseSizes = (enabled: boolean) =>
  useQuery({
    queryKey: keys.dbSizes,
    queryFn: () => api.get<Record<string, number>>("/databases/sizes"),
    enabled,
    refetchInterval: 30_000,
  });

export const useBackupSettings = () => useQuery({ queryKey: keys.backupSettings, queryFn: () => api.get<BackupSettings>("/backups/settings"), retry: false });

export const useBackupRuns = () =>
  useQuery({
    queryKey: keys.backupRuns,
    queryFn: () => api.get<BackupRun[]>("/backups/runs?limit=50"),
    refetchInterval: (q) => (q.state.data?.some((r) => r.status === "running") ? 2000 : 15_000),
  });

/** Snapshots + recent runs of one website or database (base: "/domains/3" or "/databases/9"). */
export const useItemBackups = (base: string, enabled = true) =>
  useQuery({
    queryKey: keys.itemBackups(base),
    queryFn: () => api.get<{ snapshots: Snapshot[]; runs: BackupRun[] }>(`${base}/backups`),
    enabled,
    retry: false,
    refetchInterval: (q) => (q.state.data?.runs.some((r) => r.status === "running") ? 2000 : false),
  });

// ---------- Scheduled tasks (cron) ----------

export const useCronJobs = (id: number, enabled = true) =>
  useQuery({
    queryKey: keys.cron(id),
    queryFn: () => api.get<CronList>(`/domains/${id}/cron`),
    enabled: enabled && id > 0,
    // Poll quickly while a task runs so its status settles; otherwise keep "next run" fresh.
    refetchInterval: (q) => (q.state.data?.jobs.some((j) => j.running) ? 5000 : 30_000),
  });

export const useCronRuns = (id: number, jid: number, enabled = true) =>
  useQuery({
    queryKey: keys.cronRuns(id, jid),
    queryFn: () => api.get<CronRun[]>(`/domains/${id}/cron/${jid}/runs`),
    enabled: enabled && jid > 0,
    refetchInterval: (q) => (q.state.data?.[0]?.status === "running" ? 3000 : false),
  });

/** Next run times of a schedule expression (UTC); errors carry the API's message. */
export const previewCron = (schedule: string) => api.post<CronPreview>("/cron/preview", { schedule });

export const useCronPreview = (schedule: string) =>
  useQuery({
    queryKey: ["cron-preview", schedule],
    queryFn: () => previewCron(schedule),
    enabled: schedule.trim() !== "",
    retry: false,
    staleTime: 30_000,
  });

export const useCreateCron = (id: number, onSuccess?: (j: CronJob) => void) =>
  useAction((body: CronInput) => api.post<CronJob>(`/domains/${id}/cron`, body), {
    success: t("Task created"),
    invalidate: [keys.cron(id)],
    onSuccess,
  });

export const useUpdateCron = (id: number, onSuccess?: (j: CronJob) => void) =>
  useAction(({ jid, ...body }: CronInput & { jid: number }) => api.patch<CronJob>(`/domains/${id}/cron/${jid}`, body), {
    invalidate: [keys.cron(id)],
    onSuccess,
  });

export const useDeleteCron = (id: number, onSuccess?: () => void) =>
  useAction((jid: number) => api.del(`/domains/${id}/cron/${jid}`), {
    success: t("Task deleted"),
    invalidate: [keys.cron(id)],
    onSuccess,
  });

/** Starts a task now; 409 "busy" surfaces as a toast with the server's message. Invalidating the list also refreshes its run history. */
export const useRunCron = (id: number) =>
  useAction((jid: number) => api.post(`/domains/${id}/cron/${jid}/run`), {
    success: t("Task started"),
    invalidate: [keys.cron(id)],
  });

// ---------- Firewall & Fail2Ban ----------

export const useFirewall = () =>
  useQuery({
    queryKey: keys.firewall,
    queryFn: () => api.get<FirewallStatus>("/firewall"),
    // While changes wait for confirmation, poll so a rollback shows up.
    refetchInterval: (q) => (q.state.data?.pending ? 3000 : 30_000),
  });

export const useFail2Ban = () => useQuery({ queryKey: keys.fail2ban, queryFn: () => api.get<Fail2BanStatus>("/fail2ban"), refetchInterval: 15_000 });

// ---------- DNS ----------

export const useDNS = () => useQuery({ queryKey: keys.dns, queryFn: () => api.get<DNSOverview>("/dns"), refetchInterval: 30_000 });

export const useZone = (id: number, enabled = true) =>
  useQuery({ queryKey: keys.zone(id), queryFn: () => api.get<DNSZoneDetail>(`/domains/${id}/dns`), enabled: enabled && id > 0, retry: false });

// ---------- Mail ----------

export const useCaptcha = () => useQuery({ queryKey: keys.captcha, queryFn: () => api.get<CaptchaSettings>("/captcha") });
export const useMail = () => useQuery({ queryKey: keys.mail, queryFn: () => api.get<MailOverview>("/mail"), refetchInterval: 30_000 });

export const useDomainMail = (id: number, enabled = true) =>
  useQuery({ queryKey: keys.domainMail(id), queryFn: () => api.get<DomainMail>(`/domains/${id}/mail`), enabled: enabled && id > 0, retry: false });

export const useExtensions = () =>
  useQuery({
    queryKey: keys.extensions,
    queryFn: () => api.get<Extension[]>("/extensions"),
    refetchInterval: (q) => (q.state.data?.some((e) => e.status === "starting") ? 2000 : false),
  });
export const useExtensionsUI = (enabled = true) =>
  useQuery({ queryKey: keys.extensionsUI, queryFn: () => api.get<ExtensionUI[]>("/extensions/ui"), staleTime: 60_000, enabled });
export const useMarketplace = (enabled: boolean) =>
  useQuery({ queryKey: keys.marketplace, queryFn: () => api.get<CatalogEntry[]>("/extensions/marketplace"), enabled, retry: false, staleTime: 300_000 });

/** Panel update status; polled every few seconds while an update installs
 * (the panel restarts meanwhile, so failed requests are retried). */
export const useUpdate = (enabled: boolean, installing = false) =>
  useQuery({
    queryKey: keys.update,
    queryFn: () => api.get<UpdateStatus>("/system/update"),
    enabled,
    staleTime: 300_000,
    refetchInterval: installing ? 3000 : false,
    retry: installing ? true : 1,
    retryDelay: 3000,
  });

export const useApps = () => useQuery({ queryKey: keys.apps, queryFn: () => api.get<AppInfo[]>("/apps"), staleTime: Infinity });

/** The application installed on a website (null if none); polled while it installs. */
export const useSiteApp = (id: number) =>
  useQuery({
    queryKey: keys.siteApp(id),
    queryFn: () => api.get<SiteApp | null>(`/domains/${id}/app`),
    refetchInterval: (q) => (q.state.data?.status === "installing" ? 3000 : false),
  });

export const useSFTP = () => useQuery({ queryKey: keys.sftp, queryFn: () => api.get<SFTPStatus>("/auth/me/sftp") });
