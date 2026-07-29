import i18n from './i18n'
import { LogViewer } from './LogViewer'

// classify a panel log line by its leveled tag ([INFO]/[WARN]/[ERROR]) with a
// heuristic fallback for lines logged before the leveled helpers existed.
function classify(l: string): string {
  if (/\[ERROR\]|panic|failed|error/i.test(l)) return 'error'
  if (/\[WARN\]|warning/i.test(l)) return 'warning'
  if (/\[INFO\]/i.test(l)) return 'info'
  return 'other'
}

const COLORS: Record<string, string> = {
  error: 'text-danger',
  warning: 'text-warning',
  info: 'text-success',
}

const FILTERS = () => [
  { value: 'all', label: i18n.t('logs.all') },
  { value: 'info', label: i18n.t('logs.info') },
  { value: 'warning', label: i18n.t('logs.warning') },
  { value: 'error', label: i18n.t('logs.error') },
]

export function AppLogs({ onClose }: { onClose: () => void }) {
  return (
    <LogViewer
      title={i18n.t('logs.panelTitle')}
      streamUrl="api/logs/stream"
      onClose={onClose}
      filters={FILTERS()}
      classify={classify}
      colorOf={(c) => COLORS[c] ?? 'text-gray-700'}
    />
  )
}
