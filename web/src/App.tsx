import { useCallback, useEffect, useState } from 'react'
import { getMe, setUnauthorizedHandler } from './api'
import { Spinner } from './ui'
import { Login } from './Login'
import { Dashboard } from './Dashboard'
import { Wizard } from './Wizard'
import { Agreement, agreementAccepted } from './Agreement'
import { Donate } from './Donate'
import { BrandProvider } from './brand'

type AuthState = 'loading' | 'out' | 'setup' | 'in'

export function App() {
  return (
    <BrandProvider>
      <AppInner />
    </BrandProvider>
  )
}

function AppInner() {
  const [state, setState] = useState<AuthState>('loading')
  const [username, setUsername] = useState('')
  const [version, setVersion] = useState('')
  const [agreed, setAgreed] = useState(agreementAccepted)
  const [showAgreement, setShowAgreement] = useState(false)
  const [showDonate, setShowDonate] = useState(false)

  const check = useCallback(() => {
    getMe()
      .then((m) => {
        setUsername(m.username)
        setVersion(m.version)
        setState(m.setup_done ? 'in' : 'setup')
      })
      .catch(() => setState('out'))
  }, [])

  useEffect(() => {
    check()
  }, [check])

  // Any API 401 (session expired/revoked) drops back to the login screen instead
  // of stranding the user on a dashboard where every action fails silently.
  useEffect(() => {
    setUnauthorizedHandler(() => setState('out'))
  }, [])

  const openAgreement = () => setShowAgreement(true)
  const openDonate = () => setShowDonate(true)

  let content
  if (state === 'loading') {
    content = (
      <div className="flex h-dvh items-center justify-center text-accent">
        <Spinner size={36} />
      </div>
    )
  } else if (state === 'out') {
    content = (
      <Login
        onSuccess={check}
        onShowAgreement={openAgreement}
        onShowDonate={openDonate}
      />
    )
  } else if (state === 'setup') {
    content = <Wizard onDone={check} />
  } else {
    content = (
      <Dashboard
        username={username}
        version={version}
        onLogout={() => setState('out')}
        onShowAgreement={openAgreement}
        onShowDonate={openDonate}
        onAccountChanged={check}
      />
    )
  }

  return (
    <>
      {content}
      {!agreed && <Agreement onAccept={() => setAgreed(true)} />}
      {showAgreement && <Agreement onClose={() => setShowAgreement(false)} />}
      {showDonate && <Donate onClose={() => setShowDonate(false)} />}
    </>
  )
}
