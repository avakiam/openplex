"use client";
import { createContext, useContext, useEffect, useState, type ReactNode } from "react";

import { currentUser, onSessionChange, refresh } from "./api";
import type { User } from "./types";

interface AuthState {
  user: User | null;
  ready: boolean; // initial refresh attempt finished
}

const AuthContext = createContext<AuthState>({ user: null, ready: false });

export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState>({ user: currentUser(), ready: false });
  useEffect(() => {
    const off = onSessionChange((user) => setState((s) => ({ ...s, user })));
    refresh().finally(() => setState({ user: currentUser(), ready: true }));
    return () => {
      off();
    };
  }, []);
  return <AuthContext.Provider value={state}>{children}</AuthContext.Provider>;
}

export const useAuth = () => useContext(AuthContext);

export const isAdmin = (u: User | null) => u?.role === "admin";
export const canManageUsers = (u: User | null) => u?.role === "admin" || u?.role === "reseller";
