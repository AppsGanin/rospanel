// Brand logo: shows the custom uploaded logo + panel name when configured,
// otherwise the bundled "РосПанель" wordmark. The default mark is a bundled SVG
// import (Vite emits it under /assets/ with a relative URL, so it resolves under
// the per-install secret panel path — an absolute "/favicon.svg" would not).
import logoUrl from './assets/logo.svg'
import { useBrand } from './brand'

export function BrandLogo({ size = 30 }: { size?: number }) {
  const brand = useBrand()
  const isDefault = brand.panel_name === brand.default_name && !brand.has_custom_logo
  const imgSize = `${(size + 6) / 16}rem`
  const src = brand.has_custom_logo ? brand.logoURL : logoUrl

  // min-w-0 + truncate: in a cramped flex row (the desktop header) the wordmark must
  // give way by ellipsing, not by overflowing its box — an overflowing wordmark used
  // to render underneath the version badge sitting next to it. The mark itself never
  // shrinks, so it can't be squashed out of proportion.
  return (
    <span className="inline-flex min-w-0 items-center gap-2.5">
      <img
        src={src}
        className="shrink-0"
        style={{ width: imgSize, height: imgSize, objectFit: 'contain' }}
        alt=""
      />
      <span
        className="min-w-0 truncate font-extrabold leading-none tracking-tight text-accent"
        style={{ fontSize: `${(size * 0.72) / 16}rem` }}
      >
        {isDefault ? 'РосПанель' : brand.panel_name}
      </span>
    </span>
  )
}
