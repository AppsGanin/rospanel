// Global bottom-right toasts. A module-level store lets non-React code
// (notify.ts) push toasts without a hook; <Toaster/> renders them.
import { useEffect, useState } from 'react'
import { cn, IconClose } from './ui'

export type Toast = {
  id: number
  message: string
  title?: string
  color: 'red' | 'teal'
}

let seq = 1
let toasts: Toast[] = []
const listeners = new Set<(t: Toast[]) => void>()

function emit() {
  for (const l of listeners) l(toasts)
}

export function pushToast(t: Omit<Toast, 'id'>, autoClose = 4500) {
  const id = seq++
  toasts = [...toasts, { ...t, id }]
  emit()
  if (autoClose) setTimeout(() => dismissToast(id), autoClose)
}

export function dismissToast(id: number) {
  toasts = toasts.filter((t) => t.id !== id)
  emit()
}

export function Toaster() {
  const [items, setItems] = useState<Toast[]>(toasts)
  useEffect(() => {
    listeners.add(setItems)
    return () => {
      listeners.delete(setItems)
    }
  }, [])
  return (
    <div className="fixed bottom-4 right-4 z-300 flex w-90 max-w-[92vw] flex-col gap-2">
      {items.map((t) => (
        <div
          key={t.id}
          className={cn(
            'flex animate-slide-in-right items-start gap-3 rounded-xl border bg-white p-3 shadow-lg',
            t.color === 'red' ? 'border-brandred-200' : 'border-emerald-200',
          )}
        >
          <span
            className={cn(
              'mt-0.5 h-2.5 w-2.5 shrink-0 rounded-full',
              t.color === 'red' ? 'bg-brandred-500' : 'bg-emerald-500',
            )}
          />
          <div className="min-w-0 grow">
            {t.title && <div className="text-sm font-semibold text-ink">{t.title}</div>}
            <div className="text-sm text-ink-muted wrap-break-word">{t.message}</div>
          </div>
          <button
            onClick={() => dismissToast(t.id)}
            className="text-gray-400 hover:text-gray-600"
          >
            <IconClose size={16} />
          </button>
        </div>
      ))}
    </div>
  )
}
