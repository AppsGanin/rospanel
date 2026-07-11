import { useCallback } from "react";
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
      title={`Журнал · ${userName}`}
    >
      <EventList
        load={load}
        empty="По этому пользователю пока нет событий"
      />
    </Modal>
  );
}
