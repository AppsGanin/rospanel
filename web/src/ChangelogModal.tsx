import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { getChangelog, type Release } from "./api";
import { useShowMore } from "./hooks";
import { errMessage, notifyError } from "./notify";
import { Badge, CenterLoader, Modal, ShowMore } from "./ui";

// The release history the binary was built with, newest first, with the running
// version marked. Read from the panel itself rather than GitHub: the operator's
// box may not reach it, and what changed in THIS build is the question — not
// what the latest release says.

const SECTION_KEYS: Record<string, "changelog.features" | "changelog.fixes" | "changelog.perf" | "changelog.reverts"> = {
  Features: "changelog.features",
  "Bug Fixes": "changelog.fixes",
  "Performance Improvements": "changelog.perf",
  Reverts: "changelog.reverts",
};

export function ChangelogModal({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation();
  const [version, setVersion] = useState("");
  const [releases, setReleases] = useState<Release[] | null>(null);
  const rows = useShowMore(releases ?? [], { first: 5, step: 10, resetKey: releases });

  useEffect(() => {
    getChangelog()
      .then((r) => {
        setVersion(r.version);
        setReleases(r.releases ?? []);
      })
      .catch((e) => {
        notifyError(errMessage(e));
        setReleases([]);
      });
  }, []);

  return (
    <Modal open onClose={onClose} title={t("changelog.title")} size="lg">
      {releases === null ? (
        <CenterLoader />
      ) : releases.length === 0 ? (
        <p className="text-sm text-ink-muted">{t("changelog.empty")}</p>
      ) : (
        <div className="flex max-h-[70vh] flex-col gap-5 overflow-y-auto pr-1">
          {rows.shown.map((r) => {
            const current = r.version === version;
            return (
              <section key={r.version} className={current ? "" : "opacity-90"}>
                <div className="mb-2 flex flex-wrap items-baseline gap-2">
                  <h3 className="font-bold text-ink">v{r.version}</h3>
                  {r.date && <span className="text-xs text-ink-muted">{r.date}</span>}
                  {current && (
                    <Badge color="brand" size="xs">
                      {t("changelog.current")}
                    </Badge>
                  )}
                </div>
                {r.sections.map((s) => (
                  <div key={s.title} className="mb-2">
                    <p className="text-xs font-semibold uppercase tracking-wide text-ink-muted">
                      {SECTION_KEYS[s.title] ? t(SECTION_KEYS[s.title]) : s.title}
                    </p>
                    <ul className="mt-1 list-disc space-y-0.5 pl-5 text-sm text-ink">
                      {s.items.map((item, i) => (
                        <li key={i}>{item}</li>
                      ))}
                    </ul>
                  </div>
                ))}
              </section>
            );
          })}
          <ShowMore rest={rows.rest} onClick={rows.showMore} />
        </div>
      )}
    </Modal>
  );
}
