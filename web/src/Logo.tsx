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

  return (
    <span className="inline-flex items-center gap-2.5">
      <img
        src={src}
        style={{ width: imgSize, height: imgSize, objectFit: 'contain' }}
        alt=""
      />
      {isDefault ? (
        <span
          className="font-extrabold leading-none tracking-tight text-accent"
          style={{ fontSize: `${(size * 0.72) / 16}rem` }}
        >
          РосПанель
        </span>
      ) : (
        <span
          className="font-extrabold leading-none tracking-tight text-accent"
          style={{ fontSize: `${(size * 0.72) / 16}rem` }}
        >
          {brand.panel_name}
        </span>
      )}
    </span>
  )
}
