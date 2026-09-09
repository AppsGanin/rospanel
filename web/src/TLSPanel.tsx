import { useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { getTLS, setACME, type TLSStatus } from "./api";
import { errMessage, notifyError, notifySuccess } from "./notify";
import {
  Button,
  cn,
  Mono,
  Section,
  Select,
  SettingRow,
  Skeleton,
  TextInput,
} from "./ui";
import { isValidACMETarget, isValidEmail } from "./validate";

// TLSPanel is the domain/TLS editor. By default it edits the panel's own domain
// (getTLS/setACME) and redirects to the new address on success. Passing load/save
// (and redirectOnSuccess={false}) reuses the exact same UI for a node's domain — the
// node re-issues its own cert and there's no panel redirect.
export function TLSPanel({
  load = getTLS,
  save = setACME,
  redirectOnSuccess = true,
  onChanged,
}: {
  load?: () => Promise<TLSStatus>;
  save?: (target: string, email: string, provider: string) => Promise<TLSStatus>;
  redirectOnSuccess?: boolean;
  onChanged?: () => void;
} = {}) {
  const { t } = useTranslation();
  const [status, setStatus] = useState<TLSStatus | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [target, setTarget] = useState("");
  const [email, setEmail] = useState("");
  const [provider, setProvider] = useState("letsencrypt");
  const [busy, setBusy] = useState(false);

  // biome-ignore lint/correctness/useExhaustiveDependencies: runs once on mount; the loader is redefined every render, so listing it would refetch in a loop
  useEffect(() => {
    load()
      .then(setStatus)
      .catch((e) => notifyError(errMessage(e)))
      .finally(() => setLoaded(true));
  }, []);

  useEffect(() => {
    if (status) {
      setTarget(status.domain || "");
      setEmail(status.acme_email || "");
      setProvider(status.acme_provider || "letsencrypt");
    }
  }, [status]);

  const issue = async () => {
    const host = target.trim();
    setBusy(true);
    try {
      const s = await save(host, email.trim(), provider);
      setStatus(s);
      if (redirectOnSuccess) {
        notifySuccess(t("tls.changedRedirect"));
        setTimeout(() => {
          window.location.href = `https://${host}${window.location.pathname}${window.location.hash}`;
        }, 2500);
      } else {
        notifySuccess(t("tls.changedReissue"));
        setBusy(false);
        onChanged?.();
      }
    } catch (e) {
      notifyError(errMessage(e));
      setBusy(false);
    }
  };

  if (!loaded)
    return (
      <div className="flex flex-col gap-3.5">
        <Section flush>
          <SettingRow>
            <div className="flex flex-col gap-3">
              <Skeleton className="h-5 w-32" />
              <Skeleton className="h-10 w-full rounded-lg" />
              <Skeleton className="h-10 w-full rounded-lg" />
              <Skeleton className="h-9 w-32 rounded-lg" />
            </div>
          </SettingRow>
        </Section>
      </div>
    );

  const cert = status?.cert;
  const valid = cert && cert.issuer && cert.issuer !== cert.subject;
  const certLabel = valid
    ? status?.acme_provider === "zerossl"
      ? t("tls.validZerossl")
      : t("tls.validLetsencrypt")
    : t("tls.temporary");

  const isZeroSSL = provider === "zerossl";
  const host = target.trim();
  const e = email.trim();
  const targetErr = host !== "" && !isValidACMETarget(host, isZeroSSL);
  const emailErr = e !== "" && !isValidEmail(e);
  const emailMissing = isZeroSSL && e === "";
  const disabled = host === "" || targetErr || emailErr || emailMissing;

  return (
    <div className="flex flex-col gap-3.5">
      <Section
        title={t("tls.currentAddress")}
        action={
          cert ? (
            <span
              className={cn(
                "text-[11px]",
                valid ? "text-success" : "text-warning",
              )}
            >
              {certLabel}
            </span>
          ) : undefined
        }
        flush
      >
        <SettingRow
          hint={
            cert
              ? t("tls.certLine", { issuer: cert.issuer || "—", days: cert.days_left })
              : undefined
          }
          control={
            <Mono className="text-xs text-ink">{status?.domain || "—"}</Mono>
          }
        />
      </Section>

      <Section
        title={t("tls.changeDomain")}
        desc={
          <Trans
            i18nKey={redirectOnSuccess ? "tls.changeHintPanel" : "tls.changeHintNode"}
            components={{ b: <b /> }}
          />
        }
        flush
      >
        <SettingRow
          label={isZeroSSL ? t("tls.newDomain") : t("tls.newDomainOrIp")}
          hint={
            targetErr ? (
              <span className="text-danger">
                {isZeroSSL ? t("wizard.errDomainOnly") : t("wizard.errBadTarget")}
              </span>
            ) : undefined
          }
          wideField
          field={
            <TextInput
              placeholder={
                isZeroSSL ? "vpn.example.com" : t("wizard.domainOrIpPlaceholder")
              }
              value={target}
              onChange={setTarget}
              mono
            />
          }
        />
        <SettingRow
          label={isZeroSSL ? t("wizard.emailRequired") : t("wizard.emailOptional")}
          hint={
            emailErr ? (
              <span className="text-danger">{t("wizard.errBadEmail")}</span>
            ) : undefined
          }
          wideField
          field={
            <TextInput placeholder="you@example.com" value={email} onChange={setEmail} />
          }
        />
        <SettingRow
          label={t("wizard.certAuthority")}
          hint={isZeroSSL ? t("wizard.zerosslNote") : t("wizard.letsencryptNote")}
          field={
            <Select
              value={provider}
              onChange={setProvider}
              data={[
                { value: "letsencrypt", label: "Let's Encrypt" },
                { value: "zerossl", label: "ZeroSSL" },
              ]}
            />
          }
        />
        <SettingRow
          hint={t("tls.takesSeconds")}
          control={
            <Button size="xs" loading={busy} disabled={disabled} onClick={issue}>
              {busy ? t("tls.changing") : t("tls.changeDomain")}
            </Button>
          }
        />
      </Section>
    </div>
  );
}
