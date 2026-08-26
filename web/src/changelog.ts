// changelog.ts provides human-friendly, plain-language release notes and
// changelog parsing for non-technical operators and users.

export interface ChangelogItem {
  text: string;
  scope?: string;
}

export interface ChangelogCategory {
  key: "features" | "fixes" | "improvements" | "security" | "other";
  title: string;
  icon: string;
  items: ChangelogItem[];
}

export interface ReleaseChangelog {
  version: string;
  date?: string;
  summary?: string;
  categories: ChangelogCategory[];
}

// Curated changelog entries written in simple, plain language for non-technical users.
// Both Russian and English versions are maintained.
const CURATED_CHANGELOGS: Record<
  string,
  {
    ru: { summary?: string; categories: ChangelogCategory[] };
    en: { summary?: string; categories: ChangelogCategory[] };
  }
> = {
  "2.14.0": {
    ru: {
      summary: "Управление активными сессиями администраторов и улучшение интерфейса.",
      categories: [
        {
          key: "features",
          title: "Новые возможности",
          icon: "✨",
          items: [
            {
              text: "Управление активными сессиями: теперь можно просматривать список всех входов администраторов с датами, IP-адресами и завершать ненужные сессии в один клик.",
            },
            {
              text: "Кнопка выхода со всех устройств для мгновенной защиты учётной записи.",
            },
          ],
        },
        {
          key: "improvements",
          title: "Улучшения и удобство",
          icon: "⚡",
          items: [
            {
              text: "Улучшен выбор ролей и прав доступа администраторов в настройках.",
            },
          ],
        },
      ],
    },
    en: {
      summary: "Admin session management and user interface improvements.",
      categories: [
        {
          key: "features",
          title: "New Features",
          icon: "✨",
          items: [
            {
              text: "Admin session management: view all active admin logins with dates and IP addresses, and revoke any session with one click.",
            },
            {
              text: "Log out from all devices button for instant account security.",
            },
          ],
        },
        {
          key: "improvements",
          title: "Improvements",
          icon: "⚡",
          items: [
            {
              text: "Improved role selector and permission assignment for administrators.",
            },
          ],
        },
      ],
    },
  },
  "2.13.5": {
    ru: {
      summary: "Оптимизация работы SSL-сертификатов и сетевых перенаправлений.",
      categories: [
        {
          key: "improvements",
          title: "Улучшения и стабильность",
          icon: "⚡",
          items: [
            {
              text: "Автоматическое перенаправление на безопасный HTTPS и бесперебойное обновление SSL-сертификатов на 80 порту.",
            },
          ],
        },
      ],
    },
    en: {
      summary: "SSL certificate handling and network redirect optimizations.",
      categories: [
        {
          key: "improvements",
          title: "Improvements & Stability",
          icon: "⚡",
          items: [
            {
              text: "Automatic HTTPS redirection and seamless SSL certificate issuance on port 80.",
            },
          ],
        },
      ],
    },
  },
  "2.13.4": {
    ru: {
      summary: "Гибкая настройка лимитов устройств и улучшение полей ввода.",
      categories: [
        {
          key: "features",
          title: "Новые возможности",
          icon: "✨",
          items: [
            {
              text: "Индивидуальный лимит устройств: теперь можно задать произвольное количество устройств для каждого пользователя.",
            },
          ],
        },
        {
          key: "fixes",
          title: "Исправления",
          icon: "🐛",
          items: [
            {
              text: "Улучшена работа текстовых полей ввода на смартфонах и сенсорных экранах.",
            },
          ],
        },
      ],
    },
    en: {
      summary: "Custom device limits per user and improved input fields.",
      categories: [
        {
          key: "features",
          title: "New Features",
          icon: "✨",
          items: [
            {
              text: "Custom device limits: you can now configure custom allowed device counts for individual users.",
            },
          ],
        },
        {
          key: "fixes",
          title: "Fixes",
          icon: "🐛",
          items: [
            {
              text: "Improved text inputs behavior on mobile devices and virtual keyboards.",
            },
          ],
        },
      ],
    },
  },
  "2.13.3": {
    ru: {
      summary: "Улучшение контроля ограничений по устройствам.",
      categories: [
        {
          key: "improvements",
          title: "Улучшения",
          icon: "⚡",
          items: [
            {
              text: "Расширены варианты выбора лимита устройств и улучшен контроль одновременных подключений.",
            },
          ],
        },
      ],
    },
    en: {
      summary: "Enhanced device limit controls and selection options.",
      categories: [
        {
          key: "improvements",
          title: "Improvements",
          icon: "⚡",
          items: [
            {
              text: "Expanded device limit selection options and enhanced concurrent connection enforcement.",
            },
          ],
        },
      ],
    },
  },
  "2.13.2": {
    ru: {
      summary: "Повышение надежности и стабильности системных сервисов.",
      categories: [
        {
          key: "improvements",
          title: "Улучшения и стабильность",
          icon: "⚡",
          items: [
            {
              text: "Повышена стабильность сбора системной статистики и оптимизирована работа фоновых процессов.",
            },
          ],
        },
      ],
    },
    en: {
      summary: "System services reliability and background tasks optimization.",
      categories: [
        {
          key: "improvements",
          title: "Improvements & Stability",
          icon: "⚡",
          items: [
            {
              text: "Improved system stats collection stability and optimized background services.",
            },
          ],
        },
      ],
    },
  },
  "2.13.1": {
    ru: {
      summary: "Исправление ошибок базы данных и оптимизация защиты.",
      categories: [
        {
          key: "fixes",
          title: "Исправления",
          icon: "🐛",
          items: [
            {
              text: "Устранена проблема с временной блокировкой базы данных при высокой нагрузке.",
            },
            {
              text: "Улучшена обработка ошибок при создании пользователей и добавлении настроек.",
            },
          ],
        },
        {
          key: "improvements",
          title: "Улучшения",
          icon: "⚡",
          items: [
            {
              text: "Переход на более эффективную систему защиты от подбора паролей (nftables).",
            },
          ],
        },
      ],
    },
    en: {
      summary: "Database fixes and security optimizations.",
      categories: [
        {
          key: "fixes",
          title: "Fixes",
          icon: "🐛",
          items: [
            {
              text: "Fixed database lock issues during high traffic and concurrent operations.",
            },
            {
              text: "Improved error reporting when creating users and updating settings.",
            },
          ],
        },
        {
          key: "improvements",
          title: "Improvements",
          icon: "⚡",
          items: [
            {
              text: "Switched brute-force protection to more efficient nftables engine.",
            },
          ],
        },
      ],
    },
  },
  "2.13.0": {
    ru: {
      summary: "Настройка репозитория обновлений и улучшение синхронизации.",
      categories: [
        {
          key: "features",
          title: "Новые возможности",
          icon: "✨",
          items: [
            {
              text: "Поддержка настройки пользовательского источника обновлений панели.",
            },
            {
              text: "Улучшена стабильность передачи команд и синхронизации с удалёнными нодами.",
            },
          ],
        },
      ],
    },
    en: {
      summary: "Customizable update repository source and improved node sync.",
      categories: [
        {
          key: "features",
          title: "New Features",
          icon: "✨",
          items: [
            {
              text: "Added support for customizable update repository source.",
            },
            {
              text: "Improved node synchronization reliability and command dispatching.",
            },
          ],
        },
      ],
    },
  },
  "2.12.0": {
    ru: {
      summary: "Гибкие режимы отслеживания и ограничения устройств.",
      categories: [
        {
          key: "features",
          title: "Новые возможности",
          icon: "✨",
          items: [
            {
              text: "Введены гибкие режимы отслеживания устройств (строгий, мягкий или гибридный) для удобного контроля подписок.",
            },
          ],
        },
      ],
    },
    en: {
      summary: "Flexible device tracking modes and enforcement strategies.",
      categories: [
        {
          key: "features",
          title: "New Features",
          icon: "✨",
          items: [
            {
              text: "Introduced flexible device count enforcement modes (strict, gentle, hybrid) for client subscriptions.",
            },
          ],
        },
      ],
    },
  },
  "2.11.0": {
    ru: {
      summary: "Быстрый установщик и усиленная защита данных.",
      categories: [
        {
          key: "features",
          title: "Новые возможности",
          icon: "✨",
          items: [
            {
              text: "Добавлен официальный скрипт быстрой установки панели одной командой.",
            },
            {
              text: "Расширено автоматическое шифрование конфиденциальных полей в базе данных.",
            },
          ],
        },
      ],
    },
    en: {
      summary: "Quick installation script and expanded data security.",
      categories: [
        {
          key: "features",
          title: "New Features",
          icon: "✨",
          items: [
            {
              text: "Added official one-command quick installation script.",
            },
            {
              text: "Expanded automatic encryption coverage for sensitive database entries.",
            },
          ],
        },
      ],
    },
  },
  "2.10.0": {
    ru: {
      summary: "Табличный вид списков, фильтрация и повышение надежности.",
      categories: [
        {
          key: "features",
          title: "Новые возможности",
          icon: "✨",
          items: [
            {
              text: "Табличный режим отображения пользователей, журнала событий и списков платежей с удобной сортировкой.",
            },
          ],
        },
        {
          key: "fixes",
          title: "Исправления",
          icon: "🐛",
          items: [
            {
              text: "Исправлены недочеты при генерации ссылок на подписки и управлении SSL/TLS сертификатами.",
            },
          ],
        },
      ],
    },
    en: {
      summary: "Table views for users and logs, sorting and bug fixes.",
      categories: [
        {
          key: "features",
          title: "New Features",
          icon: "✨",
          items: [
            {
              text: "Table view for users list, journal logs, and payment records with sorting and search.",
            },
          ],
        },
        {
          key: "fixes",
          title: "Fixes",
          icon: "🐛",
          items: [
            {
              text: "Fixed issues with subscription link generation and SSL/TLS certificate lifecycle.",
            },
          ],
        },
      ],
    },
  },
};

// Cleans raw git/conventional commit text into human-readable sentences.
function cleanItemText(raw: string, isRu: boolean): { text: string; scope?: string } {
  let text = raw.trim();

  // Strip commit hash links like ([345566f](...)) or (345566f) or [345566f]
  text = text.replace(/\(\[?[0-9a-f]{6,40}\]?(?:\([^)]*\))?\)/gi, "");
  text = text.replace(/\[[0-9a-f]{6,40}\]/gi, "");
  text = text.replace(/\b[0-9a-f]{7,40}\b/gi, "");

  // Strip markdown links [text](url) -> text
  text = text.replace(/\[([^\]]+)\]\([^)]+\)/g, "$1");

  // Extract scope like **ui:** or **core:** or (ui):
  let scope = "";
  const scopeMatch = text.match(/^\s*(?:\*\*|\()?([a-z0-9_-]+)(?:\*\*|\))?:\s*/i);
  if (scopeMatch) {
    scope = scopeMatch[1].toLowerCase();
    text = text.slice(scopeMatch[0].length);
  }

  // Capitalize first letter
  text = text.trim();
  if (text.length > 0) {
    text = text.charAt(0).toUpperCase() + text.slice(1);
  }

  // If Russian, translate common technical verbs for non-technical readability
  if (isRu) {
    text = text
      .replace(/^Implement\s+/i, "Добавлено: ")
      .replace(/^Add\s+/i, "Добавлено: ")
      .replace(/^Introduce\s+/i, "Внедрено: ")
      .replace(/^Enable\s+/i, "Включено: ")
      .replace(/^Fix\s+/i, "Исправлено: ")
      .replace(/^Resolve\s+/i, "Устранено: ")
      .replace(/^Update\s+/i, "Обновлено: ")
      .replace(/^Improve\s+/i, "Улучшено: ")
      .replace(/^Optimize\s+/i, "Оптимизировано: ")
      .replace(/^Refactor\s+/i, "Оптимизировано: ")
      .replace(/^Switch\s+/i, "Переведено на: ")
      .replace(/^Support\s+/i, "Поддержка: ");
  }

  return { text, scope: scope || undefined };
}

// Parses raw markdown release notes (from GitHub Releases API) into clean categories.
export function parseRawReleaseNotes(
  rawNotes: string,
  version: string,
  lang: string = "ru"
): ReleaseChangelog {
  const isRu = lang.startsWith("ru");
  const categories: ChangelogCategory[] = [];

  const categoryMap: Record<
    string,
    { key: ChangelogCategory["key"]; title: string; icon: string; items: ChangelogItem[] }
  > = {
    features: {
      key: "features",
      title: isRu ? "Новые возможности" : "New Features",
      icon: "✨",
      items: [],
    },
    fixes: {
      key: "fixes",
      title: isRu ? "Исправления ошибок" : "Bug Fixes",
      icon: "🐛",
      items: [],
    },
    improvements: {
      key: "improvements",
      title: isRu ? "Улучшения и оптимизация" : "Improvements",
      icon: "⚡",
      items: [],
    },
    other: {
      key: "other",
      title: isRu ? "Прочие изменения" : "Other Changes",
      icon: "🔧",
      items: [],
    },
  };

  let currentCategory = "other";
  const lines = rawNotes.split("\n");

  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed) continue;

    // Detect markdown headers
    const headerMatch = trimmed.match(/^#{1,4}\s+(.*)$/);
    if (headerMatch) {
      const headerTitle = headerMatch[1].toLowerCase();
      if (headerTitle.includes("feat") || headerTitle.includes("новое") || headerTitle.includes("возможн")) {
        currentCategory = "features";
      } else if (headerTitle.includes("fix") || headerTitle.includes("исправл") || headerTitle.includes("баг")) {
        currentCategory = "fixes";
      } else if (
        headerTitle.includes("perf") ||
        headerTitle.includes("refactor") ||
        headerTitle.includes("улучш") ||
        headerTitle.includes("оптим")
      ) {
        currentCategory = "improvements";
      } else {
        currentCategory = "other";
      }
      continue;
    }

    // Detect bullet points
    if (trimmed.startsWith("* ") || trimmed.startsWith("- ") || trimmed.startsWith("• ")) {
      const bulletContent = trimmed.replace(/^[*•-]\s+/, "");
      const { text, scope } = cleanItemText(bulletContent, isRu);
      if (text) {
        categoryMap[currentCategory].items.push({ text, scope });
      }
    }
  }

  // Populate categories that have items
  for (const cat of ["features", "fixes", "improvements", "other"]) {
    const c = categoryMap[cat];
    if (c && c.items.length > 0) {
      categories.push(c);
    }
  }

  // Fallback if no bullet points were parsed
  if (categories.length === 0 && rawNotes.trim()) {
    categories.push({
      key: "other",
      title: isRu ? "Список изменений" : "Release Notes",
      icon: "📋",
      items: [{ text: rawNotes.trim().replace(/^#{1,4}\s+/gm, "") }],
    });
  }

  return {
    version: version.replace(/^v/, ""),
    categories,
  };
}

// Returns a user-friendly release changelog for the given version.
// If curated notes exist, they are returned. Otherwise, rawNotes are parsed into clean friendly format.
export function getReleaseChangelog(
  version: string,
  rawNotes?: string,
  lang: string = "ru"
): ReleaseChangelog {
  const normVer = version.replace(/^v/, "").trim();
  const isRu = lang.startsWith("ru");
  const curated = CURATED_CHANGELOGS[normVer];

  if (curated) {
    const entry = isRu ? curated.ru : curated.en;
    return {
      version: normVer,
      summary: entry.summary,
      categories: entry.categories,
    };
  }

  if (rawNotes && rawNotes.trim()) {
    return parseRawReleaseNotes(rawNotes, normVer, lang);
  }

  // Default fallback when neither curated notes nor raw notes are available
  return {
    version: normVer,
    summary: isRu
      ? "Обновление стабильности и производительности системы."
      : "System stability and performance update.",
    categories: [
      {
        key: "improvements",
        title: isRu ? "Обновление системы" : "System Update",
        icon: "⚡",
        items: [
          {
            text: isRu
              ? "Плановое обновление компонентов панели и улучшение надежности."
              : "Routine update of panel components and reliability improvements.",
          },
        ],
      },
    ],
  };
}

// Returns a list of all known versions in curated history (newest first).
export function getRecentVersions(): string[] {
  return Object.keys(CURATED_CHANGELOGS);
}
