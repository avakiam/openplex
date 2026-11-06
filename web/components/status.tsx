import { AlertTriangle, CheckCircle2, CircleDashed, Loader2, PauseCircle, XCircle } from "lucide-react";

import type { DomainStatus } from "@/lib/types";

import { Badge } from "./ui";

// Status always ships with icon + label, never colour alone.
const map: Record<DomainStatus, { tone: "success" | "warning" | "danger" | "neutral" | "info"; label: string; icon: React.ComponentType<{ className?: string }> }> = {
  running: { tone: "success", label: "Running", icon: CheckCircle2 },
  pending: { tone: "info", label: "Deploying", icon: Loader2 },
  stopped: { tone: "neutral", label: "Stopped", icon: PauseCircle },
  error: { tone: "danger", label: "Error", icon: XCircle },
  disabled: { tone: "warning", label: "Disabled", icon: AlertTriangle },
};

export function StatusBadge({ status, title }: { status: DomainStatus; title?: string }) {
  const s = map[status] ?? { tone: "neutral" as const, label: status, icon: CircleDashed };
  return (
    <Badge tone={s.tone} title={title}>
      <s.icon className={`size-3 ${status === "pending" ? "animate-spin" : ""}`} />
      {s.label}
    </Badge>
  );
}

export function containerTone(state: string) {
  return state === "running" ? "success" : state === "restarting" ? "warning" : state === "exited" ? "danger" : "neutral";
}
