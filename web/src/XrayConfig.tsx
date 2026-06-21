import { useEffect, useState } from 'react'
import { getXrayConfig } from './api'
import { errMessage } from './notify'
import { Button, IconCheck, IconCopy, ToolDialog, useCopy } from './ui'

export function XrayConfigView({ onClose }: { onClose: () => void }) {
  const [text, setText] = useState('')
  const [err, setErr] = useState('')
  const { copied, copy } = useCopy()

  useEffect(() => {
    getXrayConfig()
      .then(setText)
      .catch((e) => setErr(errMessage(e)))
  }, [])

  return (
    <ToolDialog
      title="Конфигурация Xray"
      onClose={onClose}
      actions={
        <Button
          size="xs"
          variant="light"
          color={copied ? 'teal' : 'gray'}
          disabled={!text}
          onClick={() => copy(text)}
        >
          {copied ? <IconCheck /> : <IconCopy />}
          {copied ? 'Скопировано' : 'Копировать'}
        </Button>
      }
    >
      <div className="flex-1 overflow-auto bg-gray-50 p-3 font-mono text-xs leading-relaxed">
        {err ? (
          <p className="text-red-600">{err}</p>
        ) : text ? (
          <pre className="whitespace-pre-wrap break-all text-gray-700">{text}</pre>
        ) : (
          <p className="text-gray-400">Загрузка…</p>
        )}
      </div>
    </ToolDialog>
  )
}
