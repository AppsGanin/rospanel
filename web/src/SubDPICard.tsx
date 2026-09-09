import { useTranslation } from 'react-i18next'
import { type SubDPI } from './api'
import { Panel, Select, SettingRow, Switch, TextInput, ToggleRow } from './ui'

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
    <Panel title={t('subs.dpi.title')}>
      <SettingRow hint={t('subs.dpi.intro')} />
      <ToggleRow
        label={t('subs.dpi.jsonClients')}
        hint={t('subs.dpi.jsonClientsHint')}
        checked={d.json_clients}
        onChange={(v) => patch({ json_clients: v })}
      />
      <SettingRow
        label={t('subs.dpi.fragment')}
        hint={t('subs.dpi.fragmentHint')}
        control={
          <Switch checked={d.fragment} onChange={(v) => patch({ fragment: v })} />
        }
      >
        {d.fragment && (
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
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
      </SettingRow>
      <SettingRow
        label={t('subs.dpi.noise')}
        hint={t('subs.dpi.noiseHint')}
        control={<Switch checked={d.noise} onChange={(v) => patch({ noise: v })} />}
      >
        {d.noise && (
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
            <Select
              label={t('subs.dpi.noiseType')}
              data={noiseTypes}
              value={d.noise_type}
              onChange={(v) => patch({ noise_type: v })}
            />
            <TextInput
              label={
                d.noise_type === 'rand'
                  ? t('subs.dpi.noiseLength')
                  : t('subs.dpi.noisePayload')
              }
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
      </SettingRow>
      {/* The Xray-side switches only do anything when Xray-core clients receive JSON —
          say so next to them rather than letting an operator wonder why nothing changed. */}
      {xrayOffNote && (
        <SettingRow
          hint={<span className="text-warning">{t('subs.dpi.jsonOffWarning')}</span>}
        />
      )}
      <ToggleRow
        label={t('subs.dpi.recordFragment')}
        hint={t('subs.dpi.recordFragmentHint')}
        checked={d.record_fragment}
        onChange={(v) => patch({ record_fragment: v })}
      />
    </Panel>
  )
}
