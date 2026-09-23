import { td } from "./i18n";

// The row of the role editor each permission sits in, for the ones whose key is not
// the section's own name (the rest are "<section>.view" / "<section>.manage").
const SINGLE: Record<string, string> = {
  "users.delete": "usersDelete",
  "users.export": "usersExport",
  "payments.manage": "payments",
  "broadcasts.manage": "broadcasts",
  "webhooks.manage": "webhooks",
  "api.manage": "api",
  "logs.view": "logs",
  "audit.view": "audit",
  "system.update": "update",
};

// permLabel names a permission the way the role editor shows it: the section, and
// for a view/manage pair which of the two ("Users: read"). An unknown key — one a
// newer build wrote — reads as itself rather than as nothing.
export function permLabel(p: string): string {
  const single = SINGLE[p];
  if (single) return td(`permSection.${single}`);
  const [section, kind] = p.split(".");
  const name = td(`permSection.${section}`);
  if (name === `permSection.${section}`) return p;
  return `${name}: ${td(kind === "view" ? "rolesPanel.colView" : "rolesPanel.colManage")}`;
}
