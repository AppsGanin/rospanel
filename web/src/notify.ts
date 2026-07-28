import { ApiError } from './api'
import i18n, { td } from './i18n'
import { pushToast } from './toast'

// Centralized toasts (rendered bottom-right by <Toaster/> in main.tsx).

export function notifyError(message: string) {
  pushToast({ color: 'red', title: i18n.t('common.error'), message }, 5000)
}

export function notifySuccess(message: string) {
  pushToast({ color: 'teal', message }, 3500)
}

// errMessage extracts a human string from an unknown thrown value.
//
// A server error carries a dictionary code, and that is what gets rendered: the
// panel's language is a per-browser choice the server cannot see, so it sends the
// code and the panel words it. e.message is the fallback for a code this build has
// no entry for — an older panel against a newer server still shows a sentence
// rather than "err.tokenRequired".
//
// The default fallback is resolved at call time, not at module load, so it follows
// the language the admin is actually looking at.
export function errMessage(e: unknown, fallback?: string): string {
  if (e instanceof ApiError && e.code) {
    const translated = td(e.code, e.args)
    if (translated !== e.code) return translated
  }
  if (e instanceof Error) return e.message
  return fallback ?? i18n.t('common.error')
}
