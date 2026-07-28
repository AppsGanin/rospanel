import { useTranslation } from "react-i18next";
import {
  Button,
  IconCheck,
  IconCopy,
  IconHeart,
  InfoModal,
  InfoSection,
  useCopy,
} from "./ui";

const SECTIONS = [1, 2, 3] as const;

const DONATIONALERTS_URL = "https://www.donationalerts.com/r/dmitryapp";
const BOOSTY_URL = "https://boosty.to/githubapps";

// Copyable donation details — all USDT, kept in sync with the README.
const METHODS: { label: string; value: string }[] = [
  { label: "USDT · TRC20 (Tron)", value: "TJwyrPVEZVZ1YrcmDiZTyFjLo3Q2DmEGzs" },
  { label: "USDT · ERC20 (Ethereum)", value: "0xf9d663146ce902da91911b214c71cc73a5269d1d" },
  { label: "USDT · Solana", value: "2qAZRTbaUMTfYuZbD1dCYHjkYgxkw4dUYE9XY3JhC2Cs" },
  { label: "USDT · TON", value: "UQDoat731MLYuIw8ayL3Vhhw7zTBbLvRaQFmDvab--CNNI7e" },
  { label: "Bybit UID", value: "136462734" },
];

function MethodRow({ label, value }: { label: string; value: string }) {
  const { copied, copy } = useCopy();
  return (
    <button
      type="button"
      onClick={() => copy(value)}
      className="flex w-full items-center justify-between gap-3 rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 text-left transition hover:border-brand-300 accent-tint-hover"
    >
      <span className="min-w-0">
        <span className="block text-xs font-bold uppercase tracking-wider text-accent">
          {label}
        </span>
        <span className="block truncate font-mono text-sm text-ink">
          {value}
        </span>
      </span>
      <span className="shrink-0 text-gray-400">
        {copied ? <IconCheck size={18} /> : <IconCopy size={18} />}
      </span>
    </button>
  );
}

export function Donate({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation();
  return (
    <InfoModal
      icon={<IconHeart size={22} />}
      title={t("nav.donate")}
      onClose={onClose}
      footer={
        <Button
          variant="light"
          color="gray"
          onClick={onClose}
          className="w-full sm:w-auto"
        >
          {t("common.close")}
        </Button>
      }
    >
      {SECTIONS.map((n) => (
        <InfoSection
          key={n}
          title={t(`donate.s${n}Title` as "donate.s1Title")}
        >
          {t(`donate.s${n}Body` as "donate.s1Body")}
        </InfoSection>
      ))}

      <InfoSection title="DonationAlerts" bordered={false}>
        <a
          href={DONATIONALERTS_URL}
          target="_blank"
          rel="noreferrer"
          className="flex items-center justify-center gap-2 rounded-lg bg-[#f57d07] px-3 py-2.5 text-sm font-bold text-onaccent transition hover:bg-[#d96e06]"
        >
          <IconHeart size={18} />
          {t("donate.viaDonationAlerts")}
        </a>
        <p className="text-center text-xs text-gray-500">
          {t("donate.donationAlertsNote")}
        </p>
      </InfoSection>

      <InfoSection title="Boosty" bordered={false}>
        <a
          href={BOOSTY_URL}
          target="_blank"
          rel="noreferrer"
          className="flex items-center justify-center gap-2 rounded-lg bg-[#f15f2c] px-3 py-2.5 text-sm font-bold text-onaccent transition hover:bg-[#d94e1f]"
        >
          <IconHeart size={18} />
          {t("donate.viaBoosty")}
        </a>
        <p className="text-center text-xs text-gray-500">
          {t("donate.boostyNote")}
        </p>
      </InfoSection>

      <InfoSection title={t("donate.details")} bordered={false}>
        <div className="flex flex-col gap-2">
          {METHODS.map((m) => (
            <MethodRow key={m.label} {...m} />
          ))}
          <p className="text-center text-xs text-gray-500">
            {t("donate.detailsNote")}
          </p>
        </div>
      </InfoSection>
    </InfoModal>
  );
}
