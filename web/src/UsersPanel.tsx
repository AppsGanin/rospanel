import { QRCodeSVG } from "qrcode.react";
import { useCallback, useEffect, useState } from "react";
import {
  createUser,
  listUsers,
  setResetPeriod,
  setUserEnabled,
  type User,
} from "./api";
import { useAction } from "./hooks";
import {
  fmtExpire,
  fmtQuota,
  gbToBytes,
  isOnline,
  QUOTA_OPTIONS,
  RESET_PERIODS,
  statusInfo,
} from "./format";
import { errMessage, notifyError } from "./notify";
import {
  Badge,
  Button,
  Card,
  Code,
  DatePicker,
  Modal,
  Select,
  Skeleton,
  Switch,
  TextInput,
  useCopy,
} from "./ui";
import { UserDetail } from "./UserDetail";

function UsersSkeleton() {
  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
      {[...Array(4)].map((_, i) => (
        <Card key={i} className="p-4">
          <div className="mb-3 flex items-center gap-2">
            <Skeleton className="h-5 w-9 rounded-full" />
            <Skeleton className="h-4 w-32" />
          </div>
          <div className="mb-3 flex gap-2">
            <Skeleton className="h-5 w-16 rounded-full" />
            <Skeleton className="h-5 w-14 rounded-full" />
            <Skeleton className="h-5 w-20 rounded-full" />
          </div>
          <div className="flex gap-2">
            <Skeleton className="h-8 flex-1 rounded-lg" />
            <Skeleton className="h-8 flex-1 rounded-lg" />
            <Skeleton className="h-8 w-8 rounded-lg" />
          </div>
        </Card>
      ))}
    </div>
  );
}

export function UsersPanel() {
  const [users, setUsers] = useState<User[]>([]);
  const [addOpen, setAddOpen] = useState(false);
  const [detail, setDetail] = useState<User | null>(null);
  const [loaded, setLoaded] = useState(false);

  const refresh = useCallback(() => {
    listUsers()
      .then((us) => {
        setUsers(us);
        setDetail((d) => (d ? (us.find((x) => x.id === d.id) ?? d) : d));
      })
      .catch(() => {})
      .finally(() => setLoaded(true));
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  if (!loaded) return <UsersSkeleton />;

  return (
    <>
      {users.length === 0 ? (
        <p className="py-12 text-center text-ink-muted">
          Пока нет пользователей. Нажмите кнопку «+» внизу справа.
        </p>
      ) : (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          {users.map((u) => {
            const st = statusInfo(u.status);
            return (
              <Card key={u.id} className="p-4">
                <div className="mb-3 flex min-w-0 items-center gap-2">
                  <Switch
                    checked={u.enabled}
                    onChange={(v) =>
                      setUserEnabled(u.id, v)
                        .then(refresh)
                        .catch((e) => notifyError(errMessage(e)))
                    }
                  />
                  <span
                    className={`flex-1 truncate font-medium ${
                      u.status === "active" ? "text-ink" : "text-ink-muted"
                    }`}
                  >
                    {u.name}
                  </span>
                </div>

                <div className="mb-3 flex flex-wrap gap-2">
                  <Badge color={st.color as never}>{st.label}</Badge>
                  <Badge color={isOnline(u.last_seen) ? "greenSolid" : "gray"}>
                    {isOnline(u.last_seen) ? "● онлайн" : "офлайн"}
                  </Badge>
                  <Badge color="brand">
                    {fmtQuota(u.used_up + u.used_down, u.data_limit)}
                  </Badge>
                  {u.expire_at > 0 && (
                    <Badge color="gray">до {fmtExpire(u.expire_at)}</Badge>
                  )}
                  {u.device_limit > 0 && (
                    <Badge color={u.status === "device_limited" ? "orange" : "gray"}>
                      {u.active_devices}/{u.device_limit} устр.
                    </Badge>
                  )}
                </div>

                <div className="flex gap-2 mt-auto">
                  <Button
                    size="sm"
                    variant="light"
                    href={u.sub_url}
                    target="_blank"
                    className="flex-1"
                  >
                    Подписка
                  </Button>
                  <Button
                    size="sm"
                    variant="light"
                    color="gray"
                    onClick={() => setDetail(u)}
                    className="flex-1"
                  >
                    Подробнее
                  </Button>
                </div>
              </Card>
            );
          })}
        </div>
      )}

      {/* Floating action button to add a user. */}
      <button
        onClick={() => setAddOpen(true)}
        aria-label="Добавить пользователя"
        title="Добавить пользователя"
        className="fixed bottom-6 right-6 z-40 flex h-14 w-14 items-center justify-center rounded-full bg-brand-600 text-white shadow-lg transition hover:bg-brand-700 hover:shadow-xl active:scale-95"
      >
        <svg
          width="26"
          height="26"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2.5"
          strokeLinecap="round"
        >
          <path d="M12 5v14M5 12h14" />
        </svg>
      </button>

      <AddUser
        opened={addOpen}
        onClose={() => {
          setAddOpen(false);
          refresh();
        }}
      />

      <UserDetail
        user={detail}
        onChanged={refresh}
        onClose={() => {
          setDetail(null);
          refresh();
        }}
      />
    </>
  );
}

function AddUser({
  opened,
  onClose,
}: {
  opened: boolean;
  onClose: () => void;
}) {
  const [name, setName] = useState("");
  const [limitGb, setLimitGb] = useState("0");
  const [resetPeriod, setResetPeriodState] = useState("none");
  const [expDate, setExpDate] = useState("");
  const [created, setCreated] = useState<User | null>(null);
  const { busy, run } = useAction();
  const { copied, copy } = useCopy();

  const submit = async () => {
    if (!name.trim()) return;
    run(async () => {
      const dl = gbToBytes(Number(limitGb) || 0);
      const ea = expDate ? Math.floor(new Date(expDate).getTime() / 1000) : 0;
      const u = await createUser(name.trim(), dl, ea);
      if (resetPeriod !== "none") await setResetPeriod(u.id, resetPeriod);
      setCreated(u);
    });
  };

  const close = () => {
    setName("");
    setLimitGb("0");
    setResetPeriodState("none");
    setExpDate("");
    setCreated(null);
    onClose();
  };

  return (
    <Modal
      open={opened}
      onClose={close}
      title={created ? "Пользователь создан" : "Новый пользователь"}
    >
      {!created ? (
        <div className="flex flex-col gap-3">
          <TextInput
            label="Имя"
            placeholder="например, Андрей"
            value={name}
            onChange={setName}
            autoFocus
          />
          <div className="grid grid-cols-2 gap-3">
            <DatePicker
              label="Действует до"
              value={expDate}
              onChange={setExpDate}
              min={new Date().toISOString().slice(0, 10)}
            />
            <Select
              label="Лимит трафика"
              data={QUOTA_OPTIONS}
              value={limitGb}
              onChange={setLimitGb}
            />
          </div>
          <Select
            label="Автосброс трафика"
            data={RESET_PERIODS}
            value={resetPeriod}
            onChange={setResetPeriodState}
          />
          <Button loading={busy} onClick={submit}>
            Создать и показать ссылку
          </Button>
        </div>
      ) : (
        <div className="flex flex-col items-center gap-3">
          <p className="text-sm text-ink-muted">
            Подписка (все протоколы) — отсканируйте или поделитесь ссылкой
          </p>
          <div className="rounded-lg bg-white p-3">
            <QRCodeSVG value={created.sub_url} size={200} />
          </div>
          <Code block className="w-full">
            {created.sub_url}
          </Code>
          <Button
            fullWidth
            color={copied ? "teal" : "brand"}
            onClick={() => copy(created.sub_url)}
          >
            {copied ? "Скопировано" : "Скопировать ссылку подписки"}
          </Button>
          <Button
            variant="light"
            fullWidth
            href={created.sub_url}
            target="_blank"
          >
            Открыть подписку
          </Button>
          <Button variant="subtle" color="gray" fullWidth onClick={close}>
            Готово
          </Button>
        </div>
      )}
    </Modal>
  );
}
