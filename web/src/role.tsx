import { createContext, type ReactNode, useContext, useMemo } from "react";
import type { Perm, Role } from "./api";

// The signed-in admin's role and what it grants, published once at the top so the
// panel can leave out what the role can't use, without threading it through every
// component.
//
// Hiding is cosmetic. Every route behind these controls is enforced on the server
// (see requirePerm in internal/server/panel.go), so a hand-crafted request is refused
// even though the button was never rendered. If the two ever disagree, the server
// wins — the UI just looks wrong, it doesn't leak.
type Access = { role: Role; perms: ReadonlySet<Perm> };

// The default is the least privileged: a UI that shows too little is a nuisance, one
// that shows too much is a bug report.
const RoleCtx = createContext<Access>({ role: "", perms: new Set() });

export function RoleProvider({
  role,
  perms,
  children,
}: {
  role: Role;
  perms: Perm[];
  children: ReactNode;
}) {
  // Memoised on the list's contents: a fresh object every render would re-render
  // every useCan() consumer in the panel whenever App re-renders.
  const key = perms.join(",");
  // biome-ignore lint/correctness/useExhaustiveDependencies: keyed on the list's contents, not its identity — App hands a new array on every /api/me
  const value = useMemo(() => ({ role, perms: new Set(perms) }), [role, key]);
  return <RoleCtx.Provider value={value}>{children}</RoleCtx.Provider>;
}

export const useRole = () => useContext(RoleCtx).role;

// The roster and the roles are the owner's alone — no permission reaches them.
export const useIsOwner = () => useRole() === "owner";

// useCan reports whether the admin holds at least one of perms — the same "any of"
// the server's route check applies.
export function useCan(...perms: Perm[]): boolean {
  const { perms: held } = useContext(RoleCtx);
  return perms.some((p) => held.has(p));
}

// usePerms hands back the whole set, for a component that decides several things.
export const usePerms = () => useContext(RoleCtx).perms;
