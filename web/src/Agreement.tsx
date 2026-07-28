import { useTranslation } from "react-i18next";
import { Button, IconShield, InfoModal, InfoSection } from "./ui";

// Bump the key to re-show the agreement after wording changes.
const ACCEPT_KEY = "rospanel_agreement_v1";

export function agreementAccepted(): boolean {
  try {
    return localStorage.getItem(ACCEPT_KEY) === "1";
  } catch {
    return true; // storage blocked → don't trap the user behind the gate
  }
}

// The six clauses, by number. Kept as a list of ids rather than a dictionary array
// so a translation can never silently drop or reorder a clause.
const SECTIONS = [1, 2, 3, 4, 5, 6] as const;

// onAccept → first-run gate (writes acceptance). onClose → read-only view from
// the footer link. Pass one of them.
export function Agreement({
  onAccept,
  onClose,
}: {
  onAccept?: () => void;
  onClose?: () => void;
}) {
  const { t } = useTranslation();

  const accept = () => {
    try {
      localStorage.setItem(ACCEPT_KEY, "1");
    } catch {
      /* ignore */
    }
    onAccept?.();
  };

  const footer = onAccept ? (
    <Button onClick={accept} className="w-full sm:w-auto">
      {t("agreement.accept")}
    </Button>
  ) : (
    <Button
      variant="light"
      color="gray"
      onClick={onClose}
      className="w-full sm:w-auto"
    >
      {t("common.close")}
    </Button>
  );

  return (
    <InfoModal
      icon={<IconShield size={22} />}
      title={t("nav.agreement")}
      onClose={onClose}
      footer={footer}
    >
      {SECTIONS.map((n) => (
        <InfoSection
          key={n}
          title={t(`agreement.s${n}Title` as "agreement.s1Title")}
        >
          {t(`agreement.s${n}Body` as "agreement.s1Body")}
        </InfoSection>
      ))}
      {onAccept && (
        <div className="rounded-lg border border-gray-200 bg-gray-50 p-3 text-center text-xs text-gray-500">
          {t("agreement.acceptNote")}
        </div>
      )}
    </InfoModal>
  );
}
