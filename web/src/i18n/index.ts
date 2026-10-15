// Panel localisation: Russian, English, Simplified Chinese, Arabic and Persian (the
// last two right to left).
//
// The dictionaries are typed against each other (see en.ts): a key present in ru
// and missing in en is a tsc error, not a string that silently renders in the
// wrong language at runtime.
import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import en from "./en";
import ru, { type Dict } from "./ru";

export const LANGS = [
  { code: "ru", label: "Русский" },
  { code: "en", label: "English" },
  { code: "zh", label: "简体中文" },
  { code: "ar", label: "العربية" },
  { code: "fa", label: "فارسی" },
] as const;

export type Lang = (typeof LANGS)[number]["code"];

const STORAGE_KEY = "rospanel.lang";

function isLang(v: string | null): v is Lang {
  return LANGS.some((l) => l.code === v);
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

// detectLang reads the browser's preference list: Russian (and Belarusian,
// Ukrainian) speakers get Russian, Chinese, Arabic and Persian speakers their language,
// everyone else English. Note this is not i18next's fallbackLng, which is a
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
    if (/^(ru|be|uk)\b/.test(tag)) return "ru";
    if (/^(en|zh|ar|fa)\b/.test(tag)) return tag.slice(0, 2) as Lang;
  }
  return "en";
}

// Chinese, Arabic and Persian are fetched when picked rather than shipped in the main bundle
// to everyone; Russian (the fallback) and English always are.
const lazy: Partial<Record<Lang, () => Promise<{ default: Dict }>>> = {
  zh: () => import("./zh"),
  ar: () => import("./ar"),
  fa: () => import("./fa"),
};

async function load(lang: Lang) {
  const get = lazy[lang];
  if (get && !i18n.hasResourceBundle(lang, "translation")) {
    i18n.addResourceBundle(lang, "translation", (await get()).default);
  }
}

// switching counts setLang calls: a pick whose dictionary arrives after a later
// pick was made is dropped, so the last click wins.
let switching = 0;

// setLang switches the panel's language. A dictionary that fails to load (an old tab
// whose chunk an upgrade replaced) leaves the language — and the stored pick — as
// they were.
export async function setLang(lang: Lang) {
  const turn = ++switching;
  try {
    await load(lang);
  } catch {
    return;
  }
  if (turn !== switching) return;
  try {
    localStorage.setItem(STORAGE_KEY, lang);
  } catch {
    /* storage disabled — the change still applies for this session */
  }
  await i18n.changeLanguage(lang);
  if (turn === switching) markDocument(lang);
}

// markDocument tags the page with its language and writing direction: Arabic and
// Persian lay the whole panel out right to left.
function markDocument(lang: Lang) {
  document.documentElement.lang = lang;
  document.documentElement.dir = isRTL(lang) ? "rtl" : "ltr";
}

export function isRTL(lang: Lang = currentLang()): boolean {
  return lang === "ar" || lang === "fa";
}

export function currentLang(): Lang {
  return isLang(i18n.language) ? i18n.language : "ru";
}

// arrow is "→" read in the panel's direction: "old → new" in Arabic must point from
// the right-hand value to the left-hand one. (U+2192 is not mirrored by bidi.)
export function arrow(): string {
  return isRTL() ? "←" : "→";
}

// change writes "from → to". Each side is isolated: two Latin values would otherwise
// merge into one left-to-right run in Arabic and turn the arrow round.
export function change(from: string, to: string): string {
  return `\u2068${from}\u2069 ${arrow()} \u2068${to}\u2069`;
}

// locale is the tag dates and numbers are formatted with. Arabic keeps Latin
// digits: every number the panel prints (ports, IPs, sizes) is read alongside
// Latin text, and the dictionaries spell their own numbers that way. Persian also
// keeps the Gregorian calendar its default would replace: dates are typed, sent
// and compared in it everywhere else (date inputs, the bots, the API).
export function locale(): string {
  switch (currentLang()) {
    case "ar":
      return "ar-u-nu-latn";
    case "fa":
      return "fa-u-nu-latn-ca-gregory";
    case "zh":
      return "zh-CN";
    default:
      return currentLang();
  }
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

markDocument(initial);

// ready settles once the initial language's dictionary is in: the first paint waits
// for it rather than flashing Russian at a Chinese, Arabic or Persian reader. A failed fetch
// renders in English for this session, the stored pick left as it was.
export const ready: Promise<unknown> = load(initial)
  .then(() => i18n.changeLanguage(initial))
  .catch(() => {
    markDocument("en");
    return i18n.changeLanguage("en");
  });

export default i18n;
