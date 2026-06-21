// Brand logo: the mark is a bundled SVG import (Vite emits it under /assets/ with
// a relative URL, so it resolves under the per-install secret panel path — an
// absolute "/favicon.svg" would not), next to the "РосПанель" wordmark.
import logoUrl from './assets/logo.svg'

export function BrandLogo({ size = 30 }: { size?: number }) {
  return (
    <span className="inline-flex items-center gap-2.5">
      <img src={logoUrl} style={{ width: `${(size + 6) / 16}rem`, height: `${(size + 6) / 16}rem` }} alt="" />
      <span
        className="font-extrabold leading-none tracking-tight"
        style={{ fontSize: `${(size * 0.72) / 16}rem` }}
      >
        <span className="text-brandred-500">Рос</span>
        <span className="text-brand-600">Панель</span>
      </span>
    </span>
  )
}
