import { AlertTriangle, CheckCircle2, CircleDashed, Loader2, PauseCircle, XCircle } from "lucide-react";

import { tk, useT } from "@/lib/i18n";
import type { DomainStatus, Role } from "@/lib/types";

import { Badge } from "./ui";

// Status always ships with icon + label, never colour alone.
const map: Record<
  DomainStatus,
  {
    tone: "success" | "warning" | "danger" | "neutral" | "info";
    label: string;
    icon: React.ComponentType<{ className?: string }>;
  }
> = {
  running: { tone: "success", label: tk("Running"), icon: CheckCircle2 },
  pending: { tone: "info", label: tk("Deploying"), icon: Loader2 },
  stopped: { tone: "neutral", label: tk("Stopped"), icon: PauseCircle },
  error: { tone: "danger", label: tk("Error"), icon: XCircle },
  disabled: { tone: "warning", label: tk("Disabled"), icon: AlertTriangle },
};

/** Docker container states; translate with t() at render time. */
export const stateLabels: Record<string, string> = {
  running: tk("Running"),
  exited: tk("Exited"),
  created: tk("Created"),
  restarting: tk("Restarting"),
  paused: tk("Paused"),
  dead: tk("Dead"),
  removing: tk("Removing"),
};

/** Display names for user roles; translate with t() at render time. */
export const roleLabels: Record<Role, string> = {
  admin: tk("Administrator"),
  reseller: tk("Reseller"),
  customer: tk("Customer"),
};

export function StatusBadge({ status, title }: { status: DomainStatus; title?: string }) {
  const { t } = useT();
  const s = map[status] ?? {
    tone: "neutral" as const,
    label: status,
    icon: CircleDashed,
  };
  return (
    <Badge tone={s.tone} title={title}>
      <s.icon className={`size-3 ${status === "pending" ? "animate-spin" : ""}`} />
      {t(s.label)}
    </Badge>
  );
}

export function containerTone(state: string) {
  return state === "running" ? "success" : state === "restarting" ? "warning" : state === "exited" ? "danger" : "neutral";
}
