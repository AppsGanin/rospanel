import { useEffect, useState } from "react";
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
    const t = target.trim();
    setBusy(true);
    try {
      const s = await save(t, email.trim(), provider);
      setStatus(s);
      if (redirectOnSuccess) {
        notifySuccess("Домен изменён — переходим на новый адрес…");
        setTimeout(() => {
          window.location.href = `https://${t}${window.location.pathname}${window.location.hash}`;
        }, 2500);
      } else {
        notifySuccess("Домен изменён — сертификат перевыпустится на новом адресе");
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
      ? "валидный (ZeroSSL)"
      : "валидный (Let's Encrypt)"
    : "временный";

  const isZeroSSL = provider === "zerossl";
  const t = target.trim();
  const e = email.trim();
  const targetErr = t !== "" && !isValidACMETarget(t, isZeroSSL);
  const emailErr = e !== "" && !isValidEmail(e);
  const emailMissing = isZeroSSL && e === "";
  const disabled = t === "" || targetErr || emailErr || emailMissing;

  return (
    <div className="flex flex-col gap-3">
      <div className="rounded-xl border border-gray-200/80 bg-gray-50/60 p-4">
        <div className="flex items-center justify-between gap-3">
          <div>
            <p className="text-sm text-ink-muted">Текущий адрес</p>
            <p className="text-lg font-bold text-ink">
              {status?.domain || "—"}
            </p>
            {cert && (
              <p className="mt-1 text-sm text-ink-muted">
                сертификат: {cert.issuer || "—"} · ещё {cert.days_left} дн.
              </p>
            )}
          </div>
          {cert && <Badge color={valid ? "teal" : "orange"}>{certLabel}</Badge>}
        </div>
      </div>

      <div className="rounded-xl border border-gray-200/80 bg-gray-50/60 p-4">
        <div className="flex flex-col gap-3">
          <p className="font-semibold">Сменить домен</p>
          <p className="text-sm text-ink-muted">
            Укажи <b>домен</b>, направленный на этот сервер,{" "}
            <b>или IP сервера</b>. Должен быть открыт порт <b>80</b>. После
            смены {redirectOnSuccess ? "панель и подписки" : "нода и её ссылки"}{" "}
            начнут использовать новый адрес.
          </p>
          <div>
            <TextInput
              label={isZeroSSL ? "Новый домен" : "Новый домен или IP"}
              placeholder={
                isZeroSSL
                  ? "vpn.example.com"
                  : "vpn.example.com или 144.31.159.81"
              }
              value={target}
              onChange={setTarget}
            />
            {targetErr && (
              <p className="mt-1 text-xs text-danger">
                {isZeroSSL
                  ? "Введите домен (ZeroSSL не выдаёт сертификаты на IP)."
                  : "Введите корректный домен или IP-адрес."}
              </p>
            )}
          </div>
          <div>
            <TextInput
              label={
                isZeroSSL
                  ? "E-mail (обязательно для ZeroSSL)"
                  : "E-mail (необязательно)"
              }
              placeholder="you@example.com"
              value={email}
              onChange={setEmail}
            />
            {emailErr && (
              <p className="mt-1 text-xs text-danger">
                Введите корректный e-mail.
              </p>
            )}
          </div>
          <Select
            label="Центр сертификации"
            value={provider}
            onChange={setProvider}
            data={[
              { value: "letsencrypt", label: "Let's Encrypt" },
              { value: "zerossl", label: "ZeroSSL" },
            ]}
          />
          {isZeroSSL && (
            <p className="text-sm text-ink-muted">
              ZeroSSL поддерживает только домены. EAB-ключи будут получены
              автоматически по указанному e-mail.
            </p>
          )}
          {!isZeroSSL && (
            <p className="text-sm text-ink-muted">
              Let's Encrypt выдаёт сертификаты на домены и IP (на IP ~6 дней,
              продлеваются автоматически).
            </p>
          )}
          <Button loading={busy} disabled={disabled} onClick={issue}>
            {busy ? "Меняю домен…" : "Сменить домен"}
          </Button>
          <p className="text-xs text-ink-muted">
            Занимает 10–30 секунд (проверка через порт 80).
          </p>
        </div>
      </div>
    </div>
  );
}
