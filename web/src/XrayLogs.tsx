import { LogViewer } from './LogViewer'

function classify(l: string): string {
  if (/\[error\]|failed|panic|rejected/i.test(l)) return 'error'
  if (/\[warning\]/i.test(l)) return 'warning'
  if (/accepted/i.test(l)) return 'access'
  return 'other'
}

const COLORS: Record<string, string> = {
  error: 'text-red-600',
  warning: 'text-amber-600',
  access: 'text-emerald-600',
}

const FILTERS = [
  { value: 'all', label: 'Все' },
  { value: 'access', label: 'Доступ' },
  { value: 'warning', label: 'Предупр.' },
  { value: 'error', label: 'Ошибки' },
]

export function XrayLogs({ onClose }: { onClose: () => void }) {
  return (
    <LogViewer
      title="Логи Xray"
      streamUrl="api/xray/logs/stream"
      onClose={onClose}
      filters={FILTERS}
      classify={classify}
      colorOf={(c) => COLORS[c] ?? 'text-gray-700'}
    />
  )
}
