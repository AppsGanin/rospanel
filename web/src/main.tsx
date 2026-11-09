import React from 'react'
import ReactDOM from 'react-dom/client'
import '@fontsource/mulish/400.css'
import '@fontsource/mulish/500.css'
import '@fontsource/mulish/600.css'
import '@fontsource/mulish/700.css'
import '@fontsource/mulish/800.css'
// IBM Plex Mono carries every number, id, host, timestamp and version. Self-hosted
// like Mulish — Google Fonts is throttled in Russia, which is where the panel runs.
import '@fontsource/ibm-plex-mono/400.css'
import '@fontsource/ibm-plex-mono/500.css'
import '@fontsource/ibm-plex-mono/600.css'
import './index.css'
// i18next must be initialised (and the reader's dictionary loaded) before the first
// component renders, or t() returns raw keys on the very first paint.
import { ready } from './i18n'
import { App } from './App'
import { ErrorBoundary } from './ErrorBoundary'
import { Toaster } from './toast'

ready.then(() =>
  ReactDOM.createRoot(document.getElementById('root')!).render(
    <React.StrictMode>
      {/* Last resort: a throw outside a section (login, wizard, toast host) would
          otherwise leave the panel blank with no way back. */}
      <ErrorBoundary>
        <App />
        <Toaster />
      </ErrorBoundary>
    </React.StrictMode>,
  ),
)
