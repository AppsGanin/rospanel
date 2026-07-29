import { useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { getTLS, setACME, type TLSStatus } from "./api";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { Badge, Button, Select, Skeleton, TextInput } from "./ui";
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

  useEffect(() => {
    load()
      .then(setStatus)
      .catch((e) => notifyError(errMessage(e)))
      .finally(() => setLoaded(true));
    // eslint-disable-next-line react-hooks/exhaustive-deps
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

  if (!loaded) return (
    <div className="flex flex-col gap-3">
      <div className="rounded-xl border border-gray-200/80 bg-gray-50/60 p-4">
        <div className="flex items-center justify-between gap-3 mb-4">
          <Skeleton className="h-5 w-32" />
          <Skeleton className="h-6 w-20 rounded-full" />
        </div>
        <div className="flex flex-col gap-3">
          <Skeleton className="h-10 w-full rounded-lg" />
          <Skeleton className="h-10 w-full rounded-lg" />
          <Skeleton className="h-9 w-32 rounded-lg" />
        </div>
      </div>
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
    <div className="flex flex-col gap-3">
      <div className="rounded-xl border border-gray-200/80 bg-gray-50/60 p-4">
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between sm:gap-3">
          <div className="min-w-0">
            <p className="text-sm text-ink-muted">{t("tls.currentAddress")}</p>
            <p className="break-all text-lg font-bold text-ink">
              {status?.domain || "—"}
            </p>
            {cert && (
              <p className="mt-1 text-sm text-ink-muted">
                {t("tls.certLine", { issuer: cert.issuer || "—", days: cert.days_left })}
              </p>
            )}
          </div>
          {cert && (
            <Badge color={valid ? "teal" : "orange"} className="self-start sm:self-auto">
              {certLabel}
            </Badge>
          )}
        </div>
      </div>

      <div className="rounded-xl border border-gray-200/80 bg-gray-50/60 p-4">
        <div className="flex flex-col gap-3">
          <p className="font-semibold">{t("tls.changeDomain")}</p>
          <p className="text-sm text-ink-muted">
            <Trans
              i18nKey={
                redirectOnSuccess ? "tls.changeHintPanel" : "tls.changeHintNode"
              }
              components={{ b: <b /> }}
            />
          </p>
          <div>
            <TextInput
              label={isZeroSSL ? t("tls.newDomain") : t("tls.newDomainOrIp")}
              placeholder={
                isZeroSSL
                  ? "vpn.example.com"
                  : t("wizard.domainOrIpPlaceholder")
              }
              value={target}
              onChange={setTarget}
            />
            {targetErr && (
              <p className="mt-1 text-xs text-danger">
                {isZeroSSL
                  ? t("wizard.errDomainOnly")
                  : t("wizard.errBadTarget")}
              </p>
            )}
          </div>
          <div>
            <TextInput
              label={
                isZeroSSL
                  ? t("wizard.emailRequired")
                  : t("wizard.emailOptional")
              }
              placeholder="you@example.com"
              value={email}
              onChange={setEmail}
            />
            {emailErr && (
              <p className="mt-1 text-xs text-danger">
                {t("wizard.errBadEmail")}
              </p>
            )}
          </div>
          <Select
            label={t("wizard.certAuthority")}
            value={provider}
            onChange={setProvider}
            data={[
              { value: "letsencrypt", label: "Let's Encrypt" },
              { value: "zerossl", label: "ZeroSSL" },
            ]}
          />
          {isZeroSSL && (
            <p className="text-sm text-ink-muted">
              {t("wizard.zerosslNote")}
            </p>
          )}
          {!isZeroSSL && (
            <p className="text-sm text-ink-muted">
              {t("wizard.letsencryptNote")}
            </p>
          )}
          <Button loading={busy} disabled={disabled} onClick={issue}>
            {busy ? t("tls.changing") : t("tls.changeDomain")}
          </Button>
          <p className="text-xs text-ink-muted">
            {t("tls.takesSeconds")}
          </p>
        </div>
      </div>
    </div>
  );
}
