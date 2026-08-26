import { useState, useMemo } from "react";
import { useTranslation } from "react-i18next";
import {
  getReleaseChangelog,
  getRecentVersions,
  type ReleaseChangelog,
} from "./changelog";
import { Badge, cn, IconChevron, Select } from "./ui";

interface ChangelogDropdownProps {
  // Target version to display (defaults to the latest available or current)
  version: string;
  // Raw release notes markdown from GitHub API if available
  rawNotes?: string;
  // Initially expanded
  defaultOpen?: boolean;
  // Show version selector to browse past releases
  allowVersionSelect?: boolean;
  // Compact layout (e.g. for modal dialogs)
  compact?: boolean;
  className?: string;
}

export function ChangelogDropdown({
  version,
  rawNotes,
  defaultOpen = false,
  allowVersionSelect = true,
  compact = false,
  className,
}: ChangelogDropdownProps) {
  const { t, i18n } = useTranslation();
  const [open, setOpen] = useState(defaultOpen);
  const [selectedVersion, setSelectedVersion] = useState<string>(
    version.replace(/^v/, "")
  );

  // Sync selectedVersion when version prop changes
  const activeVersion = (selectedVersion || version).replace(/^v/, "");

  const recentVersions = useMemo(() => {
    const list = getRecentVersions();
    if (activeVersion && !list.includes(activeVersion)) {
      return [activeVersion, ...list];
    }
    return list;
  }, [activeVersion]);

  const versionOptions = useMemo(() => {
    return recentVersions.map((v) => ({
      value: v,
      label: `v${v}`,
    }));
  }, [recentVersions]);

  // Load friendly changelog
  const changelog: ReleaseChangelog = useMemo(() => {
    const isTargetVersion = activeVersion === version.replace(/^v/, "");
    const notesToPass = isTargetVersion ? rawNotes : undefined;
    return getReleaseChangelog(activeVersion, notesToPass, i18n.language);
  }, [activeVersion, version, rawNotes, i18n.language]);

  const hasCategories = changelog.categories && changelog.categories.length > 0;

  if (compact) {
    return (
      <div
        className={cn(
          "rounded-lg border border-ink/10 bg-surface p-3 text-sm text-ink dark:border-white/10",
          className
        )}
      >
        <div className="mb-2 flex items-center justify-between font-semibold">
          <span className="flex items-center gap-1.5">
            <span>✨</span>
            <span>{t("general.changelogLatest", { version: activeVersion })}</span>
          </span>
          <Badge color="brand">v{activeVersion}</Badge>
        </div>
        {changelog.summary && (
          <p className="mb-2 text-xs text-ink-muted leading-relaxed">
            {changelog.summary}
          </p>
        )}
        <div className="space-y-2">
          {changelog.categories.map((cat) => (
            <div key={cat.key} className="space-y-1">
              <div className="flex items-center gap-1 text-xs font-bold text-ink-muted">
                <span>{cat.icon}</span>
                <span>{cat.title}</span>
              </div>
              <ul className="list-inside list-disc space-y-0.5 pl-1 text-xs text-ink">
                {cat.items.map((item, idx) => (
                  <li key={idx} className="leading-snug">
                    {item.scope && (
                      <span className="mr-1 font-semibold text-brand-600 dark:text-brand-400">
                        [{item.scope}]
                      </span>
                    )}
                    {item.text}
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      </div>
    );
  }

  return (
    <div
      className={cn(
        "overflow-hidden rounded-xl border border-ink/10 bg-surface/70 shadow-xs transition-all duration-200 dark:border-white/10",
        open && "bg-surface shadow-sm",
        className
      )}
    >
      {/* Dropdown Trigger Header */}
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className="flex w-full cursor-pointer items-center justify-between p-3.5 text-left transition-colors hover:bg-ink/5 focus:outline-hidden dark:hover:bg-white/5"
      >
        <div className="flex min-w-0 items-center gap-2.5">
          <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-brand-50 text-base dark:bg-brand-900/40">
            ✨
          </span>
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-sm font-bold text-ink">
                {t("general.changelogLatest", { version: activeVersion })}
              </span>
              <Badge color="brand" className="text-xs">
                v{activeVersion}
              </Badge>
            </div>
            <p className="text-xs text-ink-muted">
              {open ? t("general.changelogHide") : t("general.changelogShow")}
            </p>
          </div>
        </div>

        <div className="flex items-center gap-2">
          <div
            className={cn(
              "flex h-6 w-6 items-center justify-center rounded-full text-ink-muted transition-transform duration-200",
              open && "rotate-180 text-brand-600 dark:text-brand-400"
            )}
          >
            <IconChevron size={18} />
          </div>
        </div>
      </button>

      {/* Expanded Content */}
      {open && (
        <div className="border-t border-ink/10 p-4 pt-3 dark:border-white/10">
          {/* Optional version picker to view past release changelogs */}
          {allowVersionSelect && versionOptions.length > 1 && (
            <div className="mb-3 flex items-center justify-between gap-3 border-b border-ink/5 pb-3 dark:border-white/5">
              <span className="text-xs font-semibold text-ink-muted">
                {t("general.changelogHistory")}:
              </span>
              <div className="w-36">
                <Select
                  data={versionOptions}
                  value={activeVersion}
                  onChange={(val) => {
                    if (val) setSelectedVersion(val);
                  }}
                />
              </div>
            </div>
          )}

          {/* Release Summary Highlight */}
          {changelog.summary && (
            <div className="mb-3.5 rounded-lg bg-brand-50/70 p-2.5 text-xs text-brand-900 dark:bg-brand-950/40 dark:text-brand-200">
              💡 <b>{t("general.changelog")}:</b> {changelog.summary}
            </div>
          )}

          {/* Categorized List */}
          {hasCategories ? (
            <div className="space-y-3.5">
              {changelog.categories.map((category) => (
                <div key={category.key} className="space-y-1.5">
                  <div className="flex items-center gap-1.5 text-xs font-bold uppercase tracking-wider text-ink-muted">
                    <span>{category.icon}</span>
                    <span>{category.title}</span>
                  </div>
                  <ul className="space-y-1.5 pl-1">
                    {category.items.map((item, idx) => (
                      <li
                        key={idx}
                        className="flex items-start gap-2 text-xs leading-relaxed text-ink"
                      >
                        <span className="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-brand-500/80" />
                        <span className="min-w-0">
                          {item.scope && (
                            <span className="mr-1.5 inline-block rounded-xs bg-ink/5 px-1 py-0.2 text-[10px] font-semibold text-ink-muted dark:bg-white/10">
                              {item.scope}
                            </span>
                          )}
                          {item.text}
                        </span>
                      </li>
                    ))}
                  </ul>
                </div>
              ))}
            </div>
          ) : (
            <p className="text-xs text-ink-muted">{t("general.changelogEmpty")}</p>
          )}
        </div>
      )}
    </div>
  );
}
