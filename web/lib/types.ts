// Mirrors the JSON shapes of openplexd (internal/db, internal/docker, internal/monitor).

export type Role = "admin" | "reseller" | "customer";

export interface User {
  id: number;
  username: string;
  email: string;
  role: Role;
  parent_id: number | null;
  totp_enabled: boolean;
  disabled: boolean;
  max_domains: number;
  max_databases: number;
  /** "" = follow the browser */
  locale: "" | "en" | "es" | "ca";
  created_at: string;
  updated_at: string;
}

export type DomainKind = "primary" | "subdomain" | "alias";
export type DomainStatus = "pending" | "running" | "stopped" | "error" | "disabled";

export interface Domain {
  id: number;
  owner_id: number;
  name: string;
  kind: DomainKind;
  parent_id: number | null;
  runtime: string;
  runtime_version: string;
  document_root: string;
  start_command: string;
  app_port: number;
  ssl_enabled: boolean;
  force_https: boolean;
  memory_mb: number;
  cpus: number;
  env: Record<string, string>;
  status: DomainStatus;
  status_message: string;
  container_id: string;
  created_at: string;
  updated_at: string;
  /** Set when the website is shared with the caller (absent = full access). */
  access?: { areas: SiteArea[]; read_only: boolean };
}

export type SiteArea = "site" | "files" | "dns" | "mail" | "backups";

export interface AccessGrant {
  id: number;
  user_id: number;
  kind: "site" | "database";
  resource_id: number;
  areas: SiteArea[];
  read_only: boolean;
  created_at: string;
  resource_name: string;
}

/** What the caller may do on a website. */
export function siteAccess(d: Domain) {
  const a = d.access;
  return {
    owner: !a,
    readOnly: !!a?.read_only,
    can: (area: SiteArea) => !a || a.areas.includes(area),
  };
}

export interface Runtime {
  name: string;
  label: string;
  versions: string[] | null;
  default: string;
  needs_command: boolean;
  default_port: number;
  backend: string;
}

export interface Redirect {
  id: number;
  domain_id: number;
  source_path: string;
  target_url: string;
  status_code: number;
  created_at: string;
}

export interface FileEntry {
  name: string;
  path: string;
  is_dir: boolean;
  is_symlink: boolean;
  size: number;
  mode: string;
  mod_time: string;
}

export interface APIKey {
  id: number;
  name: string;
  prefix: string;
  last_used_at: string | null;
  expires_at: string | null;
  created_at: string;
}

export interface SSHKey {
  id: number;
  name: string;
  public_key: string;
  fingerprint: string;
  created_at: string;
}

export interface AuditEntry {
  id: number;
  user_id: number | null;
  action: string;
  target: string;
  detail: string;
  ip: string;
  created_at: string;
}

export interface HostSample {
  time: string;
  cpu_percent: number;
  load1: number;
  load5: number;
  load15: number;
  mem_total: number;
  mem_used: number;
  swap_total: number;
  swap_used: number;
  disk_total: number;
  disk_used: number;
  net_rx_rate: number;
  net_tx_rate: number;
  uptime_secs: number;
}

export interface ContainerStats {
  id: string;
  name: string;
  kind: string;
  domain_id?: number;
  state: string;
  cpu_percent: number;
  mem_usage: number;
  mem_limit: number;
  net_rx: number;
  net_tx: number;
  pids: number;
}

export interface SystemInfo {
  version: string;
  host: {
    hostname: string;
    os: string;
    platform: string;
    platform_version: string;
    kernel: string;
    arch: string;
    cpus: number;
    cpu_model: string;
  };
  docker?: { version: string; api_version: string; os: string; arch: string; kernel: string };
  docker_error?: string;
}

export interface Health {
  status: string;
  version: string;
  docker: boolean;
}

export type Engine = "mariadb" | "postgres" | "redis";

export interface DatabaseEngine {
  name: Engine;
  label: string;
  image: string;
  host: string;
  port: number;
  shared: boolean;
  adminer_driver: string;
}

export interface Database {
  id: number;
  owner_id: number;
  engine: Engine;
  engine_label: string;
  name: string;
  username: string;
  host: string;
  port: number;
  status: "pending" | "running" | "error";
  status_message: string;
  created_at: string;
  /** Owned by another account and shared with the caller. */
  shared?: boolean;
}

export interface DatabaseCredentials {
  host: string;
  port: number;
  database: string;
  username: string;
  password: string;
  url: string;
  adminer: { driver: string; server: string; ticket: string };
}

export interface BackupSettings {
  target: "local" | "s3";
  local_path: string;
  s3_endpoint: string;
  s3_bucket: string;
  s3_prefix: string;
  s3_region: string;
  s3_access_key: string;
  s3_secret_set: boolean;
  schedule_enabled: boolean;
  schedule_time: string;
  keep_daily: number;
  keep_weekly: number;
  keep_monthly: number;
  last_scheduled_at: string | null;
  location: string;
}

export interface BackupRun {
  id: number;
  kind: "backup" | "restore";
  trigger: "manual" | "scheduled";
  scope: string;
  status: "running" | "success" | "error";
  message: string;
  bytes_added: number;
  user_id: number | null;
  started_at: string;
  finished_at: string | null;
}

export interface Snapshot {
  id: string;
  short_id: string;
  time: string;
  tags: string[];
  paths: string[];
  summary?: { total_bytes_processed: number; data_added: number; total_files_processed: number };
}

export type CronStatus = "running" | "success" | "failed" | "timeout" | "skipped";

export interface CronJob {
  id: number;
  domain_id: number;
  name: string;
  schedule: string;
  command: string;
  enabled: boolean;
  timeout_sec: number;
  next_run_at: string | null;
  last_run_at: string | null;
  last_status: CronStatus | "";
  created_at: string;
  updated_at: string;
  running: boolean;
}

export interface CronRun {
  id: number;
  job_id: number;
  trigger: "schedule" | "manual";
  status: CronStatus;
  exit_code: number | null;
  output: string;
  started_at: string;
  finished_at: string | null;
}

export interface CronList {
  jobs: CronJob[];
  timezone: string;
  max_jobs: number;
}

export interface CronPreview {
  next: string[];
  timezone: string;
}

/** Fields accepted by POST/PATCH /domains/{id}/cron. */
export interface CronInput {
  name?: string;
  schedule?: string;
  command?: string;
  enabled?: boolean;
  timeout_sec?: number;
}

// ---------- Firewall & Fail2Ban ----------

export type FirewallProtocol = "tcp" | "udp" | "both";

export interface FirewallRule {
  id?: number;
  protocol: FirewallProtocol;
  /** "22", "8000-8100" or "" (all ports from the source). */
  ports: string;
  /** "" = anywhere, an IP or a CIDR network. */
  source: string;
  comment: string;
}

export interface FirewallBlock {
  id: number;
  address: string;
  comment: string;
  expires_at: string | null;
  created_at: string;
}

export interface FirewallPending {
  enabled: boolean;
  rules: FirewallRule[];
  expires_at: string;
}

export interface FirewallStatus {
  available: boolean;
  unavailable?: string;
  enabled: boolean;
  rules: FirewallRule[];
  pending: FirewallPending | null;
  blocks: FirewallBlock[];
  ssh_ports: number[];
  ufw_active: boolean;
  last_error: string;
  applied_at: string | null;
  your_ip: string;
  confirm_seconds: number;
  max_rules: number;
  panel_port: number;
  panel_direct: boolean;
  via_panel_domain: boolean;
}

export interface Fail2BanJail {
  name: string;
  currently_failed: number;
  total_failed: number;
  currently_banned: number;
  total_banned: number;
  banned: string[];
}

export interface Fail2BanStatus {
  installed: boolean;
  running: boolean;
  jails: Fail2BanJail[];
}

// ---------- DNS ----------

export type DNSType = "A" | "AAAA" | "CNAME" | "MX" | "TXT" | "SRV" | "NS" | "CAA";

export interface DNSRecord {
  id: number;
  zone_id: number;
  domain_id: number | null;
  /** Relative to the zone; "@" is the domain itself. */
  name: string;
  type: DNSType;
  content: string;
  priority: number;
  ttl: number;
}

export interface DNSZone {
  id: number;
  domain_id: number;
  name: string;
  updated_at: string;
}

export interface DNSDelegation {
  status: "ok" | "partial" | "other" | "none";
  servers: string[];
  checked_at: string;
}

export interface DNSZoneDetail {
  zone: DNSZone;
  records: DNSRecord[];
  delegation: DNSDelegation;
  nameservers: string[];
  server_enabled: boolean;
  server_running: boolean;
  types: DNSType[];
  max_records: number;
}

export interface DNSSettings {
  enabled: boolean;
  ns1: string;
  ns2: string;
  ipv4: string;
  ipv6: string;
  hostmaster: string;
}

export interface DNSStatus extends DNSSettings {
  available: boolean;
  running: boolean;
  last_error: string;
  last_sync: string | null;
  listen: string[];
}

export interface DNSOverview {
  status: DNSStatus;
  zones: (DNSZone & { records: number; delegation: DNSDelegation })[];
}

// ---------- Mail ----------

export interface MailSettings {
  enabled: boolean;
  hostname: string;
  webmail: boolean;
}

export interface MailStatus extends MailSettings {
  available: boolean;
  running: boolean;
  health: string;
  last_error: string;
  last_sync: string | null;
  certificate: "letsencrypt" | "self-signed" | "none";
  server_ips: string[];
  ptr: string[];
  ptr_ok: boolean;
  forward: string[];
  webmail_running: boolean;
  webmail_url: string;
}

export type CaptchaProvider = "" | "turnstile" | "hcaptcha" | "recaptcha";

export interface CaptchaSettings {
  provider: CaptchaProvider;
  site_key: string;
  secret_set: boolean;
  panel: boolean;
  webmail: boolean;
  disabled_by_config: boolean;
}

export interface MailOverview {
  status: MailStatus;
  domains: { id: number; domain_id: number; name: string; mailboxes: number; aliases: number; created_at: string }[];
}

export interface Mailbox {
  id: number;
  local_part: string;
  address: string;
  quota_mb: number;
  used_mb: number;
  enabled: boolean;
  created_at: string;
}

export interface MailAlias {
  id: number;
  local_part: string;
  destinations: string[];
}

export interface DomainMail {
  enabled: boolean;
  domain: string;
  hostname: string;
  server_enabled: boolean;
  webmail_url: string;
  max_mailboxes: number;
  mailboxes: Mailbox[];
  aliases: MailAlias[];
}

// ---------- extensions ----------
/** Translatable text from an extension: language → text ("en" always present). */
export type ExtText = Record<string, string>;
export const extText = (t: ExtText | undefined, locale: string) => (t ? (t[locale] ?? t.en ?? "") : "");

export interface ExtensionSetting {
  key: string;
  type: "text" | "secret" | "boolean" | "select";
  label: ExtText;
  help?: ExtText;
  required?: boolean;
  default?: string;
  options?: { value: string; label: ExtText }[];
}

export interface ExtensionPage {
  id: string;
  title: ExtText;
  path: string;
  roles?: Role[];
  area?: SiteArea;
}

export interface ExtensionManifest {
  id: string;
  name: ExtText;
  description?: ExtText;
  version: string;
  publisher: { name: string; url?: string };
  homepage?: string;
  openplex?: string;
  permissions?: string[] | null;
  events?: string[] | null;
  settings?: ExtensionSetting[] | null;
  pages?: ExtensionPage[] | null;
  site_tabs?: ExtensionPage[] | null;
}

export type ExtensionStatus = "starting" | "running" | "disabled" | "error";

export interface Extension {
  id: string;
  version: string;
  source: "upload" | "marketplace";
  signer: string;
  enabled: boolean;
  status: ExtensionStatus;
  status_message: string;
  installed_at: string;
  updated_at: string;
  manifest: ExtensionManifest;
  settings: Record<string, string>;
  secrets_set: string[] | null;
  has_license: boolean;
}

/** A package received for review before installing. */
export interface PendingExtension {
  token: string;
  manifest: ExtensionManifest;
  signer: string;
  source: "upload" | "marketplace";
  installed_version?: string;
}

export interface CatalogEntry {
  id: string;
  version: string;
  name: ExtText;
  description: ExtText;
  publisher: { name: string; url?: string };
  homepage?: string;
  icon?: string;
  permissions?: string[] | null;
  price?: { amount: string; currency: string } | null;
  buy_url?: string;
}

export interface ExtensionUI {
  id: string;
  name: ExtText;
  pages: { id: string; title: ExtText }[];
  site_tabs: { id: string; title: ExtText; area?: SiteArea }[];
}

export interface UpdateRelease {
  version: string;
  name: string;
  notes: string;
  url: string;
  published_at: string;
}

export interface UpdateOutcome {
  from: string;
  to: string;
  result: "updated" | "rolled_back" | "failed";
  message?: string;
  time: string;
}

export interface UpdateStatus {
  current: string;
  enabled: boolean;
  latest: UpdateRelease | null;
  available: boolean;
  can_install: boolean;
  reason?: string;
  checked_at?: string;
  check_error?: string;
  state: "idle" | "installing";
  last?: UpdateOutcome;
}

export interface AppInfo {
  id: string;
  name: string;
  description: string;
  min_php: string;
  admin_path: string;
  languages: string[];
}

export interface SiteApp {
  domain_id: number;
  app: string;
  name: string;
  version: string;
  database_id: number | null;
  database_name?: string;
  admin_user: string;
  admin_email: string;
  admin_url: string;
  language: string;
  status: "installing" | "installed" | "error";
  status_message: string;
  created_at: string;
  updated_at: string;
}

export interface SFTPStatus {
  enabled: boolean;
  port?: number;
  fingerprint?: string;
  username: string;
  password_set: boolean;
  keys: number;
  sites: number;
  ufw_blocked: boolean;
}

export interface PHPOption {
  key: string;
  kind: "size" | "int" | "bool" | "timezone" | "list";
  default: string;
  min?: number;
  max?: number;
}

export interface PHPSettings {
  settings: Record<string, string>;
  options: PHPOption[];
  memory_mb: number;
}
