import { useTranslation } from 'react-i18next'
import { type SubDPI } from './api'
import { Card, Select, TextInput, ToggleRow } from './ui'

// SubDPICard is the client-side DPI evasion block: what the subscription tells
// Xray-core apps (through the Xray JSON format) and sing-box to do with the TLS
// handshake before a DPI box sees it. The server changes nothing; every switch here
// reaches the clients on their next subscription refresh.
export function SubDPICard({
  value: d,
  onChange,
}: {
  value: SubDPI
  onChange: (v: SubDPI) => void
}) {
  const { t } = useTranslation()
  const patch = (p: Partial<SubDPI>) => onChange({ ...d, ...p })

  const packets = [
    { value: 'tlshello', label: t('subs.dpi.packetsTlshello') },
    { value: '1-1', label: t('subs.dpi.packets11') },
    { value: '1-3', label: t('subs.dpi.packets13') },
  ]
  const noiseTypes = [
    { value: 'rand', label: t('subs.dpi.noiseRand') },
    { value: 'str', label: t('subs.dpi.noiseStr') },
    { value: 'base64', label: t('subs.dpi.noiseBase64') },
  ]
  // The Xray-side switches only do anything when Xray-core clients receive JSON —
  // say so next to them rather than letting an operator wonder why nothing changed.
  const xrayOffNote = (d.fragment || d.noise) && !d.json_clients

  return (
    <Card className="p-4">
      <h3 className="mb-1 font-bold text-ink">{t('subs.dpi.title')}</h3>
      <p className="mb-3 text-xs text-ink-muted">{t('subs.dpi.intro')}</p>
      <div className="flex flex-col gap-3">
        <ToggleRow
          label={t('subs.dpi.jsonClients')}
          hint={t('subs.dpi.jsonClientsHint')}
          checked={d.json_clients}
          onChange={(v) => patch({ json_clients: v })}
        />
        <ToggleRow
          label={t('subs.dpi.fragment')}
          hint={t('subs.dpi.fragmentHint')}
          checked={d.fragment}
          onChange={(v) => patch({ fragment: v })}
        />
        {d.fragment && (
          <div className="grid grid-cols-1 gap-2 pl-3 sm:grid-cols-3">
            <Select
              label={t('subs.dpi.packets')}
              data={packets}
              value={d.fragment_packets}
              onChange={(v) => patch({ fragment_packets: v })}
            />
            <TextInput
              label={t('subs.dpi.length')}
              placeholder="100-200"
              value={d.fragment_length}
              onChange={(v) => patch({ fragment_length: v })}
            />
            <TextInput
              label={t('subs.dpi.interval')}
              placeholder="10-20"
              value={d.fragment_interval}
              onChange={(v) => patch({ fragment_interval: v })}
            />
          </div>
        )}
        <ToggleRow
          label={t('subs.dpi.noise')}
          hint={t('subs.dpi.noiseHint')}
          checked={d.noise}
          onChange={(v) => patch({ noise: v })}
        />
        {d.noise && (
          <div className="grid grid-cols-1 gap-2 pl-3 sm:grid-cols-3">
            <Select
              label={t('subs.dpi.noiseType')}
              data={noiseTypes}
              value={d.noise_type}
              onChange={(v) => patch({ noise_type: v })}
            />
            <TextInput
              label={d.noise_type === 'rand' ? t('subs.dpi.noiseLength') : t('subs.dpi.noisePayload')}
              placeholder={d.noise_type === 'rand' ? '10-20' : ''}
              value={d.noise_packet}
              onChange={(v) => patch({ noise_packet: v })}
            />
            <TextInput
              label={t('subs.dpi.noiseDelay')}
              placeholder="10-16"
              value={d.noise_delay}
              onChange={(v) => patch({ noise_delay: v })}
            />
          </div>
        )}
        {xrayOffNote && (
          <p className="warning-tint rounded-lg px-2.5 py-1.5 text-xs text-warning">
            {t('subs.dpi.jsonOffWarning')}
          </p>
        )}
        <ToggleRow
          label={t('subs.dpi.recordFragment')}
          hint={t('subs.dpi.recordFragmentHint')}
          checked={d.record_fragment}
          onChange={(v) => patch({ record_fragment: v })}
        />
      </div>
    </Card>
  )
}
