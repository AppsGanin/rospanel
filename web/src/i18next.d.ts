// Types t() against the Russian dictionary: an unknown key is a tsc error rather
// than a raw "users.titel" rendered into the page.
import "i18next";
import type { Dict } from "./i18n/ru";

declare module "i18next" {
  interface CustomTypeOptions {
    defaultNS: "translation";
    resources: { translation: Dict };
  }
}
