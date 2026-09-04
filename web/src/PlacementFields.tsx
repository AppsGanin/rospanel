import { useTranslation } from 'react-i18next'
import type { Placement } from './api'
import { fmtBytes } from './format'
import { Badge, Checkbox, Select, TextInput } from './ui'

// placementOf lifts a server's placement out of the node view it arrives in.
export function placementOf(n: {
  country?: string
  sort_weight?: number
  capacity?: number
  hide_when_full?: boolean
  traffic_limit?: number
  traffic_period?: string
  hide_when_over?: boolean
}): Placement {
  return {
    country: n.country ?? '',
    sort_weight: n.sort_weight ?? 0,
    capacity: n.capacity ?? 0,
    hide_when_full: n.hide_when_full ?? false,
    traffic_limit: n.traffic_limit ?? 0,
    traffic_period: n.traffic_period || 'month',
    hide_when_over: n.hide_when_over ?? false,
  }
}

const GB = 1024 ** 3

// gbOf / bytesOf keep the field in gigabytes, which is the unit hosting quotes.
const gbOf = (bytes: number) => (bytes > 0 ? String(Math.round((bytes / GB) * 100) / 100) : '')
const bytesOf = (gb: string) => {
  const n = parseFloat(gb.replace(',', '.'))
  return Number.isFinite(n) && n > 0 ? Math.round(n * GB) : 0
}

// PlacementFields edits where a server sits in subscriptions: its country (blank
// = detect from the address on save), a manual weight, and the number of users it
// is meant to carry, with the live count next to it so "full" is a number the
// operator can see rather than guess.
export function PlacementFields({
  value,
  onChange,
  online,
  trafficUsed,
}: {
  value: Placement
  onChange: (p: Placement) => void
  online: number
  // What this server has carried in the current cap period, so the operator picks a
  // number against the real figure instead of guessing.
  trafficUsed?: number
}) {
  const { t } = useTranslation()
  const patch = (p: Partial<Placement>) => onChange({ ...value, ...p })
  const num = (s: string) => {
    const n = parseInt(s, 10)
    return Number.isFinite(n) ? n : 0
  }
  const full = value.capacity > 0 && online >= value.capacity
  const over = value.traffic_limit > 0 && (trafficUsed ?? 0) >= value.traffic_limit
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

      {/* Traffic cap. Separate block: capacity above is about how many people the
          server carries, this is about how much the hosting will let it carry. */}
      <div className="flex flex-col gap-2 border-t border-gray-200/70 pt-2">
        <div className="flex items-center justify-between gap-2">
          <span className="text-sm font-medium text-ink">{t('nodes.traffic.title')}</span>
          {trafficUsed !== undefined && (
            <Badge color={over ? 'orange' : 'gray'} size="xs">
              {fmtBytes(trafficUsed)}
              {value.traffic_limit > 0 ? ` / ${fmtBytes(value.traffic_limit)}` : ''}
            </Badge>
          )}
        </div>
        <p className="text-xs text-ink-muted">{t('nodes.traffic.hint')}</p>
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
          <TextInput
            label={t('nodes.traffic.limit')}
            value={gbOf(value.traffic_limit)}
            onChange={(v) => patch({ traffic_limit: bytesOf(v) })}
            placeholder={t('nodes.traffic.noLimit')}
          />
          <Select
            label={t('nodes.traffic.period')}
            data={[
              { value: 'month', label: t('nodes.traffic.perMonth') },
              { value: 'day', label: t('nodes.traffic.perDay') },
            ]}
            value={value.traffic_period || 'month'}
            disabled={value.traffic_limit <= 0}
            onChange={(v) => patch({ traffic_period: v })}
          />
        </div>
        <Checkbox
          label={t('nodes.traffic.hideWhenOver')}
          hint={value.traffic_limit > 0 ? t('nodes.traffic.hideHint') : t('nodes.traffic.hideNeedsLimit')}
          checked={value.hide_when_over}
          onChange={(v) => patch({ hide_when_over: v })}
        />
      </div>
    </div>
  )
}
