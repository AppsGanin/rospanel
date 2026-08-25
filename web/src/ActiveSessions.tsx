import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  type AdminSession,
  deleteAllAdminSessions,
  deleteAllMyOtherSessions,
  deleteAdminSession,
  deleteMySession,
  listAdminSessions,
  listMySessions,
} from "./api";
import { fmtLastSeen, parseUserAgent } from "./format";
import { currentLang } from "./i18n";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { Badge, Button, CenterLoader } from "./ui";

function fmtTs(unix: number): string {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleString(currentLang(), {
    dateStyle: "short",
    timeStyle: "short",
  });
}

export function ActiveSessions({
  adminId,
  onSessionRevoked,
}: {
  adminId?: number;
  onSessionRevoked?: () => void;
}) {
  const { t } = useTranslation();
  const [sessions, setSessions] = useState<AdminSession[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [busyHash, setBusyHash] = useState<string | null>(null);
  const [busyAll, setBusyAll] = useState(false);

  const fetchSessions = async () => {
    try {
      const res = adminId !== undefined
        ? await listAdminSessions(adminId)
        : await listMySessions();
      setSessions(res.sessions || []);
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchSessions();
  }, [adminId]);

  const handleRevokeOne = async (hash: string) => {
    setBusyHash(hash);
    try {
      if (adminId !== undefined) {
        await deleteAdminSession(adminId, hash);
      } else {
        await deleteMySession(hash);
      }
      notifySuccess(t("sessions.revokeSuccess"));
      await fetchSessions();
      onSessionRevoked?.();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusyHash(null);
    }
  };

  const handleRevokeAll = async () => {
    setBusyAll(true);
    try {
      if (adminId !== undefined) {
        await deleteAllAdminSessions(adminId);
      } else {
        await deleteAllMyOtherSessions();
      }
      notifySuccess(adminId !== undefined ? t("sessions.revokeSuccess") : t("sessions.revokeAllSuccess"));
      await fetchSessions();
      onSessionRevoked?.();
    } catch (e) {
      notifyError(errMessage(e));
    } finally {
      setBusyAll(false);
    }
  };

  if (loading) {
    return <CenterLoader />;
  }

  const list = sessions || [];
  const otherCount = list.filter((s) => !s.is_current).length;

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between">
        <div>
          <h4 className="text-sm font-semibold text-ink">{t("sessions.title")}</h4>
          <p className="text-xs text-ink-muted">{t("sessions.description")}</p>
        </div>
        {otherCount > 0 && (
          <Button
            size="sm"
            variant="light"
            color="red"
            loading={busyAll}
            onClick={handleRevokeAll}
          >
            {adminId !== undefined ? t("sessions.revokeAll") : t("sessions.revokeAllOther")}
          </Button>
        )}
      </div>

      {list.length === 0 ? (
        <div className="rounded-xl border border-gray-200 bg-gray-50/50 p-4 text-center text-xs text-ink-muted">
          {t("sessions.noSessions")}
        </div>
      ) : (
        <div className="flex flex-col gap-2 max-h-72 overflow-y-auto pr-1">
          {list.map((s) => {
            const isBusy = busyHash === s.token_hash;
            return (
              <div
                key={s.token_hash}
                className="flex items-center justify-between gap-3 rounded-xl border border-gray-200 bg-white p-2.5 transition hover:border-gray-300"
              >
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-1.5">
                    <span className="text-xs font-semibold text-ink">
                      {parseUserAgent(s.user_agent)}
                    </span>
                    {s.is_current && (
                      <Badge color="green">{t("sessions.thisDevice")}</Badge>
                    )}
                    <span className="font-mono text-xs text-ink-muted">
                      {s.ip || "—"}
                    </span>
                  </div>
                  <div className="mt-0.5 flex flex-wrap gap-x-2 text-[11px] text-ink-muted">
                    <span>{t("sessions.signedIn", { time: fmtTs(s.created_at) })}</span>
                    <span>·</span>
                    <span>{t("sessions.lastSeen", { time: fmtLastSeen(s.last_seen_at) })}</span>
                  </div>
                </div>

                <Button
                  size="sm"
                  variant="light"
                  color="red"
                  loading={isBusy}
                  onClick={() => handleRevokeOne(s.token_hash)}
                >
                  {t("sessions.revoke")}
                </Button>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
