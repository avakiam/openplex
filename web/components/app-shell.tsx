"use client";
import {
  Activity,
  Archive,
  Database,
  Globe,
  KeyRound,
  LayoutDashboard,
  LogOut,
  Menu,
  Moon,
  ScrollText,
  Sun,
  UserCog,
  Users,
} from "lucide-react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useEffect, useState } from "react";

import { logout } from "@/lib/api";
import { canManageUsers, isAdmin, useAuth } from "@/lib/auth";
import { tk, useT } from "@/lib/i18n";
import type { User } from "@/lib/types";
import { cn } from "@/lib/utils";

import { LanguageSelect } from "./language-select";
import { roleLabels } from "./status";
import { Badge, Dropdown, DropdownContent, DropdownItem, DropdownSeparator, DropdownTrigger, Spinner } from "./ui";

interface NavItem {
  href: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
  show?: (u: User | null) => boolean;
}

const nav: NavItem[] = [
  { href: "/", label: tk("Dashboard"), icon: LayoutDashboard },
  { href: "/domains/", label: tk("Websites & Domains"), icon: Globe },
  { href: "/databases/", label: tk("Databases"), icon: Database },
  { href: "/users/", label: tk("Users"), icon: Users, show: canManageUsers },
  { href: "/keys/", label: tk("API & SSH keys"), icon: KeyRound },
  { href: "/backups/", label: tk("Backups"), icon: Archive, show: isAdmin },
  { href: "/audit/", label: tk("Audit log"), icon: ScrollText, show: isAdmin },
  { href: "/account/", label: tk("Account & security"), icon: UserCog },
];

export function Logo({ className }: { className?: string }) {
  return (
    <span className={cn("flex items-center gap-2 font-semibold tracking-tight", className)}>
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img src="/favicon.svg" alt="" className="size-7" />
      OpenPlex
    </span>
  );
}

function useTheme() {
  const [dark, setDark] = useState(false);
  useEffect(() => setDark(document.documentElement.classList.contains("dark")), []);
  const toggle = () => {
    const next = !dark;
    document.documentElement.classList.toggle("dark", next);
    try {
      localStorage.setItem("opx-theme", next ? "dark" : "light");
    } catch {
      /* storage unavailable */
    }
    setDark(next);
  };
  return { dark, toggle };
}

export function AppShell({ children }: { children: React.ReactNode }) {
  const { user, ready } = useAuth();
  const { t } = useT();
  const router = useRouter();
  const pathname = usePathname();
  const [open, setOpen] = useState(false);
  const { dark, toggle } = useTheme();

  useEffect(() => {
    if (ready && !user) router.replace(`/login/?next=${encodeURIComponent(pathname)}`);
  }, [ready, user, router, pathname]);
  useEffect(() => setOpen(false), [pathname]);

  if (!ready || !user) {
    return (
      <div className="flex h-dvh items-center justify-center">
        <Spinner />
      </div>
    );
  }

  const isActive = (href: string) => (href === "/" ? pathname === "/" : pathname.startsWith(href));

  return (
    <div className="flex min-h-dvh">
      <aside
        className={cn(
          "fixed inset-y-0 left-0 z-40 flex w-64 flex-col border-r bg-card transition-transform lg:translate-x-0",
          open ? "translate-x-0" : "-translate-x-full",
        )}
      >
        <div className="flex h-14 items-center border-b px-5">
          <Logo />
        </div>
        <nav className="flex-1 space-y-0.5 overflow-y-auto p-3">
          {nav
            .filter((n) => !n.show || n.show(user))
            .map((n) => (
              <Link
                key={n.href}
                href={n.href}
                className={cn(
                  "flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium text-muted-foreground hover:bg-muted hover:text-foreground",
                  isActive(n.href) && "bg-accent text-accent-foreground hover:bg-accent hover:text-accent-foreground",
                )}
              >
                <n.icon className="size-4" />
                {t(n.label)}
              </Link>
            ))}
        </nav>
        <div className="flex items-center gap-2 border-t px-5 py-3 text-xs text-muted-foreground">
          <Activity className="size-3.5" /> openplex.dev
        </div>
      </aside>
      {open && <div className="fixed inset-0 z-30 bg-black/40 lg:hidden" onClick={() => setOpen(false)} />}

      <div className="flex min-w-0 flex-1 flex-col lg:pl-64">
        <header className="sticky top-0 z-20 flex h-14 items-center gap-3 border-b bg-card/90 px-4 backdrop-blur sm:px-6">
          <button className="rounded p-1.5 hover:bg-muted lg:hidden" onClick={() => setOpen(true)} aria-label={t("Open menu")}>
            <Menu className="size-5" />
          </button>
          <div className="lg:hidden">
            <Logo />
          </div>
          <div className="ml-auto flex items-center gap-1">
            <LanguageSelect />
            <button
              onClick={toggle}
              className="rounded-md p-2 text-muted-foreground hover:bg-muted hover:text-foreground"
              aria-label={dark ? t("Switch to light theme") : t("Switch to dark theme")}
            >
              {dark ? <Sun className="size-4" /> : <Moon className="size-4" />}
            </button>
            <Dropdown>
              <DropdownTrigger className="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted">
                <span className="flex size-7 items-center justify-center rounded-full bg-accent text-xs font-semibold uppercase text-accent-foreground">
                  {user.username.slice(0, 2)}
                </span>
                <span className="hidden font-medium sm:inline">{user.username}</span>
                <Badge className="hidden sm:inline-flex">{t(roleLabels[user.role])}</Badge>
              </DropdownTrigger>
              <DropdownContent>
                <DropdownItem onSelect={() => router.push("/account/")}>
                  <UserCog /> {t("Account & security")}
                </DropdownItem>
                <DropdownSeparator />
                <DropdownItem
                  destructive
                  onSelect={async () => {
                    await logout();
                    router.replace("/login/");
                  }}
                >
                  <LogOut /> {t("Sign out")}
                </DropdownItem>
              </DropdownContent>
            </Dropdown>
          </div>
        </header>
        <main className="mx-auto w-full max-w-7xl flex-1 px-4 py-6 sm:px-6 lg:py-8">{children}</main>
      </div>
    </div>
  );
}
