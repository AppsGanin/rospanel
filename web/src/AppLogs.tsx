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
  error: 'text-red-600',
  warning: 'text-amber-600',
  info: 'text-emerald-600',
}

const FILTERS = [
  { value: 'all', label: 'Все' },
  { value: 'info', label: 'Инфо' },
  { value: 'warning', label: 'Предупр.' },
  { value: 'error', label: 'Ошибки' },
]

export function AppLogs({ onClose }: { onClose: () => void }) {
  return (
    <LogViewer
      title="Логи панели"
      streamUrl="api/logs/stream"
      onClose={onClose}
      filters={FILTERS}
      classify={classify}
      colorOf={(c) => COLORS[c] ?? 'text-gray-700'}
    />
  )
}
