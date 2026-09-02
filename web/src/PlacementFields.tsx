import { useTranslation } from 'react-i18next'
import type { Placement } from './api'
import { Badge, Checkbox, TextInput } from './ui'

// placementOf lifts a server's placement out of the node view it arrives in.
export function placementOf(n: {
  country?: string
  sort_weight?: number
  capacity?: number
  hide_when_full?: boolean
}): Placement {
  return {
    country: n.country ?? '',
    sort_weight: n.sort_weight ?? 0,
    capacity: n.capacity ?? 0,
    hide_when_full: n.hide_when_full ?? false,
  }
}

// PlacementFields edits where a server sits in subscriptions: its country (blank
// = detect from the address on save), a manual weight, and the number of users it
// is meant to carry, with the live count next to it so "full" is a number the
// operator can see rather than guess.
export function PlacementFields({
  value,
  onChange,
  online,
}: {
  value: Placement
  onChange: (p: Placement) => void
  online: number
}) {
  const { t } = useTranslation()
  const patch = (p: Partial<Placement>) => onChange({ ...value, ...p })
  const num = (s: string) => {
    const n = parseInt(s, 10)
    return Number.isFinite(n) ? n : 0
  }
  const full = value.capacity > 0 && online >= value.capacity
  return (
    <div className="flex flex-col gap-2 rounded-lg border border-gray-200/70 bg-gray-50/60 p-3">
      <div className="flex items-center justify-between gap-2">
        <span className="text-sm font-medium text-ink">{t('nodes.placement.title')}</span>
        <Badge color={full ? 'orange' : 'gray'} size="xs">
          {t('nodes.placement.online', { count: online })}
          {value.capacity > 0 ? ` / ${value.capacity}` : ''}
        </Badge>
      </div>
      <p className="text-xs text-ink-muted">{t('nodes.placement.hint')}</p>
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        <TextInput
          label={t('nodes.placement.weight')}
          type="number"
          value={String(value.sort_weight)}
          onChange={(v) => patch({ sort_weight: num(v) })}
        />
        <TextInput
          label={t('nodes.placement.capacity')}
          type="number"
          value={String(value.capacity)}
          onChange={(v) => patch({ capacity: Math.max(0, num(v)) })}
          placeholder="0"
        />
      </div>
      <Checkbox
        label={t('nodes.placement.hideWhenFull')}
        hint={value.capacity > 0 ? undefined : t('nodes.placement.hideNeedsCapacity')}
        checked={value.hide_when_full}
        onChange={(v) => patch({ hide_when_full: v })}
      />
    </div>
  )
}
