import { pushToast } from './toast'

// Centralized toasts (rendered bottom-right by <Toaster/> in main.tsx).

export function notifyError(message: string) {
  pushToast({ color: 'red', title: 'Ошибка', message }, 5000)
}

export function notifySuccess(message: string) {
  pushToast({ color: 'teal', message }, 3500)
}

// errMessage extracts a human string from an unknown thrown value.
export function errMessage(e: unknown, fallback = 'Ошибка'): string {
  return e instanceof Error ? e.message : fallback
}
