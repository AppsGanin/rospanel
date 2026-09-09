import { useCallback } from "react";
import { useTranslation } from "react-i18next";
import { getUserEvents } from "./api";
import { EventList } from "./events";
import { Modal } from "./ui";

// The per-user audit trail, opened from the user detail. It nests inside the
// UserDetail modal — the shared escape stack closes this one first (LIFO).
export function UserEventsModal({
  userID,
  userName,
  open,
  onClose,
}: {
  userID: number;
  userName: string;
  open: boolean;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  // Memoized so EventList refetches only when the user changes, not on every render
  // of the parent.
  const load = useCallback(
    (before: number) => getUserEvents(userID, before),
    [userID],
  );
  return (
    <Modal
      open={open}
      onClose={onClose}
      size="lg"
      title={`${t("users.tabEvents")} · ${userName}`}
    >
      {/* Edge to edge: a table's rows carry their own padding, and the dialog's
          would inset every one of them. */}
      <div className="-m-4">
        <EventList table load={load} empty={t("events.emptyForUser")} />
      </div>
    </Modal>
  );
}
