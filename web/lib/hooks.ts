"use client";
// TanStack Query hooks, one per resource. Mutations invalidate the queries they affect.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { api } from "./api";
import type {
  APIKey,
  AuditEntry,
  ContainerStats,
  Domain,
  FileEntry,
  HostSample,
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
  files: (id: number, path: string) => ["domains", id, "files", path] as const,
  runtimes: ["runtimes"] as const,
  apiKeys: ["api-keys"] as const,
  sshKeys: ["ssh-keys"] as const,
  audit: ["audit"] as const,
  info: ["system", "info"] as const,
  stats: ["system", "stats"] as const,
  containers: ["system", "containers"] as const,
};

/** Wraps useMutation with toast feedback and cache invalidation. */
export function useAction<TArgs, TRes = unknown>(
  fn: (a: TArgs) => Promise<TRes>,
  opts: { success?: string; invalidate?: readonly (readonly unknown[])[]; onSuccess?: (r: TRes) => void } = {},
) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: (r) => {
      if (opts.success) toast.success(opts.success);
      opts.invalidate?.forEach((k) => qc.invalidateQueries({ queryKey: k }));
      opts.onSuccess?.(r);
    },
    onError: (e: Error) => toast.error(e.message),
  });
}

export const useMe = () => useQuery({ queryKey: keys.me, queryFn: () => api.get<User>("/auth/me") });
export const useUsers = () => useQuery({ queryKey: keys.users, queryFn: () => api.get<User[]>("/users") });
export const useRuntimes = () =>
  useQuery({ queryKey: keys.runtimes, queryFn: () => api.get<Runtime[]>("/runtimes"), staleTime: Infinity });

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

export const useRedirects = (id: number) =>
  useQuery({ queryKey: keys.redirects(id), queryFn: () => api.get<Redirect[]>(`/domains/${id}/redirects`) });

export const useFiles = (id: number, path: string) =>
  useQuery({
    queryKey: keys.files(id, path),
    queryFn: () =>
      api.get<{ path: string; entries: FileEntry[] }>(`/domains/${id}/files?path=${encodeURIComponent(path)}`),
  });

export const useAPIKeys = () => useQuery({ queryKey: keys.apiKeys, queryFn: () => api.get<APIKey[]>("/api-keys") });
export const useSSHKeys = () => useQuery({ queryKey: keys.sshKeys, queryFn: () => api.get<SSHKey[]>("/ssh-keys") });
export const useAudit = () =>
  useQuery({ queryKey: keys.audit, queryFn: () => api.get<AuditEntry[]>("/audit?limit=200") });

export const useSystemInfo = (enabled = true) =>
  useQuery({ queryKey: keys.info, queryFn: () => api.get<SystemInfo>("/system/info"), enabled });
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
