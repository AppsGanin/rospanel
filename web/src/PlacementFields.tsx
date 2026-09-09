import { useTranslation } from 'react-i18next'
import type { Placement } from './api'
import { fmtBytes } from './format'
import {
  cn,
  Mono,
  Section,
  Select,
  SettingRow,
  Switch,
  TextInput,
} from './ui'

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
    <>
      {/* Where this server sits in a subscription, and how many people it is meant
          to carry — with the live count in the header, so "full" is a number. */}
      <Section
        title={t('nodes.placement.title')}
        desc={t('nodes.placement.hint')}
        action={
          <Mono className={cn('text-[11px]', full ? 'text-warning' : 'text-ink-muted')}>
            {t('nodes.placement.online', { count: online })}
            {value.capacity > 0 ? ` / ${value.capacity}` : ''}
          </Mono>
        }
        flush
      >
        <SettingRow
          label={t('nodes.placement.weight')}
          field={
            <TextInput
              type="number"
              value={String(value.sort_weight)}
              onChange={(v) => patch({ sort_weight: num(v) })}
            />
          }
        />
        <SettingRow
          label={t('nodes.placement.capacity')}
          field={
            <TextInput
              type="number"
              value={String(value.capacity)}
              onChange={(v) => patch({ capacity: Math.max(0, num(v)) })}
              placeholder="0"
            />
          }
        />
        <SettingRow
          label={t('nodes.placement.hideWhenFull')}
          hint={t('nodes.placement.hideNeedsCapacity')}
          control={
            // Nothing to hide against without a capacity, so the switch is off the
            // table until there is one — the hint says why.
            <Switch
              checked={value.hide_when_full}
              disabled={value.capacity <= 0}
              onChange={(v) => patch({ hide_when_full: v })}
            />
          }
        />
      </Section>

      {/* The traffic cap. Its own section: capacity above is how many people the
          server carries, this is how much the hosting will let it carry. */}
      <Section
        title={t('nodes.traffic.title')}
        desc={t('nodes.traffic.hint')}
        action={
          trafficUsed !== undefined ? (
            <Mono className={cn('text-[11px]', over ? 'text-warning' : 'text-ink-muted')}>
              {fmtBytes(trafficUsed)}
              {value.traffic_limit > 0 ? ` / ${fmtBytes(value.traffic_limit)}` : ''}
            </Mono>
          ) : undefined
        }
        flush
      >
        <SettingRow
          label={t('nodes.traffic.limit')}
          field={
            <TextInput
              value={gbOf(value.traffic_limit)}
              onChange={(v) => patch({ traffic_limit: bytesOf(v) })}
              placeholder={t('nodes.traffic.noLimit')}
            />
          }
        />
        <SettingRow
          label={t('nodes.traffic.period')}
          field={
            <Select
              data={[
                { value: 'month', label: t('nodes.traffic.perMonth') },
                { value: 'day', label: t('nodes.traffic.perDay') },
              ]}
              value={value.traffic_period || 'month'}
              disabled={value.traffic_limit <= 0}
              onChange={(v) => patch({ traffic_period: v })}
            />
          }
        />
        <SettingRow
          label={t('nodes.traffic.hideWhenOver')}
          hint={t('nodes.traffic.hideHint')}
          control={
            <Switch
              checked={value.hide_when_over}
              disabled={value.traffic_limit <= 0}
              onChange={(v) => patch({ hide_when_over: v })}
            />
          }
        />
      </Section>
    </>
  )
}
