// Panel localisation. Two languages ship today; adding a third means adding a
// dictionary next to ru.ts/en.ts and one LANGS entry — nothing else.
//
// The dictionaries are typed against each other (see en.ts): a key present in ru
// and missing in en is a tsc error, not a string that silently renders in the
// wrong language at runtime.
import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import en from "./en";
import ru from "./ru";

export const LANGS = [
  { code: "ru", label: "Русский" },
  { code: "en", label: "English" },
] as const;

export type Lang = (typeof LANGS)[number]["code"];

const STORAGE_KEY = "rospanel.lang";

function isLang(v: string | null): v is Lang {
  return v === "ru" || v === "en";
}

// storedLang is the admin's explicit pick, which always beats detection — someone
// who switched to English on a ru-RU laptop meant it.
function storedLang(): Lang | null {
  try {
    const v = localStorage.getItem(STORAGE_KEY);
    return isLang(v) ? v : null;
  } catch {
    return null; // private mode / storage disabled — fall through to detection
  }
}

// detectLang reads the browser's preference list: Russian speakers get Russian,
// everyone else gets English. Note this is not i18next's fallbackLng, which is a
// different question (what to render when a KEY is missing) and stays "ru" — ru is
// the dictionary the code is written against, so it is the one that is never behind.
export function detectLang(): Lang {
  const stored = storedLang();
  if (stored) return stored;
  const list =
    typeof navigator !== "undefined"
      ? (navigator.languages ?? [navigator.language])
      : [];
  for (const raw of list) {
    const tag = (raw || "").toLowerCase();
    if (tag.startsWith("ru")) return "ru";
    if (tag.startsWith("en")) return "en";
  }
  return "en";
}

export function setLang(lang: Lang) {
  try {
    localStorage.setItem(STORAGE_KEY, lang);
  } catch {
    /* storage disabled — the change still applies for this session */
  }
  i18n.changeLanguage(lang);
  document.documentElement.lang = lang;
}

export function currentLang(): Lang {
  return isLang(i18n.language) ? i18n.language : "ru";
}

// td translates a key assembled at runtime — an event action, a provider id, a
// server status. Typed t() cannot check those (the key does not exist as a literal
// anywhere), so this is the one deliberate escape hatch; everything spelled out in
// source should use the typed t() instead and get compile-time checking.
//
// Keys built from identifiers that contain dots must slug them first (see slugKey):
// i18next reads a dot as nesting, so "user.created" would look for a subtree.
export function td(key: string, opts?: Record<string, unknown>): string {
  return (i18n.t as (k: string, o?: Record<string, unknown>) => string)(
    key,
    opts,
  );
}

// slugKey makes a dotted identifier safe to use as one i18next key segment.
export function slugKey(id: string): string {
  return id.replace(/\./g, "_");
}

const initial = detectLang();

i18n.use(initReactI18next).init({
  resources: { ru: { translation: ru }, en: { translation: en } },
  lng: initial,
  fallbackLng: "ru",
  // React already escapes everything it renders; letting i18next escape too turns
  // a user's "Ivanov & Co" into "Ivanov &amp; Co" on screen.
  interpolation: { escapeValue: false },
});

document.documentElement.lang = initial;

export default i18n;
