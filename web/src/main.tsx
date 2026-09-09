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
// Imported for its side effect: i18next must be initialised before the first
// component renders, or t() returns raw keys on the very first paint.
import './i18n'
import { App } from './App'
import { Toaster } from './toast'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
    <Toaster />
  </React.StrictMode>,
)
