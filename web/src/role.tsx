import { createContext, type ReactNode, useContext } from "react";
import type { Role } from "./api";

// The signed-in admin's role, published once at the top so the panel can leave out
// what a role can't use, without threading it through every component.
//
// Hiding is cosmetic. Every route behind these controls is enforced on the server
// (see requireRole in internal/server/panel.go), so a hand-crafted request from an
// operator is refused even though the button was never rendered. If the two ever
// disagree, the server wins — the UI just looks wrong, it doesn't leak.
const RoleCtx = createContext<Role>("operator");

export function RoleProvider({
  role,
  children,
}: {
  role: Role;
  children: ReactNode;
}) {
  return <RoleCtx.Provider value={role}>{children}</RoleCtx.Provider>;
}

export const useRole = () => useContext(RoleCtx);

// The roster is the owner's alone.
export const useIsOwner = () => useRole() === "owner";

// Settings, backups, the API surface, Xray itself: admin and up. The default when
// the role is anything unexpected is the least privileged one — a UI that shows too
// little is a nuisance, one that shows too much is a bug report.
export function useIsAdmin() {
  const role = useRole();
  return role === "owner" || role === "admin";
}
