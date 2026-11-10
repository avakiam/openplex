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
}

export interface DatabaseCredentials {
  host: string;
  port: number;
  database: string;
  username: string;
  password: string;
  url: string;
  adminer: { driver: string; server: string };
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
