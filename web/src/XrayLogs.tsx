import i18n from './i18n'
import { LogViewer } from './LogViewer'

function classify(l: string): string {
  if (/\[error\]|failed|panic|rejected/i.test(l)) return 'error'
  if (/\[warning\]/i.test(l)) return 'warning'
  if (/accepted/i.test(l)) return 'access'
  return 'other'
}

const COLORS: Record<string, string> = {
  error: 'text-danger',
  warning: 'text-warning',
  access: 'text-success',
}

const FILTERS = () => [
  { value: 'all', label: i18n.t('logs.all') },
  { value: 'access', label: i18n.t('logs.access') },
  { value: 'warning', label: i18n.t('logs.warning') },
  { value: 'error', label: i18n.t('logs.error') },
]

export function XrayLogs({ onClose }: { onClose: () => void }) {
  return (
    <LogViewer
      title={i18n.t('logs.xrayTitle')}
      streamUrl="api/xray/logs/stream"
      onClose={onClose}
      filters={FILTERS()}
      classify={classify}
      colorOf={(c) => COLORS[c] ?? 'text-gray-700'}
    />
  )
}
