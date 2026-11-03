/* Make the header language selector switch the same page, not the home page.
 *
 * Material's `extra.alternate` is static config: every language link points at a
 * fixed URL, so from /benchmarks/ the Russian entry went to /ru/ and lost the page.
 * This rewrites the selector (and its <link rel=alternate> twins) to the current
 * page's counterpart. Pages without a translation fall back to that language's home.
 */
(function () {
  const BASE = "/rospanel/";
  const HOME = { en: "", ru: "ru/", zh: "zh/", ar: "ar/", fa: "fa/" };
  const BENCH = {
    en: "benchmarks/",
    ru: "benchmarks/ru/",
    zh: "benchmarks/zh/",
    ar: "benchmarks/ar/",
    fa: "benchmarks/fa/",
  };
  const API = {
    en: "api/",
    ru: "api/ru/",
    zh: "api/zh/",
    ar: "api/ar/",
    fa: "api/fa/",
  };
  const API_REF = {
    en: "api/reference/",
    ru: "api/ru/",
    zh: "api/zh/",
    ar: "api/ar/",
    fa: "api/fa/",
  };
  const GRAFANA = {
    en: "grafana/",
    ru: "grafana/ru/",
    zh: "grafana/zh/",
    ar: "grafana/ar/",
    fa: "grafana/fa/",
  };

  const path = location.pathname.startsWith(BASE)
    ? location.pathname.slice(BASE.length)
    : location.pathname.replace(/^\//, "");

  const isBenchmarks = path.startsWith("benchmarks");
  const isReference = path.startsWith("api/reference");
  const isAPI = path.startsWith("api/") || path === "api/";
  const isGrafana = path.startsWith("grafana");
  const isPlugins = path.startsWith("plugins/");
  // Plugin pages are English at plugins/<name>/ and Russian at plugins/ru/<name>/,
  // with the Russian index at plugins/ru/ — the same shape as the other sections.
  const pluginSegs = isPlugins ? path.split("/").filter(Boolean).slice(1) : [];
  const pluginName = pluginSegs[0] === "ru" ? pluginSegs[1] || "" : pluginSegs[0] || "";
  const page = isBenchmarks
    ? BENCH
    : isReference
      ? API_REF
      : isAPI
        ? API
        : isGrafana
          ? GRAFANA
          : isPlugins
            ? null
            : HOME;

  // Plugin pages only have English and Russian; the other languages fall back to
  // the English page rather than sending the reader to the home page.
  function pluginHref(lang) {
    if (lang === "ru") return pluginName ? "plugins/ru/" + pluginName + "/" : "plugins/ru/";
    return pluginName ? "plugins/" + pluginName + "/" : "plugins/";
  }

  function targetFor(lang) {
    return isPlugins ? pluginHref(lang) : page && page[lang];
  }

  function currentLanguage() {
    if (path.startsWith("plugins/ru")) return "ru";
    const markers = {
      ru: ["benchmarks/ru", "api/ru", "grafana/ru", "ru/"],
      zh: ["benchmarks/zh", "api/zh", "grafana/zh", "zh/"],
      ar: ["benchmarks/ar", "api/ar", "grafana/ar", "ar/"],
      fa: ["benchmarks/fa", "api/fa", "grafana/fa", "fa/"],
    };
    for (const [lang, prefixes] of Object.entries(markers)) {
      if (prefixes.some((p) => path.startsWith(p))) return lang;
    }
    return "en";
  }

  function apply() {
    document.querySelectorAll("a.md-select__link[hreflang]").forEach((a) => {
      const lang = a.getAttribute("hreflang");
      const href = targetFor(lang);
      if (href) a.setAttribute("href", BASE + href);
    });
    document.querySelectorAll('link[rel="alternate"][hreflang]').forEach((l) => {
      const lang = l.getAttribute("hreflang");
      const href = targetFor(lang);
      if (href) l.setAttribute("href", BASE + href);
    });

    // The nav is static and labelled in English; keep its links on the page and
    // in the language the reader is actually in.
    const lang = currentLanguage();
    if (lang !== "en") {
      document.querySelectorAll('.md-nav__link').forEach((a) => {
        const label = a.textContent.trim();
        if (label === "Benchmarks") a.setAttribute("href", BASE + BENCH[lang]);
        if (label === "Overview") a.setAttribute("href", BASE + HOME[lang]);
        if (label === "API") a.setAttribute("href", BASE + API[lang]);
        if (label === "Grafana") a.setAttribute("href", BASE + GRAFANA[lang]);
        if (label === "Plugins") a.setAttribute("href", BASE + "plugins/ru/");
      });
    }
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", apply);
  } else {
    apply();
  }
})();
