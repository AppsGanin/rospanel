import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { setUserNote, setUserTags, listUserTags, type TagCount, type User } from './api'
import { useAction } from './hooks'
import { notifySuccess } from './notify'
import {
  useReadOnly, Button, SettingRow, TagsInput, Textarea } from './ui'
import i18n from './i18n'

// GroupChip is one access group in the user drawer. A solid chip ("on") is a group the
// user belongs to — clicking it (the ×) leaves; a dashed chip ("add") is one they can
// join — clicking (the ＋) adds. `count` is how many connections the group grants, shown
// so an operator can tell a rich group from an empty (access-revoking) one at a glance.
// TAG_MAX_LEN mirrors model.MaxUserTagLen for the hint text; the server is the one
// that enforces it.
const TAG_MAX_LEN = 32

// NoteAndTags is the operator's own annotation of the account: a free-text note and
// the tag list the user list filters on. Tags save on every change — a cheap write
// with no Xray reload — while the note, being typed rather than picked, saves on a
// button so half a sentence never lands in the journal.
export function NoteAndTags({ user, onChanged }: { user: User; onChanged: () => void }) {
  const { t } = useTranslation()
  const [note, setNote] = useState(user.note ?? '')
  const [known, setKnown] = useState<TagCount[]>([])
  const { busy, run } = useAction()
  const tags = user.tags ?? []
  const tagKey = tags.join(',')

  // biome-ignore lint/correctness/useExhaustiveDependencies: re-seeds the draft when the card switches user or the server returns a new note; user.id keeps a same-note switch from keeping the previous card's edits
  useEffect(() => {
    setNote(user.note ?? '')
  }, [user.id, user.note])

  // Every tag in use, as suggestions — so the second user tagged "vip" gets the
  // same spelling as the first without retyping it. Refetched when this user's
  // tags change, since that is when the set of known tags can grow.
  // biome-ignore lint/correctness/useExhaustiveDependencies: refetched when this user's tags change — tagKey is the compared form of an array that is a new object every render
  useEffect(() => {
    let alive = true
    listUserTags()
      .then((l) => alive && setKnown(l))
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [user.id, tagKey])

  const saved = user.note ?? ''
  const noteDirty = note.trim() !== saved.trim()
  const saveNote = () =>
    run(async () => {
      await setUserNote(user.id, note)
      onChanged()
      notifySuccess(t('userDetail.noteSaved'))
    })
  const saveTags = (next: string[]) =>
    run(async () => {
      await setUserTags(user.id, next)
      onChanged()
    })

  return (
    <>
      <SettingRow
        label={t('userDetail.tags')}
        hint={t('userDetail.tagsHint', { maxLen: TAG_MAX_LEN })}
      >
        <TagsInput
          value={tags}
          onChange={saveTags}
          options={known.map((k) => ({ value: k.tag, label: k.tag }))}
        />
      </SettingRow>
      <SettingRow
        label={t('userDetail.note')}
        control={
          noteDirty ? (
            <span className="flex gap-2">
              <Button
                size="xs"
                variant="light"
                color="gray"
                disabled={busy}
                onClick={() => setNote(saved)}
              >
                {t('common.cancel')}
              </Button>
              <Button size="xs" loading={busy} onClick={saveNote}>
                {t('common.save')}
              </Button>
            </span>
          ) : undefined
        }
      >
        <Textarea
          value={note}
          onChange={setNote}
          rows={2}
          placeholder={t('userDetail.notePlaceholder')}
        />
      </SettingRow>
    </>
  )
}

export function GroupChip({
  name,
  count,
  state,
  onClick,
}: {
  name: string
  count: number
  state: 'on' | 'add'
  onClick: () => void
}) {
  const on = state === 'on'
  const ro = useReadOnly()
  return (
    <button
      type="button"
      disabled={ro}
      onClick={onClick}
      title={`${i18n.t('userDetail.nConnections', { count })} · ${i18n.t(on ? 'userDetail.removeFromGroup' : 'userDetail.addToGroup')}`}
      className={
        'inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium transition ' +
        // Selected uses the theme-composited accent tint (translucent → readable on a
        // dark surface too), NOT a fixed bg-brand-NN shade, which would bake a light
        // fill that stays light in the dark theme. Mirrors the Checkbox checked state.
        (on
          ? 'border-accent accent-tint text-accent hover:border-brand-500'
          : 'border-dashed border-gray-300 bg-white text-ink-muted hover:border-brand-400 hover:text-accent')
      }
    >
      {!on && (
        <svg width="10" height="10" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
          <path d="M6 2v8M2 6h8" />
        </svg>
      )}
      <span className="max-w-40 truncate">{name}</span>
      <span className="opacity-70">· {count}</span>
      {on && (
        <svg width="10" height="10" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
          <path d="M3 3l6 6M9 3l-6 6" />
        </svg>
      )}
    </button>
  )
}
