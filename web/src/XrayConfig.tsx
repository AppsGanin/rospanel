import { useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { getNodeXrayConfig, getXrayConfig } from './api'
import { errMessage } from './notify'
import { Button, IconCheck, IconCopy, ToolDialog, useCopy } from './ui'

// XrayConfigView shows an Xray config read-only. Without a nodeId it shows the
// panel server's live config.json; with one it shows that server's config as the
// panel generates it (node 0 resolves to the master's live file too).
export function XrayConfigView({
  nodeId,
  title,
  note,
  onClose,
}: {
  nodeId?: number
  title?: string
  note?: ReactNode
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [text, setText] = useState('')
  const [err, setErr] = useState('')
  const { copied, copy } = useCopy()

  useEffect(() => {
    ;(nodeId === undefined ? getXrayConfig() : getNodeXrayConfig(nodeId))
      .then(setText)
      .catch((e) => setErr(errMessage(e)))
  }, [nodeId])

  return (
    <ToolDialog
      title={title ?? t('xray.configTitle')}
      onClose={onClose}
      headerExtra={note ? <p className="text-xs text-ink-muted">{note}</p> : undefined}
      actions={
        <Button
          size="xs"
          variant="light"
          color={copied ? 'teal' : 'gray'}
          disabled={!text}
          onClick={() => copy(text)}
        >
          {copied ? <IconCheck /> : <IconCopy />}
          {t(copied ? 'common.copied' : 'common.copy')}
        </Button>
      }
    >
      <div className="flex-1 overflow-auto bg-gray-50 p-3 font-mono text-xs leading-relaxed">
        {err ? (
          <p className="text-danger">{err}</p>
        ) : text ? (
          <pre className="whitespace-pre-wrap break-all text-gray-700">{text}</pre>
        ) : (
          <p className="text-gray-400">{t('common.loading')}</p>
        )}
      </div>
    </ToolDialog>
  )
}
