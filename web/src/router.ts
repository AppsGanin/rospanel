// Tiny History-API router. Routes hang off the SPA's "/<secret>/" base href
// (injected by the Go server), so the app never needs to know its secret path.
// navigate() pushes a new URL and notifies all useRoute() subscribers; the
// browser's back/forward fire popstate, which does the same.
import { useEffect, useState } from 'react'

const BASE = new URL(document.baseURI).pathname

// currentPath returns the path relative to BASE, e.g. "settings/dns".
function currentPath(): string {
  let p = window.location.pathname
  if (p.startsWith(BASE)) p = p.slice(BASE.length)
  return p.replace(/^\/+|\/+$/g, '')
}

const listeners = new Set<() => void>()

// navigate switches to a route relative to BASE ("" = root). No-op if unchanged.
export function navigate(rel: string) {
  const path = BASE + rel.replace(/^\/+/, '')
  if (path === window.location.pathname) return
  window.history.pushState(null, '', path)
  listeners.forEach((l) => l())
}

// useRoute returns the current path split into segments and re-renders on any
// navigation (pushState via navigate, or browser back/forward).
export function useRoute(): string[] {
  const [path, setPath] = useState(currentPath)
  useEffect(() => {
    const update = () => setPath(currentPath())
    listeners.add(update)
    window.addEventListener('popstate', update)
    return () => {
      listeners.delete(update)
      window.removeEventListener('popstate', update)
    }
  }, [])
  return path.split('/').filter(Boolean)
}
