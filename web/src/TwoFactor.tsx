import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { QRCodeSVG } from 'qrcode.react'
import { disableTOTP, enableTOTP, getTOTP, startTOTP, type TOTPStatus } from './api'
import { useAction } from './hooks'
import { notifySuccess } from './notify'
import { Button, Code, Modal, TextInput } from './ui'
import { useStepUpDialog } from './stepup'

// The admin's own second factor, inside the account dialog. It manages the CALLER and
// nobody else — there is no way to touch another admin's 2FA from the panel, which is
// why an operator who has lost their phone is helped on the server instead:
// `rospanel totp reset <login>`.
//
// The setup deliberately has two steps. Scanning a QR proves nothing; typing a code
// the app produced proves the secret actually landed in it. Until that code arrives
// the panel keeps signing this admin in with the password alone, so a QR that was
// never scanned cannot lock anyone out of their own panel.
// Both actions are re-authorised in a dialog of their own the moment they are asked
// for — the account dialog no longer carries a password field for them to borrow.
export function TwoFactor() {
  const { t } = useTranslation()
  const [status, setStatus] = useState<TOTPStatus | null>(null)
  const [setup, setSetup] = useState<{ secret: string; uri: string } | null>(null)
  const [code, setCode] = useState('')
  const { busy, run } = useAction()
  const { ask, stepUpNode } = useStepUpDialog()

  const reload = () =>
    getTOTP()
      .then(setStatus)
      .catch(() => {})

  useEffect(() => {
    reload()
  }, [])

  const start = async () => {
    const creds = await ask({
      title: t('totp.title'),
      body: t('totp.hint'),
      confirmLabel: t('totp.turnOn'),
    })
    if (!creds) return
    run(async () => {
      const s = await startTOTP(creds.password)
      setSetup(s)
      setCode('')
    })
  }

  const confirm = () =>
    run(async () => {
      await enableTOTP(code)
      setSetup(null)
      setCode('')
      notifySuccess(t('totp.enabled'))
      await reload()
    })

  const turnOff = async () => {
    const creds = await ask({
      title: t('totp.turnOff'),
      body: t('totp.hint'),
      confirmLabel: t('totp.turnOff'),
      danger: true,
    })
    if (!creds) return
    run(async () => {
      await disableTOTP(creds.password)
      notifySuccess(t('totp.disabled'))
      await reload()
    })
  }

  if (!status) return null

  return (
    <div className="flex flex-col gap-3 border-t border-gray-200 pt-4">
      <span className="text-sm font-medium text-ink">{t('totp.title')}</span>
      <p className="text-xs text-ink-muted">{t('totp.hint')}</p>
      {/* On: the only action is removing it, and that costs the password — the whole
          point of a second factor is that a stolen session is not enough by itself. */}
      {status.enabled && (
        <Button size="sm" color="red" variant="outline" loading={busy} onClick={turnOff}>
          {t('totp.turnOff')}
        </Button>
      )}

      {/* Off: the button opens the setup dialog. */}
      {!status.enabled && (
        <Button size="sm" loading={busy} onClick={start}>
          {t('totp.turnOn')}
        </Button>
      )}

      {/* Off, secret issued: scan, then prove it arrived. In a dialog of its own —
          a QR the size of a phone camera's comfort zone does not belong squeezed
          under a form, and the scan-and-type step deserves the screen while it lasts. */}
      <Modal
        open={!!setup && !status.enabled}
        onClose={() => setSetup(null)}
        title={t('totp.title')}
      >
        {setup && (
          <div className="flex flex-col items-center gap-4">
            <div className="rounded-xl bg-white p-3">
              <QRCodeSVG value={setup.uri} size={180} includeMargin />
            </div>
            <p className="text-center text-sm text-ink-muted">{t('totp.scanHint')}</p>
            {/* The secret in text too: some authenticators (and every desktop one) take
                it typed, and a phone camera is not always at hand. */}
            <Code block copy>{setup.secret}</Code>
            <TextInput
              className="text-center"
              label={t('totp.enterCode')}
              value={code}
              onChange={(v) => setCode(v.replace(/\D/g, '').slice(0, 6))}
              placeholder="000000"
              autoFocus
              mono
            />
            <div className="flex w-full justify-end gap-2">
              <Button variant="subtle" color="gray" onClick={() => setSetup(null)}>
                {t('common.cancel')}
              </Button>
              <Button loading={busy} disabled={code.length !== 6} onClick={confirm}>
                {t('totp.confirm')}
              </Button>
            </div>
          </div>
        )}
      </Modal>

      {stepUpNode}
    </div>
  )
}
