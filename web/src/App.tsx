import { useCallback, useEffect, useState } from 'react'
import { getMe, type Role, setUnauthorizedHandler } from './api'
import { Spinner } from './ui'
import { Login } from './Login'
import { Dashboard } from './Dashboard'
import { ForcePassword } from './ForcePassword'
import { Wizard } from './Wizard'
import { Agreement, agreementAccepted } from './Agreement'
import { Donate } from './Donate'
import { BrandProvider } from './brand'
import { RoleProvider } from './role'

// 'password' is where a colleague lands at their first sign-in, still holding the
// temporary password the owner gave them: the server refuses everything else until
// they replace it, so the SPA must not pretend the dashboard is reachable.
type AuthState = 'loading' | 'out' | 'setup' | 'password' | 'in'

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
  const [role, setRole] = useState<Role>('operator')
  const [version, setVersion] = useState('')
  const [billingEnabled, setBillingEnabled] = useState(false)
  const [agreed, setAgreed] = useState(agreementAccepted)
  const [showAgreement, setShowAgreement] = useState(false)
  const [showDonate, setShowDonate] = useState(false)

  const check = useCallback(() => {
    getMe()
      .then((m) => {
        setUsername(m.username)
        setRole(m.role)
        setVersion(m.version)
        setBillingEnabled(!!m.billing_enabled)
        // The first-run wizard covers the owner's own password step, so it wins:
        // an install that hasn't been set up yet goes there, not to the bare
        // password screen. Everyone added later gets the password screen.
        if (!m.setup_done) return setState('setup')
        setState(m.must_change_password ? 'password' : 'in')
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
  } else if (state === 'password') {
    content = <ForcePassword username={username} onDone={check} />
  } else {
    content = (
      <RoleProvider role={role}>
        <Dashboard
          username={username}
          version={version}
          billingEnabled={billingEnabled}
          onLogout={() => setState('out')}
          onShowAgreement={openAgreement}
          onShowDonate={openDonate}
          onAccountChanged={check}
        />
      </RoleProvider>
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
